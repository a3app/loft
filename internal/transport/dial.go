package transport

// Dialer start, front selection, replica/roster failover, and Supervise.

import (
	"context"
	"errors"
	"fmt"
	"log"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/a3app/loft/internal/cdn"
	"github.com/a3app/loft/internal/config"
	"github.com/a3app/loft/internal/mux"
	"github.com/a3app/loft/internal/netconf"
	"github.com/a3app/loft/internal/version"
	"github.com/a3app/loft/internal/wire"
)

var (
	lastDialMu sync.Mutex
	lastDial   error
)

// LastDialError is the most recent StartSelected failure (mobile timeout).
func LastDialError() error {
	lastDialMu.Lock()
	defer lastDialMu.Unlock()
	return lastDial
}

func rememberDial(err error) {
	lastDialMu.Lock()
	lastDial = err
	lastDialMu.Unlock()
}

// ClearDialError drops LastDialError. Engine.Start calls this.
func ClearDialError() { rememberDial(nil) }

// clientMemberID is stamped on Hello. Empty is shared-token mode.
var clientMemberID string

// newClientHello builds Hello with caps, member id, instance, and version.
func newClientHello(caps wire.Cap) wire.Hello {
	h := wire.Hello{Caps: caps, MemberID: clientMemberID, InstanceID: EnsureInstance()}
	version.StampHello(&h)
	return h
}

// Dialer is a live front session. Holder swaps one without tearing down inbound.
type Dialer interface {
	Open(target string) (net.Conn, error)
	Session() *mux.Session
	Done() <-chan struct{}
	Close() error
}

// IPPump is a front that carries raw IP datagrams after Status (l3).
// TUN skips the userspace TCP stack and copies packets.
type IPPump interface {
	WriteIP([]byte) error
	ReadIP() ([]byte, error)
}

// rebindable replaces front sockets after an egress change without Park.
// GET does this (HTTP sid is not the client 4-tuple). ws/h2/quic/h3 do not.
type rebindable interface {
	Rebind() error
}

// rebindFront keeps d live across a path change. False means Park + 0x02.
func rebindFront(d Dialer) bool {
	if d == nil {
		return false
	}
	select {
	case <-d.Done():
		return false
	default:
	}
	r, ok := d.(rebindable)
	if !ok {
		return false
	}
	return r.Rebind() == nil
}

// Start opens one outbound using member Hello caps (no CapRelay).
// GET path change rebinds HTTP/2. Other fronts Park; resume is 0x02 + instance_id.
func Start(ctx context.Context, token string, o config.Outbound) (Dialer, error) {
	return startWithOffer(ctx, token, o, wire.MemberCaps)
}

// startWithOffer retries once on AdaptError so SNI can match the node identity.
func startWithOffer(ctx context.Context, token string, o config.Outbound, offer wire.Cap) (Dialer, error) {
	d, err := startFront(ctx, token, o, offer)
	if err != nil {
		var ae *AdaptError
		if errors.As(err, &ae) {
			return startFront(ctx, token, o, offer)
		}
		return nil, err
	}
	return d, nil
}

// startFront constructs get, ws, quic, h2, h3, or l3 and runs Start.
func startFront(ctx context.Context, token string, o config.Outbound, offer wire.Cap) (Dialer, error) {
	noteKEX("")
	if o.CDN != "" {
		if p, err := cdn.Lookup(o.CDN); err == nil && o.Type == "" {
			o.Type = p.Front
		}
	}
	front, err := normalizeType(o.Type)
	if err != nil {
		return nil, err
	}
	if !markReady(o) {
		return nil, fmt.Errorf("%s: mask pub unknown", front)
	}
	if front == wire.FrontL3 {
		offer |= wire.CapL3
	} else {
		offer &^= wire.CapL3
	}
	switch front {
	case "get":
		y, err := NewGET(token, o)
		if err != nil {
			return nil, err
		}
		y.offer = offer
		if err := y.Start(ctx); err != nil {
			return nil, err
		}
		// Overlay STATUS is the GET session. Extra fronts attach to the
		// node URL in the background and share this mux.
		go y.attachExtras(ctx)
		return y, nil
	case "ws":
		w := NewWSS(token, o)
		w.offer = offer
		if err := w.Start(ctx); err != nil {
			return nil, err
		}
		return w, nil
	case "quic":
		q := NewQUIC(token, o)
		q.offer = offer
		if err := q.Start(ctx); err != nil {
			return nil, err
		}
		return q, nil
	case "h2":
		h := NewH2(token, o, offer)
		if err := h.Start(ctx); err != nil {
			return nil, err
		}
		return h, nil
	case "h3":
		h := NewH3(token, o, offer)
		if err := h.Start(ctx); err != nil {
			return nil, err
		}
		return h, nil
	case "l3":
		l := NewL3(token, o, offer)
		if err := l.Start(ctx); err != nil {
			return nil, err
		}
		return l, nil
	default:
		return nil, fmt.Errorf("unknown outbound type %q (want get, ws, h2, quic, h3 or l3)", o.Type)
	}
}

// Supervise keeps a dialer up across metro / LTE drops: TUN stays.
// GET rebinds HTTP/2 (same sid). Other fronts Park; resume is 0x02 + instance_id.
func Supervise(ctx context.Context, cfg *config.ClientConfig, use func(Dialer, config.Outbound)) error {
	return SuperviseOn(ctx, cfg, WatchPath(ctx), use)
}

// SuperviseOn is Supervise with an explicit path-change signal (mobile OS
// notifies; WatchPath is wrong inside a full tunnel).
func SuperviseOn(ctx context.Context, cfg *config.ClientConfig, path <-chan struct{}, use func(Dialer, config.Outbound)) error {
	if cfg != nil {
		clientMemberID = cfg.MemberID
		if cfg.InstanceID != "" {
			RememberInstance(cfg.InstanceID)
		}
	}
	EnsureInstance()
	if cfg != nil {
		SetPauseSchedule(cfg.Pause)
	}
	if path == nil {
		path = make(chan struct{})
	}
	delay := time.Duration(0)
	for {
		if delay > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-path:
				delay = 0
			case <-time.After(delay):
			}
		}
		d, o, err := StartSelected(ctx, cfg)
		if err != nil {
			rememberDial(err)
			log.Printf("loft dial: %v", err)
			if fatalFront(err) {
				return err
			}
			rotateN.Add(1)
			if delay < 8*time.Second {
				if delay == 0 {
					delay = 200 * time.Millisecond
				} else {
					delay *= 2
				}
			}
			continue
		}
		delay = 0
		SetP2PClient(cfg.P2P == nil || *cfg.P2P)
		acceptMember(d)
		if cfg.P2P == nil || *cfg.P2P {
			go announceP2P(d)
		}
		use(d, o)
		var ttlC <-chan time.Time
		var ttlTimer *time.Timer
		if ms := sessionTTLMS(); ms > 0 {
			wait := time.Duration(ms) * 80 / 100 * time.Millisecond
			if wait < time.Millisecond {
				wait = time.Millisecond
			}
			ttlTimer = time.NewTimer(wait)
			ttlC = ttlTimer.C
		}
		alive := true
		for alive {
			select {
			case <-d.Done():
				_ = parkOrClose(d)
				rotateN.Add(1)
				delay = 200 * time.Millisecond
				alive = false
			case <-path:
				if rebindFront(d) {
					continue
				}
				_ = parkOrClose(d)
				rotateN.Add(1)
				delay = 0
				alive = false
			case <-ttlC:
				_ = d.Close()
				rotateN.Add(1)
				delay = 200 * time.Millisecond
				alive = false
			case <-ctx.Done():
				_ = d.Close()
				clearClientHold()
				if ttlTimer != nil {
					ttlTimer.Stop()
				}
				return ctx.Err()
			}
		}
		if ttlTimer != nil {
			ttlTimer.Stop()
		}
	}
}

// fatalFront is a node Status the radio cannot fix: stop Supervise retries
// so the GUI can show the credentials / policy error.
func fatalFront(err error) bool {
	var we wire.Error
	if !errors.As(err, &we) {
		return false
	}
	switch we.Code {
	case wire.AuthBadKey, wire.AuthRevoked, wire.AuthExpired, wire.AuthNotAuthorized,
		wire.AuthClock, wire.AuthInstance, wire.NetDisabled:
		return true
	default:
		return false
	}
}

// StartSelected tries filtered outbounds, then replicas, then the signed roster.
func StartSelected(ctx context.Context, cfg *config.ClientConfig) (Dialer, config.Outbound, error) {
	BootstrapRoster(ctx, cfg)
	BootstrapLists(ctx, cfg)
	list := filterOutbounds(cfg, cfg.Pick(cfg.Select))
	if len(list) == 0 {
		return nil, config.Outbound{}, fmt.Errorf("no outbound named %q", cfg.Select)
	}
	offer := helloCaps(cfg)
	var last error
	// First front gets a longer budget (CDN origin can sit ~5s). The rest
	// must still fit in the GUI 25s watchdog: a stuck SYN used to hold
	// DialTLS with no context until the OS gave up.
	tryBudget := func(o config.Outbound, budget time.Duration) (Dialer, error) {
		if !markReady(o) {
			return nil, fmt.Errorf("%s: mask pub unknown", o.Type)
		}
		cctx, cancel := context.WithTimeout(ctx, budget)
		defer cancel()
		return startWithOffer(cctx, cfg.Token, o, offer)
	}
	try := func(o config.Outbound) (Dialer, error) {
		return tryBudget(o, nextDialBudget)
	}
	for i, o := range list {
		budget := nextDialBudget
		if i == 0 {
			budget = firstDialBudget
		}
		d, err := tryBudget(o, budget)
		if err != nil {
			log.Printf("loft dial %s: %v", o.Type, err)
			last = fmt.Errorf("%s: %w", o.Name, err)
			if d, o2, err2 := tryReplicas(ctx, try, o); err2 == nil {
				return d, o2, nil
			}
			if d, o2, err2 := tryRoster(ctx, try, o); err2 == nil {
				return d, o2, nil
			}
			continue
		}
		return d, o, nil
	}
	if last == nil {
		last = fmt.Errorf("no outbound connected")
	}
	return nil, config.Outbound{}, last
}

// tryReplicas redials o at Status replica hosts.
func tryReplicas(ctx context.Context, try func(config.Outbound) (Dialer, error), o config.Outbound) (Dialer, config.Outbound, error) {
	addr, err := outboundAddr(o)
	if err != nil {
		return nil, o, err
	}
	var last error
	for _, host := range CachedReplicas(addr) {
		o2 := o
		o2.URL = replaceURLHost(o.URL, host)
		d, err := try(o2)
		if err != nil {
			last = err
			continue
		}
		return d, o2, nil
	}
	if last == nil {
		last = fmt.Errorf("no replicas")
	}
	return nil, o, last
}

// tryRoster redials using live roster entries.
func tryRoster(ctx context.Context, try func(config.Outbound) (Dialer, error), o config.Outbound) (Dialer, config.Outbound, error) {
	var last error
	for _, e := range CachedRoster() {
		o2 := o
		o2.Name = e.ID
		o2.URL = e.URL
		if e.Decoy != "" {
			o2.Decoy = e.Decoy
		}
		if e.Path != "" {
			o2.Path = e.Path
		}
		if len(e.Fronts) == 1 {
			o2.Type = e.Fronts[0]
		} else if len(e.Fronts) > 0 && o2.Type != "" {
			ok := false
			for _, f := range e.Fronts {
				if f == o2.Type {
					ok = true
					break
				}
			}
			if !ok {
				o2.Type = e.Fronts[0]
			}
		}
		if len(e.MaskPub) == 32 {
			if addr, err := outboundAddr(o2); err == nil {
				RememberMaskPub(addr, e.MaskPub)
			}
		}
		d, err := try(o2)
		if err != nil {
			last = err
			continue
		}
		return d, o2, nil
	}
	if last == nil {
		last = fmt.Errorf("no roster")
	}
	return nil, o, last
}

// replaceURLHost sets URL host (and port if present) to hostport.
func replaceURLHost(raw, hostport string) string {
	u, err := parseURL(raw)
	if err != nil {
		return raw
	}
	host, port, err := net.SplitHostPort(hostport)
	if err != nil {
		host = hostport
		if u.Port() != "" {
			port = u.Port()
		}
	}
	if port == "" {
		u.Host = host
	} else {
		u.Host = net.JoinHostPort(host, port)
	}
	return u.String()
}

// First dial may wait out a slow CDN origin fetch. Later fronts stay short
// so get, ws and h2 all fit under the macOS helper's 25s watchdog.
const (
	firstDialBudget = 8 * time.Second
	nextDialBudget  = 5 * time.Second
)

// markReady is false for quic/h3 until mask_pub is known. A v2 Initial on a
// mask node is spliced to decoy and the dial sits until HandshakeIdleTimeout.
func markReady(o config.Outbound) bool {
	switch strings.ToLower(strings.TrimSpace(o.Type)) {
	case wire.FrontQUIC, wire.FrontH3:
	default:
		return true
	}
	if len(ParseMaskPub(o.MaskPub)) == 32 {
		return true
	}
	addr, err := outboundAddr(o)
	if err != nil {
		return false
	}
	loadFrontCache(addr)
	return len(CachedMaskPub(addr)) == 32
}

// normalizeType maps a front name to the wire token (get, ws, h2, quic, h3, l3).
func normalizeType(t string) (string, error) { return netconf.NormalizeFront(t) }

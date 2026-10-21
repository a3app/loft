package egress

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// SSTPConn is a held SSTP session that can open TCP and UDP through it
// (the node wires clients/go/inbound.SSTPNet in; this package does not
// import it, to keep transport free of client code).
type SSTPConn interface {
	Open(target string) (net.Conn, error)
	ListenUDP() (net.PacketConn, error)
	Done() <-chan struct{}
	Close() error
	OverlayIP() net.IP
}

// SSTPFunc connects one SSTP channel.
type SSTPFunc func(ctx context.Context, ch Channel) (SSTPConn, error)

// Options are what the router needs besides egress.conf.
type Options struct {
	Lists  map[string][]string // node.conf lists: domain, .suffix, CIDR
	SSTP   SSTPFunc
	SysNet string // /sys/class/net (tests)
	Log    func(string, ...any)
}

// Who is the member a connection belongs to; empty for traffic without one.
type Who struct {
	Member string
	Tags   []string
}

type chanState struct {
	ch        Channel
	tx, rx    atomic.Uint64
	conns     atomic.Int64
	mu        sync.Mutex
	downUntil time.Time
	lastErr   string
	sstp      SSTPConn
	sstpState string
}

// Router applies one egress.conf. Make a new one on reload and Close the old.
type Router struct {
	cfg    *Config
	opt    Options
	ch     map[string]*chanState
	ctx    context.Context
	cancel context.CancelFunc
}

// dialTimeout bounds one connect through any channel.
const dialTimeout = 10 * time.Second

// downFor is how long a channel that failed a connect is skipped.
const downFor = 20 * time.Second

// New starts a router for cfg; SSTP channels connect in the background.
func New(cfg *Config, opt Options) *Router {
	if opt.SysNet == "" {
		opt.SysNet = "/sys/class/net"
	}
	if opt.Log == nil {
		opt.Log = func(string, ...any) {}
	}
	r := &Router{cfg: cfg, opt: opt, ch: map[string]*chanState{}}
	r.ctx, r.cancel = context.WithCancel(context.Background())
	for _, c := range cfg.Channels {
		cs := &chanState{ch: c}
		r.ch[c.Name] = cs
		if c.Kind == KindSSTP && !c.Off {
			go r.holdSSTP(cs)
		}
	}
	return r
}

// Close stops SSTP sessions; open connections keep running.
func (r *Router) Close() {
	r.cancel()
	for _, cs := range r.ch {
		cs.mu.Lock()
		if cs.sstp != nil {
			cs.sstp.Close()
		}
		cs.mu.Unlock()
	}
}

// Config is what the router runs.
func (r *Router) Config() *Config { return r.cfg }

func (r *Router) holdSSTP(cs *chanState) {
	wait := 5 * time.Second
	for r.ctx.Err() == nil {
		cs.mu.Lock()
		cs.sstpState = "connecting"
		cs.mu.Unlock()
		if r.opt.SSTP == nil {
			r.fail(cs, errors.New("this node has no SSTP client"))
			return
		}
		ctx, cancel := context.WithTimeout(r.ctx, 30*time.Second)
		s, err := r.opt.SSTP(ctx, cs.ch)
		cancel()
		if err != nil {
			r.fail(cs, err)
			r.opt.Log("egress: %s: %v", cs.ch.Name, err)
			select {
			case <-r.ctx.Done():
				return
			case <-time.After(wait):
			}
			if wait < time.Minute {
				wait *= 2
			}
			continue
		}
		wait = 5 * time.Second
		cs.mu.Lock()
		cs.sstp, cs.sstpState, cs.lastErr = s, "up", ""
		cs.mu.Unlock()
		r.opt.Log("egress: %s: SSTP up, %s", cs.ch.Name, s.OverlayIP())
		select {
		case <-r.ctx.Done():
			s.Close()
			return
		case <-s.Done():
		}
		cs.mu.Lock()
		cs.sstp = nil
		cs.mu.Unlock()
		r.opt.Log("egress: %s: SSTP session ended", cs.ch.Name)
	}
}

func (r *Router) fail(cs *chanState, err error) {
	cs.mu.Lock()
	cs.lastErr = err.Error()
	cs.downUntil = time.Now().Add(downFor)
	cs.mu.Unlock()
}

// up reports whether a channel may be tried now.
func (r *Router) up(cs *chanState) bool {
	cs.mu.Lock()
	down := time.Now().Before(cs.downUntil)
	sstp := cs.sstp
	cs.mu.Unlock()
	if cs.ch.Off {
		return false
	}
	switch cs.ch.Kind {
	case KindDev:
		b, err := os.ReadFile(filepath.Join(r.opt.SysNet, cs.ch.Dev, "operstate"))
		if err != nil || strings.TrimSpace(string(b)) == "down" {
			return false
		}
	case KindSSTP:
		if sstp == nil {
			return false
		}
	}
	return !down
}

// Route is where traffic of who to host goes first: a channel, direct or block.
func (r *Router) Route(who Who, host string) string {
	for _, ru := range r.cfg.Rules {
		ok := false
		switch ru.Match {
		case "member":
			ok = who.Member != "" && ru.Value == who.Member
		case "tag":
			for _, t := range who.Tags {
				ok = ok || strings.EqualFold(t, ru.Value)
			}
		case "list":
			ok = inList(r.opt.Lists[ru.Value], host)
		}
		if ok {
			return ru.Via
		}
	}
	if r.cfg.Default == "" {
		return Direct
	}
	return r.cfg.Default
}

func inList(entries []string, host string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	ip := net.ParseIP(host)
	for _, e := range entries {
		e = strings.ToLower(strings.TrimSpace(e))
		switch {
		case strings.Contains(e, "/"):
			if _, n, err := net.ParseCIDR(e); err == nil && ip != nil && n.Contains(ip) {
				return true
			}
		case strings.HasPrefix(e, "."):
			if host == e[1:] || strings.HasSuffix(host, e) {
				return true
			}
		default:
			if host == e || (ip != nil && ip.Equal(net.ParseIP(e))) {
				return true
			}
		}
	}
	return false
}

var errBlocked = errors.New("blocked by the exit rules")

// steps walks a route through fail links: every hop it may try in order.
func (r *Router) steps(first string) []string {
	var out []string
	seen := map[string]bool{}
	for s := first; s != "" && !seen[s]; {
		seen[s] = true
		out = append(out, s)
		if s == Direct || s == Block {
			break
		}
		cs := r.ch[s]
		if cs == nil {
			break
		}
		s = cs.ch.Fail
		if s == "" {
			s = Direct
		}
	}
	return out
}

// DialTCP opens a TCP connection for who to target along the rules; it
// returns the step that carried it (a channel name or direct).
func (r *Router) DialTCP(ctx context.Context, who Who, target string) (net.Conn, string, error) {
	host, _, err := net.SplitHostPort(target)
	if err != nil {
		return nil, "", err
	}
	var last error = errBlocked
	for _, s := range r.steps(r.Route(who, host)) {
		switch s {
		case Direct:
			c, err := (&net.Dialer{Timeout: dialTimeout}).DialContext(ctx, "tcp4", target)
			return c, Direct, err
		case Block:
			return nil, Block, last
		}
		cs := r.ch[s]
		if cs == nil || !r.up(cs) {
			continue
		}
		cctx, cancel := context.WithTimeout(ctx, dialTimeout)
		c, err := r.dialOn(cctx, cs, target)
		cancel()
		if err == nil {
			return r.count(cs, c), s, nil
		}
		last = fmt.Errorf("%s: %v", s, err)
		r.fail(cs, err)
	}
	return nil, "", last
}

func (r *Router) dialOn(ctx context.Context, cs *chanState, target string) (net.Conn, error) {
	switch cs.ch.Kind {
	case KindDev:
		return (&net.Dialer{Control: bindDev(cs.ch.Dev)}).DialContext(ctx, "tcp4", target)
	case KindSOCKS5:
		return socksDial(ctx, r.under(cs.ch.Via), &cs.ch, target)
	case KindHTTP:
		return httpDial(ctx, r.under(cs.ch.Via), &cs.ch, target)
	case KindSSTP:
		cs.mu.Lock()
		s := cs.sstp
		cs.mu.Unlock()
		if s == nil {
			return nil, errors.New("SSTP is not connected")
		}
		return s.Open(target)
	}
	return nil, fmt.Errorf("unknown channel kind %q", cs.ch.Kind)
}

// under is how a proxy itself is reached: directly or through via.
func (r *Router) under(via string) dialFn {
	if cs := r.ch[via]; via != "" && cs != nil {
		return func(ctx context.Context, _, addr string) (net.Conn, error) { return r.dialOn(ctx, cs, addr) }
	}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		return (&net.Dialer{Timeout: dialTimeout}).DialContext(ctx, network, addr)
	}
}

type counted struct {
	net.Conn
	cs   *chanState
	once sync.Once
}

func (c *counted) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	c.cs.rx.Add(uint64(n))
	return n, err
}

func (c *counted) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	c.cs.tx.Add(uint64(n))
	return n, err
}

func (c *counted) Close() error {
	c.once.Do(func() { c.cs.conns.Add(-1) })
	return c.Conn.Close()
}

func (r *Router) count(cs *chanState, c net.Conn) net.Conn {
	cs.conns.Add(1)
	return &counted{Conn: c, cs: cs}
}

// Status is one channel as the node sees it.
type Status struct {
	Name  string `json:"name"`
	Kind  string `json:"kind"`
	State string `json:"state"` // up | down | connecting | off
	Error string `json:"error,omitempty"`
	TX    uint64 `json:"tx"`
	RX    uint64 `json:"rx"`
	Conns int64  `json:"conns"`
	IP    string `json:"ip,omitempty"` // SSTP: the address the server gave
}

// Status lists every channel.
func (r *Router) Status() []Status {
	out := make([]Status, 0, len(r.cfg.Channels))
	for _, c := range r.cfg.Channels {
		cs := r.ch[c.Name]
		st := Status{Name: c.Name, Kind: c.Kind, State: "up", TX: cs.tx.Load(), RX: cs.rx.Load(), Conns: cs.conns.Load()}
		if !r.up(cs) {
			st.State = "down"
		}
		if c.Off {
			st.State = "off"
		}
		cs.mu.Lock()
		st.Error = cs.lastErr
		if c.Kind == KindSSTP {
			if cs.sstp != nil {
				st.IP = cs.sstp.OverlayIP().String()
			} else if cs.sstpState == "connecting" {
				st.State = "connecting"
			}
		}
		cs.mu.Unlock()
		out = append(out, st)
	}
	return out
}

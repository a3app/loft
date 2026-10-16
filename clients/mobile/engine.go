package mobile

import (
	"context"
	"fmt"
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/a3app/loft/clients/go/inbound"
	"github.com/a3app/loft/internal/config"
	"github.com/a3app/loft/internal/protect"
	"github.com/a3app/loft/internal/sstp"
	"github.com/a3app/loft/internal/transport"
	"github.com/a3app/loft/internal/trojan"
	"github.com/a3app/loft/internal/vless"
)

const (
	startWait = 30 * time.Second
	loftMTU   = 1280
)

// Snapshot is gomobile-friendly session state. DNS and FrontIPs are comma-separated.
type Snapshot struct {
	Kind       string
	IP         string
	Gateway    string
	MTU        int
	DNS        string
	FrontIPs   string
	BytesUp    int64
	BytesDown  int64
	State      string
	Err        string
	InstanceID string
}

// Engine is one overlay/VPN session for a Packet Tunnel / VpnService.
type Engine struct {
	mu     sync.Mutex
	state  string
	err    string
	kind   string
	mtu    int
	dns    string
	fronts string

	ctx    context.Context
	cancel context.CancelFunc
	pathCh chan struct{}
	pipe   *packetPipe
	sink   PacketSink
	stack  *inbound.Device
	holder *transport.Holder
	vless  *vless.Client
	trojan *trojan.Client
	sstp   *sstp.Session

	bytesUp   atomic.Int64
	bytesDown atomic.Int64
}

// NewEngine returns an idle session.
func NewEngine() *Engine {
	return &Engine{state: "idle", mtu: loftMTU}
}

// Start brings the session up from client YAML. Blocks until the first
// Dial / Status or startWait. Path change is NotifyPathChange, not WatchPath.
func (e *Engine) Start(yamlStr string) error {
	e.mu.Lock()
	if e.state == "connecting" || e.state == "on" {
		e.mu.Unlock()
		return fmt.Errorf("already started")
	}
	e.mu.Unlock()

	cfg, extra, err := decodeFile([]byte(yamlStr))
	if err != nil {
		e.setErr(err)
		return err
	}
	kind := primaryType(cfg)
	if kind == "" {
		err = fmt.Errorf("no outbound")
		e.setErr(err)
		return err
	}
	switch {
	case kind == "sstp", kind == "vless", kind == "trojan":
	case loftTypes[kind]:
		if len(loftOnly(cfg).Outbounds) == 0 {
			err = fmt.Errorf("no loft outbound")
			e.setErr(err)
			return err
		}
	default:
		err = fmt.Errorf("unknown outbound type %q", kind)
		e.setErr(err)
		return err
	}

	fronts := joinComma(ResolveFrontIPs(FrontHosts(cfg)))
	ctx, cancel := context.WithCancel(context.Background())
	pipe := newPacketPipe()
	pathCh := make(chan struct{}, 1)
	ready := make(chan error, 1)

	e.mu.Lock()
	e.ctx = ctx
	e.cancel = cancel
	e.pathCh = pathCh
	e.pipe = pipe
	if e.sink != nil {
		pipe.setSink(e.sink)
	}
	e.stack = nil
	e.holder = nil
	e.vless = nil
	e.trojan = nil
	e.sstp = nil
	e.kind = kind
	e.fronts = fronts
	e.dns = ""
	e.mtu = loftMTU
	e.state = "connecting"
	e.err = ""
	e.bytesUp.Store(0)
	e.bytesDown.Store(0)
	e.mu.Unlock()

	go e.run(ctx, cfg, extra, kind, pipe, pathCh, ready)

	select {
	case err := <-ready:
		if err != nil {
			e.Stop()
			e.setErr(err)
			return err
		}
		return nil
	case <-time.After(startWait):
		e.Stop()
		err = fmt.Errorf("timeout")
		e.setErr(err)
		return err
	}
}

// Stop tears the session down. Idle is a no-op.
func (e *Engine) Stop() {
	e.mu.Lock()
	if e.state == "idle" {
		e.mu.Unlock()
		return
	}
	cancel := e.cancel
	e.cancel = nil
	stack := e.stack
	e.stack = nil
	h := e.holder
	e.holder = nil
	sess := e.sstp
	e.sstp = nil
	vc := e.vless
	e.vless = nil
	tc := e.trojan
	e.trojan = nil
	pipe := e.pipe
	e.pipe = nil
	e.pathCh = nil
	e.ctx = nil
	e.state = "stopped"
	e.mu.Unlock()

	if cancel != nil {
		cancel()
	}
	if h != nil {
		_ = h.Close()
	}
	if sess != nil {
		_ = sess.Close()
	}
	if vc != nil {
		_ = vc.Close()
	}
	if tc != nil {
		_ = tc.Close()
	}
	if stack != nil {
		_ = stack.Close()
	} else if pipe != nil {
		_ = pipe.Close()
	}
}

// InjectPacket is one IP packet from the OS (packetFlow / VpnService).
func (e *Engine) InjectPacket(pkt []byte) error {
	e.mu.Lock()
	p := e.pipe
	e.mu.Unlock()
	if p == nil {
		return fmt.Errorf("not started")
	}
	return p.inject(pkt)
}

// PullPacket is one IP packet toward the OS. Nil if the queue is empty.
func (e *Engine) PullPacket() []byte {
	e.mu.Lock()
	p := e.pipe
	e.mu.Unlock()
	if p == nil {
		return nil
	}
	return p.pull()
}

// NotifyPathChange is Wi-Fi ↔ LTE from the OS. Non-blocking.
// loft: close the front, ticket 0x03, same instance_id. SSTP/VLESS/Trojan: redial.
func (e *Engine) NotifyPathChange() {
	e.mu.Lock()
	ch := e.pathCh
	e.mu.Unlock()
	if ch == nil {
		return
	}
	select {
	case ch <- struct{}{}:
	default:
	}
}

// Snapshot is the live tunnel settings for the OS adapter.
func (e *Engine) Snapshot() *Snapshot {
	e.mu.Lock()
	s := &Snapshot{
		Kind:       e.kind,
		MTU:        e.mtu,
		DNS:        e.dns,
		FrontIPs:   e.fronts,
		State:      e.state,
		Err:        e.err,
		InstanceID: transport.ClientInstance(),
		BytesUp:    e.bytesUp.Load(),
		BytesDown:  e.bytesDown.Load(),
	}
	h := e.holder
	vc := e.vless
	tc := e.trojan
	kind := e.kind
	e.mu.Unlock()
	s.IP = transport.OverlayIP()
	s.Gateway = transport.OverlayGateway()
	if dns := transport.OverlayDNS(); len(dns) > 0 {
		s.DNS = joinComma(dns)
	}
	switch {
	case h != nil && loftTypes[kind]:
		up, down := h.Bytes()
		s.BytesUp, s.BytesDown = int64(up), int64(down)
	case vc != nil:
		up, down := vc.Bytes()
		s.BytesUp, s.BytesDown = int64(up), int64(down)
	case tc != nil:
		up, down := tc.Bytes()
		s.BytesUp, s.BytesDown = int64(up), int64(down)
	}
	return s
}

// Status is Snapshot under the name in the catalog API.
func (e *Engine) Status() *Snapshot { return e.Snapshot() }

// SocketProtector is Android VpnService.protect. iOS leaves this unset.
type SocketProtector interface {
	Protect(fd int) bool
}

// SetProtector marks new outbound fds off the tunnel. Call before Start.
func (e *Engine) SetProtector(p SocketProtector) {
	if p == nil {
		protect.Set(nil)
		return
	}
	protect.Set(p.Protect)
}

// SetSink delivers packets toward the OS. Exclusive with PullPacket.
func (e *Engine) SetSink(s PacketSink) {
	e.mu.Lock()
	e.sink = s
	p := e.pipe
	e.mu.Unlock()
	if p != nil {
		p.setSink(s)
	}
}

func (e *Engine) setErr(err error) {
	e.mu.Lock()
	e.state = "error"
	if err != nil {
		e.err = err.Error()
	}
	e.mu.Unlock()
}

func (e *Engine) addBytes(up, down int64) {
	if up != 0 {
		e.bytesUp.Add(up)
	}
	if down != 0 {
		e.bytesDown.Add(down)
	}
}

func (e *Engine) run(ctx context.Context, cfg *config.ClientConfig, extra fileExtra, kind string, pipe *packetPipe, path <-chan struct{}, ready chan<- error) {
	switch {
	case kind == "sstp":
		e.runSSTP(ctx, cfg, extra, pipe, path, ready)
	case kind == "vless":
		e.runVLESS(ctx, cfg, extra, pipe, path, ready)
	case kind == "trojan":
		e.runTrojan(ctx, cfg, extra, pipe, path, ready)
	default:
		e.runLoft(ctx, cfg, pipe, path, ready)
	}
}

func (e *Engine) runLoft(ctx context.Context, cfg *config.ClientConfig, pipe *packetPipe, path <-chan struct{}, ready chan<- error) {
	h := &transport.Holder{}
	e.mu.Lock()
	e.holder = h
	e.mu.Unlock()
	only := loftOnly(cfg)
	var once sync.Once
	signal := func(err error) { once.Do(func() { ready <- err }) }
	err := transport.SuperviseOn(ctx, only, path, func(d transport.Dialer, o config.Outbound) {
		h.Set(d)
		e.mu.Lock()
		st := e.stack
		e.kind = o.Type
		e.mtu = loftMTU
		e.mu.Unlock()
		if st == nil {
			ip := transport.OverlayIP()
			dev, err := inbound.StartStack(pipe, "pkt", h, ip)
			if err != nil {
				signal(err)
				return
			}
			e.mu.Lock()
			e.stack = dev
			e.state = "on"
			e.err = ""
			e.dns = joinComma(transport.OverlayDNS())
			e.mu.Unlock()
			signal(nil)
			return
		}
		st.Attach(h)
		e.mu.Lock()
		e.state = "on"
		e.err = ""
		e.mu.Unlock()
	})
	signal(err)
}

func (e *Engine) runSSTP(ctx context.Context, cfg *config.ClientConfig, extra fileExtra, pipe *packetPipe, path <-chan struct{}, ready chan<- error) {
	o, ok := firstOutbound(cfg, "sstp")
	if !ok {
		ready <- fmt.Errorf("no sstp outbound")
		return
	}
	insecure := o.Insecure
	if extra.tlsCheck != nil {
		insecure = !*extra.tlsCheck
	}
	pass := extra.password
	if pass == "" {
		pass = cfg.Token
	}
	scfg := sstp.Config{
		URL:      o.URL,
		User:     extra.username,
		Password: pass,
		Insecure: insecure,
		PAPCHAP:  extra.pap,
		SNI:      o.SNI,
	}
	var once sync.Once
	signal := func(err error) { once.Do(func() { ready <- err }) }
	delay := time.Duration(0)
	for {
		if delay > 0 {
			select {
			case <-ctx.Done():
				signal(ctx.Err())
				return
			case <-path:
				delay = 0
			case <-time.After(delay):
			}
		}
		child, cancel := context.WithCancel(ctx)
		go func() {
			select {
			case <-child.Done():
			case <-path:
				cancel()
			}
		}()
		sess, err := sstp.Dial(child, scfg)
		if err != nil {
			cancel()
			if delay == 0 {
				delay = 200 * time.Millisecond
			} else if delay < 8*time.Second {
				delay *= 2
			}
			continue
		}
		delay = 0
		ip := sess.IP.String()
		gw := ""
		if p := sess.Peer.To4(); p != nil {
			gw = p.String()
		}
		transport.RememberOverlay(ip, gw)
		e.mu.Lock()
		e.sstp = sess
		e.kind = "sstp"
		e.mtu = sess.MTU()
		e.dns = inbound.JoinDNS(sess.DNS)
		e.state = "on"
		e.err = ""
		e.mu.Unlock()
		signal(nil)
		_ = e.pumpSSTP(child, sess, pipe)
		_ = sess.Close()
		cancel()
		e.mu.Lock()
		if e.sstp == sess {
			e.sstp = nil
		}
		if e.state == "on" {
			e.state = "connecting"
		}
		e.mu.Unlock()
		if ctx.Err() != nil {
			return
		}
		delay = 200 * time.Millisecond
	}
}

func (e *Engine) pumpSSTP(ctx context.Context, sess *sstp.Session, pipe *packetPipe) error {
	errc := make(chan error, 2)
	go func() {
		for {
			select {
			case <-ctx.Done():
				errc <- ctx.Err()
				return
			case pkt, ok := <-pipe.in:
				if !ok {
					errc <- io.EOF
					return
				}
				if err := sess.WriteIP(pkt); err != nil {
					errc <- err
					return
				}
				e.addBytes(int64(len(pkt)), 0)
			}
		}
	}()
	go func() {
		for {
			ip, err := sess.ReadIP()
			if err != nil {
				errc <- err
				return
			}
			if _, err := pipe.Write(ip); err != nil {
				errc <- err
				return
			}
			e.addBytes(0, int64(len(ip)))
		}
	}()
	select {
	case <-ctx.Done():
		_ = sess.Close()
		return ctx.Err()
	case err := <-errc:
		return err
	}
}

func (e *Engine) runVLESS(ctx context.Context, cfg *config.ClientConfig, extra fileExtra, pipe *packetPipe, path <-chan struct{}, ready chan<- error) {
	o, ok := firstOutbound(cfg, "vless")
	if !ok {
		ready <- fmt.Errorf("no vless outbound")
		return
	}
	insecure := o.Insecure
	if extra.tlsCheck != nil {
		insecure = !*extra.tlsCheck
	}
	var once sync.Once
	signal := func(err error) { once.Do(func() { ready <- err }) }
	delay := time.Duration(0)
	for {
		if delay > 0 {
			select {
			case <-ctx.Done():
				signal(ctx.Err())
				return
			case <-path:
				delay = 0
			case <-time.After(delay):
			}
		}
		cli, err := vless.New(vless.Config{
			URL:      o.URL,
			UUID:     cfg.Token,
			SNI:      o.SNI,
			Insecure: insecure,
		})
		if err != nil {
			signal(err)
			return
		}
		if err := cli.Probe(ctx); err != nil {
			_ = cli.Close()
			if ctx.Err() != nil {
				signal(ctx.Err())
				return
			}
			if delay == 0 {
				delay = 200 * time.Millisecond
			} else if delay < 8*time.Second {
				delay *= 2
			}
			continue
		}
		delay = 0
		transport.RememberOverlay(vless.TunIP, vless.TunIP)
		e.mu.Lock()
		st := e.stack
		e.vless = cli
		e.kind = "vless"
		e.mtu = loftMTU
		e.state = "on"
		e.err = ""
		e.mu.Unlock()
		if st == nil {
			dev, err := inbound.StartStack(pipe, "pkt", cli, vless.TunIP)
			if err != nil {
				_ = cli.Close()
				signal(err)
				return
			}
			e.mu.Lock()
			e.stack = dev
			e.mu.Unlock()
		} else {
			st.Attach(cli)
		}
		signal(nil)
		select {
		case <-ctx.Done():
			_ = cli.Close()
			return
		case <-path:
			_ = cli.Close()
		case <-cli.Done():
		}
		e.mu.Lock()
		if e.vless == cli {
			e.vless = nil
		}
		if e.state == "on" {
			e.state = "connecting"
		}
		e.mu.Unlock()
		if ctx.Err() != nil {
			return
		}
		delay = 200 * time.Millisecond
	}
}

func (e *Engine) runTrojan(ctx context.Context, cfg *config.ClientConfig, extra fileExtra, pipe *packetPipe, path <-chan struct{}, ready chan<- error) {
	o, ok := firstOutbound(cfg, "trojan")
	if !ok {
		ready <- fmt.Errorf("no trojan outbound")
		return
	}
	insecure := o.Insecure
	if extra.tlsCheck != nil {
		insecure = !*extra.tlsCheck
	}
	var once sync.Once
	signal := func(err error) { once.Do(func() { ready <- err }) }
	delay := time.Duration(0)
	for {
		if delay > 0 {
			select {
			case <-ctx.Done():
				signal(ctx.Err())
				return
			case <-path:
				delay = 0
			case <-time.After(delay):
			}
		}
		cli, err := trojan.New(trojan.Config{
			URL:      o.URL,
			Password: cfg.Token,
			SNI:      o.SNI,
			Insecure: insecure,
		})
		if err != nil {
			signal(err)
			return
		}
		if err := cli.Probe(ctx); err != nil {
			_ = cli.Close()
			if ctx.Err() != nil {
				signal(ctx.Err())
				return
			}
			if delay == 0 {
				delay = 200 * time.Millisecond
			} else if delay < 8*time.Second {
				delay *= 2
			}
			continue
		}
		delay = 0
		transport.RememberOverlay(trojan.TunIP, trojan.TunIP)
		e.mu.Lock()
		st := e.stack
		e.trojan = cli
		e.kind = "trojan"
		e.mtu = loftMTU
		e.state = "on"
		e.err = ""
		e.mu.Unlock()
		if st == nil {
			dev, err := inbound.StartStack(pipe, "pkt", cli, trojan.TunIP)
			if err != nil {
				_ = cli.Close()
				signal(err)
				return
			}
			e.mu.Lock()
			e.stack = dev
			e.mu.Unlock()
		} else {
			st.Attach(cli)
		}
		signal(nil)
		select {
		case <-ctx.Done():
			_ = cli.Close()
			return
		case <-path:
			_ = cli.Close()
		case <-cli.Done():
		}
		e.mu.Lock()
		if e.trojan == cli {
			e.trojan = nil
		}
		if e.state == "on" {
			e.state = "connecting"
		}
		e.mu.Unlock()
		if ctx.Err() != nil {
			return
		}
		delay = 200 * time.Millisecond
	}
}

func firstOutbound(cfg *config.ClientConfig, typ string) (config.Outbound, bool) {
	list := cfg.Pick(cfg.Select)
	if len(list) == 0 {
		list = cfg.Outbounds
	}
	for _, o := range list {
		if strings.ToLower(o.Type) == typ {
			return o, true
		}
	}
	return config.Outbound{}, false
}

package mobile

import (
	"context"
	"fmt"
	"io"
	"net"
	"runtime"
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

// engine.go is the session lifecycle: Start picks the outbound from the YAML,
// runs it in a goroutine that reconnects on its own, and the OS adapter talks
// to it only through packets, NotifyPathChange and Snapshot.

const (
	// startWait is how long Start blocks for the first live session before
	// it gives up and reports a timeout to the app.
	startWait = 30 * time.Second
	// loftMTU is the tunnel MTU for every outbound except SSTP (which takes
	// the MTU PPP negotiated). 1280 is the smallest MTU IPv6 allows.
	loftMTU = 1280
)

// Snapshot is gomobile-friendly session state. DNS, FrontIPs, and Routes are comma-separated.
// gomobile cannot export slices, hence the joined strings.
//
// IP/Prefix and Gateway are the tunnel address the node leased (loft Status)
// or PPP gave (SSTP IPCP). FrontIPs are the server addresses the OS must
// route outside the tunnel so the session does not loop into itself. Fronts
// and MaskPub are what the node told the client about itself (other fronts,
// the mask public key); the app saves them back into the connection.
// State is one of idle, connecting, on, error, stopped.
type Snapshot struct {
	Kind        string
	IP          string
	Gateway     string
	IP6         string
	Gateway6    string
	Prefix      int
	Prefix6     int
	MTU         int
	DNS         string
	FrontIPs    string
	Fronts      string
	MaskPub     string
	Routes      string
	BytesUp     int64
	BytesDown   int64
	State       string
	Err         string
	InstanceID  string
	ProfileJSON string
}

// Engine is one overlay/VPN session for a Packet Tunnel / VpnService.
// Exactly one of holder (loft), sstp, vless or trojan is live at a time.
// stack is the userspace TCP/IP stack that sits between the packet pipe and
// a stream-based outbound; it outlives reconnects, so apps keep their
// sockets while the connection underneath is replaced.
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
// (WatchPath would watch the default route, and inside a full tunnel that is
// the tunnel itself, so the OS has to tell us instead.)
//
// The server addresses are resolved here, before any tunnel exists, so the
// lookup still uses the real network. On Android the uplink is pinned first
// so the engine's own sockets bypass the VPN.
func (e *Engine) Start(yamlStr string) error {
	e.mu.Lock()
	if e.state == "connecting" || e.state == "on" {
		e.mu.Unlock()
		return fmt.Errorf("already started")
	}
	e.mu.Unlock()

	// 1. Parse the YAML and check that the first outbound is a kind this
	// engine can run.
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

	// 2. Resolve server addresses while the real network is still the route,
	// then reset the engine state for a fresh session.
	if runtime.GOOS == "android" {
		_ = protect.BindUplink("")
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

	// 3. Run the session in the background and wait for its first result.
	// On a timeout the last dial error (if any) explains what went wrong.
	transport.ClearDialError()
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
		if derr := transport.LastDialError(); derr != nil {
			err = fmt.Errorf("timeout: %v", derr)
		}
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

	// Close everything outside the lock: Close may block, and the session
	// goroutine may need the lock to finish. The stack owns the pipe, so the
	// pipe is closed directly only when there is no stack.
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
	if runtime.GOOS == "android" {
		_ = protect.Bind("")
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
// GET rebinds the HTTP/2 pool (same sid). Other loft fronts Park; resume is
// 0x02 plus the same instance_id. SSTP/VLESS/Trojan redial.
//
// In plain words: the GET front reopens its sockets on the new network and
// keeps its session id (sid). Other fronts drop the connection; the node
// parks (holds) the mux for a while, and the client comes back with a fresh
// handshake (0x02) and the same instance_id, so the node hands back the same
// streams and overlay address. The signal channel has room for one event;
// extra events while one is pending are dropped.
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
// Address, gateway, routes and DNS come from process-wide state in
// transport, filled by the last Status (or by RememberOverlay for the
// non-loft outbounds). Byte counters come from the live client when it
// keeps its own; SSTP counts in the pump instead.
func (e *Engine) Snapshot() *Snapshot {
	e.mu.Lock()
	s := &Snapshot{
		Kind:        e.kind,
		MTU:         e.mtu,
		DNS:         e.dns,
		FrontIPs:    e.fronts,
		State:       e.state,
		Err:         e.err,
		InstanceID:  transport.ClientInstance(),
		BytesUp:     e.bytesUp.Load(),
		BytesDown:   e.bytesDown.Load(),
		ProfileJSON: transport.ListsProfileJSON(),
	}
	h := e.holder
	vc := e.vless
	tc := e.trojan
	kind := e.kind
	e.mu.Unlock()
	// Tunnel settings come from transport's process-wide state.
	s.IP, s.Prefix = splitPrefix(transport.OverlayIP(), 24)
	s.Gateway = transport.OverlayGateway()
	s.IP6, s.Prefix6 = splitPrefix(transport.OverlayIPv6(), 64)
	s.Routes = joinComma(transport.OverlayRoutes())
	s.Fronts = transport.FormatFronts(transport.LearnedFronts())
	s.MaskPub = transport.LearnedMaskPub()
	if dns := transport.OverlayDNS(); len(dns) > 0 {
		s.DNS = joinComma(dns)
	}
	// Prefer the live client's own byte counters when it has them.
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

// SetInstanceFile persists overlay instance_id across process restarts.
// Call before Start. Empty path is memory-only (auth_already_online after kill).
// instance_id is how the node recognises the same install after a
// reconnect. If it is lost when the OS kills the extension, the node still
// sees the old session online and refuses the new one (auth_already_online).
func (e *Engine) SetInstanceFile(path string) {
	transport.SetInstanceFile(path)
}

// SetProtector marks new outbound fds off the tunnel. Call before Start.
// Without it, on Android the engine's own sockets to the server would be
// routed back into the VPN it serves.
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

// setErr moves the engine into the "error" state and keeps the message for
// Snapshot, so the app can show why the session stopped.
func (e *Engine) setErr(err error) {
	e.mu.Lock()
	e.state = "error"
	if err != nil {
		e.err = err.Error()
	}
	e.mu.Unlock()
}

// addBytes adds to the upload and download byte counters shown in Snapshot.
// It uses atomics because packet pumps call it from several goroutines.
func (e *Engine) addBytes(up, down int64) {
	if up != 0 {
		e.bytesUp.Add(up)
	}
	if down != 0 {
		e.bytesDown.Add(down)
	}
}

// run is the session goroutine. It hands off to the loop for the outbound
// kind. Each loop sends exactly one value on ready: nil when the first
// session is up, or the error that stopped it first.
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

// runLoft runs the loft overlay. transport.SuperviseOn owns the connect and
// reconnect loop and calls the callback each time a front comes up.
//
// The Holder is a stable slot for "the current connection"; the callback
// swaps what is inside it, so the userspace stack above never has to be
// rebuilt. For the l3 front (raw IP instead of mux streams) the dialer is
// wrapped in an L3Net, and on reconnect only its packet pump is swapped.
//
// On the first connect the userspace stack is started on the overlay IP
// from Status, and Start is released. Later connects just re-attach the
// stack (for UDP/ICMP and the l3 pump) to the new connection.
//
// WatchStall adds its own path events: when the mux stops answering pings
// while the node is still reachable, the GET front is stuck behind a lost
// response, and a forced path change gets it moving again.
func (e *Engine) runLoft(ctx context.Context, cfg *config.ClientConfig, pipe *packetPipe, path <-chan struct{}, ready chan<- error) {
	h := &transport.Holder{}
	e.mu.Lock()
	e.holder = h
	e.mu.Unlock()
	only := loftOnly(cfg)
	var once sync.Once
	signal := func(err error) { once.Do(func() { ready <- err }) }
	path = transport.MergePath(ctx, path, transport.WatchStall(ctx, h, nil))
	err := transport.SuperviseOn(ctx, only, path, func(d transport.Dialer, o config.Outbound) {
		// Put the new connection into the Holder (or into the live L3Net).
		if live, ok := h.Live().(*inbound.L3Net); ok {
			if p, ok := d.(transport.IPPump); ok {
				live.Swap(d, p)
			} else {
				h.Set(d)
			}
		} else {
			w, err := inbound.WrapL3(d)
			if err != nil {
				signal(err)
				return
			}
			h.Set(w)
		}
		e.mu.Lock()
		st := e.stack
		e.kind = o.Type
		e.mtu = loftMTU
		e.mu.Unlock()
		// First connect: start the stack and release Start. Later
		// connects only re-attach it.
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
	e.endRun(ctx, err)
	signal(err)
}

// runSSTP runs an SSTP session (PPP inside TLS) and redials it forever.
//
// SSTP already carries whole IP packets, so there is no userspace stack:
// pumpSSTP copies packets between the pipe and the session as they are.
// The address, peer and DNS come from PPP (IPCP) and are published for
// Snapshot through RememberOverlay.
//
// tls_check in the YAML, when set, overrides the outbound's insecure flag.
// The password falls back to the token. A failed dial retries after
// 200 ms, doubling up to about 8 s; a path change cancels the live dial or
// session and skips the wait. A session that ends retries after 200 ms.
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
		// 1. Wait out the retry delay; a path change cuts it short.
		if delay > 0 {
			select {
			case <-ctx.Done():
				e.endRun(ctx, ctx.Err())
				signal(ctx.Err())
				return
			case <-path:
				delay = 0
			case <-time.After(delay):
			}
		}
		// 2. Dial in a child context that a path change cancels.
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
		// 3. Up: publish the PPP address and DNS, then pump packets until
		// the session ends.
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
		// 4. Down: forget the session and go round again.
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
			e.endRun(ctx, ctx.Err())
			return
		}
		delay = 200 * time.Millisecond
	}
}

// pumpSSTP copies IP packets both ways between the OS pipe and the SSTP
// session and counts bytes. It returns when either direction fails or ctx
// ends; the caller then closes the session and redials.
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

// runVLESS runs a VLESS client (a proxy protocol over TCP+TLS; the token is
// the user UUID) and reconnects it forever.
//
// VLESS opens one TCP connection per proxied stream, so there is no single
// session to keep alive. Probe checks that the server answers before the
// tunnel is reported up. The tunnel then gets the fixed synthetic address
// vless.TunIP, because the server leases no address.
//
// The userspace stack turns OS packets into VLESS connections. It is
// created once and re-attached to each new client, so apps keep their
// sockets across reconnects. The loop waits until the context ends, a path
// change arrives (close and redial at once), or the client dies.
//
// The first Probe failure is returned to Start so the app sees the real
// error (dest certificate, alpn, timeout) instead of a 30 s "timeout".
// After the tunnel has been up, later Probe failures back off from 200 ms
// doubling to 8 s, same as a dropped session.
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
	up := false
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
		// Build a client and probe the server before reporting the tunnel up.
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
			if !up {
				signal(err)
				return
			}
			if delay == 0 {
				delay = 200 * time.Millisecond
			} else if delay < 8*time.Second {
				delay *= 2
			}
			continue
		}
		// Up: publish the synthetic address and start or re-attach the stack.
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
		up = true
		signal(nil)
		// Wait for the end: stop, path change or a dead client.
		select {
		case <-ctx.Done():
			_ = cli.Close()
			e.endRun(ctx, ctx.Err())
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
			e.endRun(ctx, ctx.Err())
			return
		}
		delay = 200 * time.Millisecond
	}
}

// runTrojan is runVLESS for Trojan: the same loop, probe, backoff and
// synthetic tunnel address (trojan.TunIP); the token is the Trojan
// password instead of a UUID.
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
	up := false
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
		// Build a client and probe the server before reporting the tunnel up.
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
			if !up {
				signal(err)
				return
			}
			if delay == 0 {
				delay = 200 * time.Millisecond
			} else if delay < 8*time.Second {
				delay *= 2
			}
			continue
		}
		// Up: publish the synthetic address and start or re-attach the stack.
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
		up = true
		signal(nil)
		// Wait for the end: stop, path change or a dead client.
		select {
		case <-ctx.Done():
			_ = cli.Close()
			e.endRun(ctx, ctx.Err())
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
			e.endRun(ctx, ctx.Err())
			return
		}
		delay = 200 * time.Millisecond
	}
}

// firstOutbound is the first outbound of type typ, looked up in the
// selected list first and in all outbounds when the selection is empty.
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

// endRun records how the session loop ended: "stopped" when it was
// cancelled (the user or Stop), "error" with the message otherwise. It
// leaves an idle or already stopped engine alone.
func (e *Engine) endRun(ctx context.Context, err error) {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.state == "idle" || e.state == "stopped" {
		return
	}
	if ctx != nil && ctx.Err() != nil {
		e.state = "stopped"
		return
	}
	e.state = "error"
	if err != nil {
		e.err = err.Error()
	}
}

// splitPrefix splits "10.0.0.2/24" into the address and prefix length.
// A bare address gets def.
func splitPrefix(raw string, def int) (string, int) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", def
	}
	if ip, n, err := net.ParseCIDR(s); err == nil {
		ones, _ := n.Mask.Size()
		if ones <= 0 {
			ones = def
		}
		return ip.String(), ones
	}
	return s, def
}

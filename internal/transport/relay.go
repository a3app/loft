package transport

// Relay edge: terminate public TLS (decoy/mask) and pipe TLS-plaintext HTTP
// to the controller hub. Inner mux AEAD stays on the controller.
//
// A relay is an extra public entry point for the same network. It owns a
// public address, a TLS certificate and a decoy site, but no member keys and
// no overlay: it only peels off the outer TLS and passes the HTTP bytes on.
// It reaches the controller as an ordinary loft client whose Hello carries
// CapRelay; that one mux is the trunk. Each member connection becomes one
// hub:1 (HTTP/1.1) or hub:2 (HTTP/2) stream on the trunk, so the
// controller's member keys, leases and frame encryption never leave the
// controller.

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"io"
	"net"
	"path/filepath"
	"strings"
	"time"

	"github.com/a3app/loft/internal/config"
	"github.com/a3app/loft/internal/identity"
	"github.com/a3app/loft/internal/mux"
	"github.com/a3app/loft/internal/wire"
)

// RunRelay terminates public TLS (decoy/mask) and pipes plaintext to the
// controller over a loft trunk (Open hub:1 / hub:2). Auth, lease and roster
// stay on the controller.
func RunRelay(ctx context.Context, cfg *config.ServerConfig) error {
	if cfg.Controller == "" {
		return net.ErrClosed
	}
	// The relay enters the controller with the key issued for its id, and
	// trusts only slices signed by the controller key it was given.
	token := cfg.RelayToken
	if token == "" {
		return errors.New("relay: no relay key (relay.key next to the node files)")
	}
	ctlPub, err := ParseControllerPub(cfg.ControllerPub)
	if err != nil {
		return fmt.Errorf("relay: controller_pub: %w", err)
	}
	pin := &relayPin{base: ctlPub, cur: ctlPub}
	if cfg.ControllerKey != "" {
		pin.path = filepath.Join(filepath.Dir(cfg.ControllerKey), "controller.pub")
		pin.load()
		pin.takePath = filepath.Join(filepath.Dir(cfg.ControllerKey), "controller.epoch")
		pin.loadTake()
	}
	// The trunk tries the controller, then any standby controllers.
	var obs []config.Outbound
	for k, u := range cfg.ControllerURLs() {
		obs = append(obs, trunkOutbound(fmt.Sprintf("hub%d", k), u))
	}
	// The Holder keeps the current trunk; Supervise replaces it on every
	// reconnect, so pipeHub always opens hub streams on a live mux.
	h := &Holder{}
	rt := &relayRuntime{started: time.Now()}
	trunk := &config.ClientConfig{
		Token:     token,
		Select:    "auto",
		Outbounds: obs,
		// ExtraCaps is the only way a Hello gets CapRelay after MemberCaps.
		// Without this bit the controller will not Open hub:* / ctl:1.
		ExtraCaps: uint64(wire.CapRelay),
	}
	go func() {
		_ = Supervise(ctx, trunk, func(d Dialer, _ config.Outbound) { h.Set(d) })
	}()
	if len(obs) > 1 {
		go fenceLoop(ctx, trunk, pin, fenceEvery)
	}
	standby := ""
	if cfg.IsReplica() && cfg.ControllerKey != "" {
		standby = filepath.Join(filepath.Dir(cfg.ControllerKey), "standby")
	}
	go runRelayCtl(ctx, h, cfg, rt, pin, standby)
	// Every relay is a witness for the standbys in its controller list.
	if len(cfg.ControllerAlt) > 0 {
		witnessLoop(ctx, rt, cfg.ID, []byte(token), cfg.ControllerAlt)
	}
	var wit *witnessSink
	if standby != "" {
		if RelayKeys == nil {
			RelayKeys = identity.NewRing()
		}
		wit = &witnessSink{book: &witnessBook{}, standby: standby}
		go runStandby(ctx, cfg, rt, wit)
	}
	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return err
	}
	var mask *Mask
	if cfg.Mask && len(cfg.Decoy) > 0 {
		mask = NewMask([]byte(cfg.Token), cfg.Decoy[0])
	}
	// Non-loft ClientHellos go to the decoy site. A loft one (recognised by
	// the sid mark) on a mask relay finishes the mask TLS handshake inside
	// the decoy listener and comes here; without mask it is returned by
	// Accept and terminated with the relay's own certificate below.
	pipe := func(c net.Conn, hello clientHello) {
		if rt.drain.Load() {
			c.Close()
			return
		}
		relayConn(h, rt, c, wit)
	}
	ln = wrapRelayDecoy(ln, []byte(cfg.Token), cfg.Decoy, mask, pipe, PublicSNIs(cfg.Advertise, cfg.CertFile))
	ApplyDecoyProxy(ln, cfg.DecoyProxy)
	ApplyCDNPeers(ln, cfg.CDNPeers)
	if d, ok := ln.(*decoyListener); ok {
		rt.decoy = d
	}
	if len(cfg.Decoy) > 0 {
		go func() { _ = SpliceUDP(cfg.Listen, cfg.Decoy[0]) }()
	}
	tlsCfg := &tls.Config{MinVersion: tls.VersionTLS13, NextProtos: []string{"h2", "http/1.1"}}
	if cfg.CertFile != "" && cfg.KeyFile != "" {
		cert, err := tls.LoadX509KeyPair(cfg.CertFile, cfg.KeyFile)
		if err != nil {
			return err
		}
		tlsCfg.Certificates = []tls.Certificate{cert}
	}
	tl := tls.NewListener(ln, tlsCfg)
	for {
		c, err := tl.Accept()
		if err != nil {
			return err
		}
		if rt.drain.Load() {
			c.Close()
			continue
		}
		go relayConn(h, rt, c, wit)
	}
}

// relayConn handles one loft connection after TLS: on a standby a witness
// report is taken here, anything else goes to the controller.
func relayConn(h *Holder, rt *relayRuntime, c net.Conn, wit *witnessSink) {
	if wit == nil || alpnOf(c) == "h2" {
		pipeHub(h, rt, c)
		return
	}
	br := bufio.NewReader(c)
	_ = c.SetReadDeadline(time.Now().Add(15 * time.Second))
	head, _ := br.Peek(len(witnessPrefix))
	_ = c.SetReadDeadline(time.Time{})
	if bytes.Equal(head, witnessPrefix) {
		wit.serve(c, br)
		return
	}
	pipeHub(h, rt, &peekedConn{Conn: c, r: br})
}

// alpnOf is the ALPN protocol TLS agreed on c, or "".
func alpnOf(c net.Conn) string {
	if cs, ok := c.(interface{ ConnectionState() tls.ConnectionState }); ok {
		return cs.ConnectionState().NegotiatedProtocol
	}
	return ""
}

// peekedConn reads first what was peeked from its Conn.
type peekedConn struct {
	net.Conn
	r *bufio.Reader
}

func (p *peekedConn) Read(b []byte) (int, error) { return p.r.Read(b) }

// ConnectionState passes through the TLS state, so pipeHub still sees ALPN.
func (p *peekedConn) ConnectionState() tls.ConnectionState {
	if cs, ok := p.Conn.(interface{ ConnectionState() tls.ConnectionState }); ok {
		return cs.ConnectionState()
	}
	return tls.ConnectionState{}
}

// wrapRelayDecoy splices non-loft ClientHello to origins and hands loft TCP to onLoft.
func wrapRelayDecoy(ln net.Listener, token []byte, origins []string, mask *Mask, onLoft func(net.Conn, clientHello), own []string) net.Listener {
	d, ok := WrapDecoyMask(ln, token, origins, mask, nil, own...).(*decoyListener)
	if !ok {
		return ln
	}
	d.onLoft = onLoft
	return d
}

// pipeHub Opens hub:1 or hub:2 on the trunk (ALPN h2 → hub:2) and copies c.
// It waits up to 15 s for a trunk, so a member that connects while the relay
// is still reaching the controller is not dropped at once.
func pipeHub(h *Holder, rt *relayRuntime, c net.Conn) {
	defer c.Close()
	if rt != nil {
		rt.conns.Add(1)
		defer rt.conns.Add(-1)
	}
	port := wire.HubHTTPPort
	if alpnOf(c) == "h2" {
		port = wire.HubH2Port
	}
	sess := waitSession(h, 15*time.Second)
	if sess == nil {
		return
	}
	st, err := sess.Open(net.JoinHostPort(wire.HubHost, port))
	if err != nil {
		return
	}
	defer st.Close()
	go func() { _, _ = io.Copy(st, c); st.Close() }()
	_, _ = io.Copy(c, st)
}

// waitSession polls Holder.Session until it is non-nil or d elapses.
func waitSession(h *Holder, d time.Duration) *mux.Session {
	deadline := time.Now().Add(d)
	for time.Now().Before(deadline) {
		if s := h.Session(); s != nil {
			return s
		}
		time.Sleep(50 * time.Millisecond)
	}
	return h.Session()
}

// trunkOutbound is how a relay dials one controller URL: http(s) → the GET
// front, wss → WebSocket (the default, with certificate checks off for a
// bare host).
// fenceEvery is how often a relay shows its Takeover to every controller
// address; a var so tests can shorten it.
var fenceEvery = 2 * time.Minute

// fenceLoop shows the newest Takeover the relay knows to every controller
// address in turn, over a short trunk of its own. The relay's main trunk
// stays on one controller, so without this an old controller that comes
// back would not hear it was replaced until that trunk dropped. A current
// controller ignores a Takeover it already has.
func fenceLoop(ctx context.Context, trunk *config.ClientConfig, pin *relayPin, every time.Duration) {
	t := time.NewTicker(every)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		take, _ := pin.takeover()
		if len(take) == 0 {
			continue
		}
		for _, o := range trunk.Outbounds {
			showTakeover(ctx, trunk, o, take)
		}
	}
}

// showTakeover dials one controller address and sends take on ctl:1.
func showTakeover(ctx context.Context, trunk *config.ClientConfig, o config.Outbound, take []byte) {
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	d, err := startWithOffer(cctx, trunk.Token, o, helloCaps(trunk)&^wire.CapP2P)
	if err != nil {
		return
	}
	defer d.Close()
	st, err := d.Session().Open(net.JoinHostPort(wire.CtlHost, wire.CtlPort))
	if err != nil {
		return
	}
	_ = writeMsg(st, wire.EncodeFlt(wire.FltTakeover, take))
	// The controller reads it before closing; a short wait lets it arrive.
	_ = st.SetReadDeadline(time.Now().Add(2 * time.Second))
	_, _ = readMsg(st)
	st.Close()
}

func trunkOutbound(name, u string) config.Outbound {
	o := config.Outbound{Name: name, Type: "ws", URL: u, Fingerprint: "chrome", Insecure: true}
	switch {
	case strings.HasPrefix(u, "https:"):
		o.Type, o.Insecure = "get", false
	case strings.HasPrefix(u, "http:"):
		o.Type = "get"
	case strings.HasPrefix(u, "wss:"):
		o.Type, o.Insecure = "ws", false
	}
	return o
}

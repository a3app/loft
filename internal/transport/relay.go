package transport

// Relay edge: terminate public TLS (decoy/mask) and pipe plaintext to the controller hub.

import (
	"context"
	"crypto/tls"
	"io"
	"net"
	"time"

	"github.com/a3app/loft/internal/config"
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
	token := cfg.RelayToken
	if token == "" {
		token = cfg.Token
	}
	o := config.Outbound{
		Name:        "hub",
		Type:        "ws",
		URL:         cfg.Controller,
		Fingerprint: "chrome",
		Insecure:    true,
	}
	if len(cfg.Controller) >= 8 && (cfg.Controller[:7] == "http://" || (len(cfg.Controller) >= 6 && cfg.Controller[:4] == "http" && cfg.Controller[4] != 's')) {
		o.Type = "get"
	}
	if len(cfg.Controller) >= 6 && cfg.Controller[:6] == "https:" {
		o.Type = "get"
		o.Insecure = false
	}
	if len(cfg.Controller) >= 4 && cfg.Controller[:4] == "wss:" {
		o.Type = "ws"
		o.Insecure = false
	}
	h := &Holder{}
	rt := &relayRuntime{started: time.Now()}
	go func() {
		_ = Supervise(ctx, &config.ClientConfig{
			Token:     token,
			Select:    "auto",
			Outbounds: []config.Outbound{o},
			ExtraCaps: uint64(wire.CapRelay),
		}, func(d Dialer, _ config.Outbound) { h.Set(d) })
	}()
	go runRelayCtl(ctx, h, cfg, rt)
	ln, err := net.Listen("tcp", cfg.Listen)
	if err != nil {
		return err
	}
	var mask *Mask
	if cfg.Mask && len(cfg.Decoy) > 0 {
		mask = NewMask([]byte(cfg.Token), cfg.Decoy[0])
	}
	pipe := func(c net.Conn, hello clientHello) {
		if rt.drain.Load() {
			c.Close()
			return
		}
		pipeHub(h, rt, c)
	}
	ln = wrapRelayDecoy(ln, []byte(cfg.Token), cfg.Decoy, mask, pipe, PublicSNIs(cfg.Advertise, cfg.CertFile))
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
		go pipeHub(h, rt, c)
	}
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
func pipeHub(h *Holder, rt *relayRuntime, c net.Conn) {
	defer c.Close()
	if rt != nil {
		rt.conns.Add(1)
		defer rt.conns.Add(-1)
	}
	port := wire.HubHTTPPort
	if cs, ok := c.(interface{ ConnectionState() tls.ConnectionState }); ok {
		if cs.ConnectionState().NegotiatedProtocol == "h2" {
			port = wire.HubH2Port
		}
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

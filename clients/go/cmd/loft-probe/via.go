// via.go is the loft-probe transport: own mux session or a live SOCKS5.
package main

import (
	"context"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/a3app/loft/internal/cdn"
	"github.com/a3app/loft/internal/config"
	"github.com/a3app/loft/internal/mux"
	"github.com/a3app/loft/internal/transport"
)

// datagram is one overlay or SOCKS UDP payload with its host:port.
type datagram struct {
	addr string
	data []byte
}

// via is how probes send TCP and datagrams: mux session or SOCKS5.
type via interface {
	kind() string
	detail() string
	canICMP() bool
	overlayIP() string
	dialTCP(ctx context.Context, hostport string) (net.Conn, error)
	writeDgram(addr string, data []byte) error
	waitDgram(ctx context.Context, match func(datagram) bool) (datagram, error)
	close() error
}

// muxVia is a loft front session. ICMP uses CmdDgram.
type muxVia struct {
	d     transport.Dialer
	front string
	ip    string
	ch    chan datagram
	once  sync.Once
}

// openMux starts a GET/WS front and steals inbound datagrams into v.ch.
func openMux(ctx context.Context, token, rawURL, kind, path, cdnName string) (*muxVia, error) {
	d, front, err := dialFront(ctx, token, rawURL, kind, path, cdnName)
	if err != nil {
		return nil, err
	}
	v := &muxVia{d: d, front: front, ip: transport.OverlayIP(), ch: make(chan datagram, 64)}
	if s := d.Session(); s != nil {
		s.FilterDgram(func(addr string, data []byte) bool {
			select {
			case v.ch <- datagram{addr: addr, data: append([]byte(nil), data...)}:
			default:
			}
			return true
		})
	}
	return v, nil
}

// kind reports "mux".
func (v *muxVia) kind() string { return "mux" }

// canICMP is true: mux CmdDgram carries IPv4 ICMP.
func (v *muxVia) canICMP() bool { return true }

// overlayIP is the address assigned after the front handshake.
func (v *muxVia) overlayIP() string {
	if v.ip != "" {
		return v.ip
	}
	return transport.OverlayIP()
}

// detail is "front=… overlay=… dns=…" for the probe banner.
func (v *muxVia) detail() string {
	ip := v.overlayIP()
	dns := strings.Join(transport.OverlayDNS(), ",")
	s := fmt.Sprintf("front=%s overlay=%s", v.front, ip)
	if dns != "" {
		s += " dns=" + dns
	}
	return s
}

// dialTCP opens a mux stream to hostport.
func (v *muxVia) dialTCP(ctx context.Context, hostport string) (net.Conn, error) {
	c, err := v.d.Open(hostport)
	if err != nil {
		return nil, err
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = c.SetDeadline(dl)
	}
	return c, nil
}

// writeDgram sends a mux datagram to addr.
func (v *muxVia) writeDgram(addr string, data []byte) error {
	s := v.d.Session()
	if s == nil {
		return fmt.Errorf("no session")
	}
	return s.WriteDgram(addr, data)
}

// waitDgram returns the next stolen datagram matching match, or ctx/session error.
func (v *muxVia) waitDgram(ctx context.Context, match func(datagram) bool) (datagram, error) {
	for {
		select {
		case <-ctx.Done():
			return datagram{}, ctx.Err()
		case <-v.d.Done():
			return datagram{}, mux.ErrClosed
		case dg := <-v.ch:
			if match(dg) {
				return dg, nil
			}
		}
	}
}

// close shuts the front session once.
func (v *muxVia) close() error {
	v.once.Do(func() {
		_ = v.d.Close()
	})
	return nil
}

// socksVia talks to a live loft SOCKS5. ICMP is not available.
type socksVia struct {
	addr  string
	mu    sync.Mutex
	ctrl  net.Conn
	pc    net.PacketConn
	relay *net.UDPAddr
}

// openSocks dials addr once to check the proxy is up.
func openSocks(addr string) (*socksVia, error) {
	c, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		return nil, fmt.Errorf("socks %s: %w", addr, err)
	}
	_ = c.Close()
	return &socksVia{addr: addr}, nil
}

// kind reports "socks".
func (v *socksVia) kind() string { return "socks" }

// canICMP is false: SOCKS5 has no ICMP.
func (v *socksVia) canICMP() bool { return false }

// overlayIP is empty; SOCKS does not report the overlay address.
func (v *socksVia) overlayIP() string { return "" }

// detail is "socks=host:port" for the probe banner.
func (v *socksVia) detail() string { return "socks=" + v.addr }

// dialTCP CONNECTs through SOCKS5 to hostport.
func (v *socksVia) dialTCP(ctx context.Context, hostport string) (net.Conn, error) {
	var d net.Dialer
	c, err := d.DialContext(ctx, "tcp", v.addr)
	if err != nil {
		return nil, err
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = c.SetDeadline(dl)
	}
	if err := socksConnect(c, hostport); err != nil {
		_ = c.Close()
		return nil, err
	}
	return c, nil
}

// ensureUDP holds a UDP ASSOCIATE control conn and local UDP socket.
func (v *socksVia) ensureUDP() error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.pc != nil {
		return nil
	}
	ctrl, err := net.DialTimeout("tcp", v.addr, 5*time.Second)
	if err != nil {
		return err
	}
	relay, err := socksUDPAssociate(ctrl)
	if err != nil {
		_ = ctrl.Close()
		return err
	}
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		_ = ctrl.Close()
		return err
	}
	v.ctrl, v.pc, v.relay = ctrl, pc, relay
	return nil
}

// writeDgram sends a SOCKS5 UDP datagram to the relay.
func (v *socksVia) writeDgram(addr string, data []byte) error {
	if err := v.ensureUDP(); err != nil {
		return err
	}
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	p, _ := strconv.Atoi(portStr)
	pkt, err := encodeSocksUDP(host, p, data)
	if err != nil {
		return err
	}
	_, err = v.pc.WriteTo(pkt, v.relay)
	return err
}

// waitDgram reads SOCKS UDP until match or ctx deadline.
func (v *socksVia) waitDgram(ctx context.Context, match func(datagram) bool) (datagram, error) {
	if err := v.ensureUDP(); err != nil {
		return datagram{}, err
	}
	buf := make([]byte, 65535)
	for {
		if dl, ok := ctx.Deadline(); ok {
			_ = v.pc.SetReadDeadline(dl)
		}
		n, _, err := v.pc.ReadFrom(buf)
		if err != nil {
			if ctx.Err() != nil {
				return datagram{}, ctx.Err()
			}
			return datagram{}, err
		}
		host, port, data, err := parseSocksUDP(buf[:n])
		if err != nil {
			continue
		}
		dg := datagram{addr: net.JoinHostPort(host, strconv.Itoa(port)), data: append([]byte(nil), data...)}
		if match(dg) {
			return dg, nil
		}
	}
}

// close drops the UDP associate sockets.
func (v *socksVia) close() error {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.pc != nil {
		_ = v.pc.Close()
	}
	if v.ctrl != nil {
		_ = v.ctrl.Close()
	}
	return nil
}

// dialFront tries the requested front. Stock public path is GET.
func dialFront(ctx context.Context, token, raw, kind, path, cdnName string) (transport.Dialer, string, error) {
	kind = strings.ToLower(strings.TrimSpace(kind))
	var order []string
	switch kind {
	case "ws":
		order = []string{"ws", "get"}
	case "get":
		order = []string{"get"}
	case "h2", "quic", "h3":
		order = []string{kind}
	default:
		if strings.HasPrefix(raw, "ws://") || strings.HasPrefix(raw, "wss://") {
			order = []string{"ws", "get"}
		} else {
			order = []string{"get"}
		}
	}
	var last error
	for _, t := range order {
		o := outboundFor(raw, t, path, cdnName)
		d, err := transport.Start(ctx, token, o)
		if err == nil {
			return d, t, nil
		}
		last = err
		fmt.Fprintf(os.Stderr, "front %s: %v\n", t, err)
	}
	if last == nil {
		last = fmt.Errorf("no front")
	}
	return nil, "", last
}

// outboundFor builds an Outbound for a GET or WS front URL.
func outboundFor(raw, kind, path, cdnName string) config.Outbound {
	o := config.Outbound{
		Name:         "loft",
		Type:         kind,
		URL:          raw,
		Path:         path,
		CDN:          strings.TrimSpace(cdnName),
		Fingerprint:  "chrome",
		Insecure:     true,
		HeaderBudget: 16 << 10,
		Inflight:     48,
	}
	if p, err := cdn.Lookup(o.CDN); err == nil && o.CDN != "" {
		o.HeaderBudget = p.HeaderBudget
		if p.Inflight > 0 {
			o.Inflight = p.Inflight
		}
		if strings.EqualFold(p.Name, "yandex") {
			o.Insecure = false
		}
	}
	switch kind {
	case "get":
		o.URL = httpOrigin(raw)
		if o.Path == "" || o.Path == "/static/js" {
			o.Path = "/static/v3/build"
		}
	case "ws":
		o.URL = withWSPath(wsURL(raw), "/api/v1/sync")
	case "h2", "h3":
		o.URL = httpOrigin(raw)
		if o.Path == "" || o.Path == "/static/js" {
			o.Path = "/api/v1/tunnel"
		}
	case "quic":
		o.URL = httpOrigin(raw)
	}
	return o
}

// withWSPath puts fallback on a URL that has no path (cover is /).
func withWSPath(raw, fallback string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	if u.Path == "" || u.Path == "/" {
		u.Path = fallback
	}
	return u.String()
}

// httpOrigin maps a ws(s) URL to the matching http(s) origin.
func httpOrigin(raw string) string {
	s := strings.TrimSpace(raw)
	switch {
	case strings.HasPrefix(s, "wss://"):
		s = "https://" + strings.TrimPrefix(s, "wss://")
	case strings.HasPrefix(s, "ws://"):
		s = "http://" + strings.TrimPrefix(s, "ws://")
	}
	u, err := url.Parse(s)
	if err != nil {
		return s
	}
	host := u.Host
	if host == "" {
		host = u.Path
	}
	scheme := u.Scheme
	if scheme == "" {
		scheme = "https"
	}
	return scheme + "://" + host
}

// wsURL maps an http(s) URL to the matching ws(s) URL.
func wsURL(raw string) string {
	s := strings.TrimSpace(raw)
	if strings.HasPrefix(s, "https://") {
		return "wss://" + strings.TrimPrefix(s, "https://")
	}
	if strings.HasPrefix(s, "http://") {
		return "ws://" + strings.TrimPrefix(s, "http://")
	}
	if !strings.HasPrefix(s, "ws://") && !strings.HasPrefix(s, "wss://") {
		return "wss://" + s
	}
	return s
}

// loadProfile reads a client YAML profile.
func loadProfile(path string) (*config.ClientConfig, error) {
	return config.LoadClient(path)
}

// pickOutbound uses cfg.Select, else the first outbound.
func pickOutbound(cfg *config.ClientConfig) config.Outbound {
	if cfg.Select != "" && cfg.Select != "auto" {
		for _, o := range cfg.Outbounds {
			if o.Name == cfg.Select || o.Type == cfg.Select {
				return o
			}
		}
	}
	return cfg.Outbounds[0]
}

// findProfile prefers -c, then LOFT_PROFILE, then the newest yaml in the profile dirs.
func findProfile(explicit string) (string, error) {
	if explicit != "" {
		return explicit, nil
	}
	if e := os.Getenv("LOFT_PROFILE"); e != "" {
		return e, nil
	}
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	dirs := []string{
		filepath.Join(home, "Library", "Application Support", "loft", "profiles"),
		filepath.Join(home, ".config", "loft"),
	}
	var found []string
	for _, dir := range dirs {
		ents, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range ents {
			if e.IsDir() {
				continue
			}
			n := e.Name()
			if strings.HasSuffix(n, ".yaml") || strings.HasSuffix(n, ".yml") {
				found = append(found, filepath.Join(dir, n))
			}
		}
	}
	if len(found) == 0 {
		return "", fmt.Errorf("no profile (pass -c or -token/-url)")
	}
	best, bestT := found[0], time.Time{}
	for _, p := range found {
		st, err := os.Stat(p)
		if err != nil {
			continue
		}
		if st.ModTime().After(bestT) {
			best, bestT = p, st.ModTime()
		}
	}
	return best, nil
}

// socksListening is true if a TCP connect to addr succeeds.
func socksListening(addr string) bool {
	c, err := net.DialTimeout("tcp", addr, 400*time.Millisecond)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

// liveSOCKS returns preferred if it is up, else the other of GUI 1080 / debug 18080.
func liveSOCKS(preferred string) string {
	if preferred != "" && socksListening(preferred) {
		return preferred
	}
	for _, addr := range []string{config.DefaultSOCKS5, config.DebugSOCKS5} {
		if addr != preferred && socksListening(addr) {
			return addr
		}
	}
	return ""
}

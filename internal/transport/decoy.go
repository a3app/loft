package transport

// Decoy splice forwards non-loft ClientHellos to a configured HTTPS origin.
//
// Why: a censor finds proxies by connecting to suspicious IPs and looking at
// what answers (active probing). With decoy on, the node reads the first TLS
// message of each TCP connection before answering. If it carries the loft
// mark (see hellopeek.go), the node serves loft. Otherwise the node splices:
// it opens a TCP connection to the decoy, a real website named in the
// server config, replays the bytes it read, and pipes both directions. The
// prober then completes TLS with the real site and sees its real
// certificate and pages. The node never decrypts spliced traffic.
//
// Terms used below: "own SNI" is one of the node's public hostnames (its
// advertise URL and certificate names); a "decoy SNI" is the decoy's name;
// "dest" and "origin" both mean the decoy website.

import (
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/http2"
)

// prefixConn replays peeked ClientHello bytes before reading the conn.
type prefixConn struct {
	net.Conn
	prefix []byte
}

// Read drains prefix then the inner conn.
func (p *prefixConn) Read(b []byte) (int, error) {
	if len(p.prefix) > 0 {
		n := copy(b, p.prefix)
		p.prefix = p.prefix[n:]
		return n, nil
	}
	return p.Conn.Read(b)
}

// decoyListener implements decoy splice. It wraps the node's public TCP
// listener; Accept hands on only loft connections (masked ones are served
// in their own goroutine). A valid session_id is loft
// (own SNI → node certificate; decoy SNI → mask when ready). Own SNI
// from a configured CDN peer may complete TLS without sid. Anything
// else — including own SNI without sid, a broken hello, and non-TLS —
// is forwarded to a configured origin so a probe sees that origin.
//
// The splice destination is server config (`decoy:`), never a client field.
// First entry is this node's identity (one node may be a government portal,
// another a bank). A matching ClientHello SNI selects among the node's list;
// anything else splices to the primary. Hosts are not protocol constants.
type decoyListener struct {
	ln      net.Listener
	token   []byte
	primary string
	origins map[string]string // sni -> host:port
	own     []string          // advertise / cert names: do not splice
	mask    *Mask
	handler http.Handler                // serves masked conns when onLoft is nil
	onLoft  func(net.Conn, clientHello) // takes masked conns; wins over handler
	cdn     []net.IPNet                 // CDN origin-pull ranges (cdn_peers)
	proxy   bool                        // PROXY v1 header before the spliced bytes
	mu      sync.Mutex
}

// WrapDecoy wraps ln. Empty origins: decoy is off, Accept is plain.
func WrapDecoy(ln net.Listener, token []byte, origins []string) net.Listener {
	return WrapDecoyMask(ln, token, origins, nil, nil)
}

// WrapDecoyMask wraps ln with decoy splice and optional borrowed-cert mask.
// own SNI without a loft session_id is spliced (active probe / CT hostname).
// CDN origin pull must come from cdn_peers, or carry a valid session_id.
func WrapDecoyMask(ln net.Listener, token []byte, origins []string, mask *Mask, h http.Handler, own ...string) net.Listener {
	if len(origins) == 0 && mask == nil {
		return ln
	}
	m := map[string]string{}
	var primary string
	for i, d := range origins {
		host, addr := splitDecoy(d)
		m[strings.ToLower(host)] = addr
		if i == 0 {
			primary = addr
		}
	}
	return &decoyListener{ln: ln, token: token, primary: primary, origins: m, own: ownSNIList(own), mask: mask, handler: h}
}

// PublicSNIs is advertise hostname plus DNS names on the node certificate.
// CDN origin Host/SNI must be one of these so the node does not splice the edge.
func PublicSNIs(advertise, certFile string) []string {
	var out []string
	if u, err := url.Parse(strings.TrimSpace(advertise)); err == nil {
		if h := normSNI(u.Hostname()); h != "" {
			out = append(out, h)
		}
	}
	body, err := os.ReadFile(certFile)
	if err != nil || certFile == "" {
		return out
	}
	for rest := body; len(rest) > 0; {
		var block *pem.Block
		block, rest = pem.Decode(rest)
		if block == nil {
			break
		}
		if block.Type != "CERTIFICATE" {
			continue
		}
		cert, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			continue
		}
		if cn := normSNI(cert.Subject.CommonName); cn != "" {
			out = append(out, cn)
		}
		for _, n := range cert.DNSNames {
			if n = normSNI(n); n != "" {
				out = append(out, n)
			}
		}
		break
	}
	return ownSNIList(out)
}

// normSNI lower-cases a hostname and drops spaces and a trailing dot.
func normSNI(s string) string {
	return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(s), "."))
}

// ownSNIList normalizes names and removes blanks and duplicates.
func ownSNIList(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, n := range in {
		n = normSNI(n)
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	return out
}

// sniIsOwn reports whether sni is this node's public name (exact or one-label wildcard).
func sniIsOwn(sni string, names []string) bool {
	sni = normSNI(sni)
	if sni == "" {
		return false
	}
	for _, n := range names {
		if n == sni {
			return true
		}
		if strings.HasPrefix(n, "*.") {
			suf := n[1:]
			if strings.HasSuffix(sni, suf) && !strings.Contains(strings.TrimSuffix(sni, suf), ".") {
				return true
			}
		}
	}
	return false
}

// splitDecoy returns hostname and host:port, defaulting port 443.
func splitDecoy(d string) (host, addr string) {
	host, _, err := net.SplitHostPort(d)
	if err != nil {
		return d, d + ":443"
	}
	return host, d
}

// decoyHost strips a port from d.
func decoyHost(d string) string {
	host, _, err := net.SplitHostPort(d)
	if err != nil {
		return d
	}
	return host
}

// Addr returns the inner listener address.
func (d *decoyListener) Addr() net.Addr { return d.ln.Addr() }

// Close closes the inner listener.
func (d *decoyListener) Close() error { return d.ln.Close() }

// Accept peeks ClientHello:
//  1. Valid loft session_id + own SNI → node certificate (CDN origin pull
//     after the edge learned sid, and a member that dials advertise).
//  2. Own SNI from a configured CDN peer, even without sid → node certificate.
//  3. Valid session_id on a decoy SNI → loft; mask when ready.
//  4. Anything else, including a broken or non-TLS first flight → splice to dest.
//
// Own SNI without a mark is spliced too: the node's hostname is public (for
// example in Certificate Transparency logs), so a prober may ask for it by
// name. A CDN cannot add the loft mark to its own ClientHello, which is why
// configured CDN peer addresses are let through on own SNI.
// A marked hello whose client_random was already used is a replay and is
// spliced (see hello_once.go).
func (d *decoyListener) Accept() (net.Conn, error) {
	for {
		c, err := d.ln.Accept()
		if err != nil {
			return nil, err
		}
		_ = c.SetReadDeadline(time.Now().Add(8 * time.Second))
		raw, hello, err := readClientHello(c)
		_ = c.SetReadDeadline(time.Time{})
		if err != nil {
			go spliceDecoy(c, raw, d.pick(hello.SNI), d.proxyOn())
			continue
		}
		d.mu.Lock()
		own := d.own
		maskOn := d.mask != nil
		d.mu.Unlock()
		// Mask nodes admit a decoy SNI only as sid v3. sid v2 is the member
		// key, so a stolen key would otherwise mark those ClientHellos.
		// Own SNI still accepts v2: that path presents the node certificate.
		loft := verifySID3(d.token, hello)
		if !loft && (!maskOn || sniIsOwn(hello.SNI, own)) {
			loft = verifyMemberV2(d.token, hello)
		}
		if loft && !helloOnce.take(hello.Random) {
			loft = false
		}
		if sniIsOwn(hello.SNI, own) && (loft || d.isCDNPeer(c.RemoteAddr())) {
			return &prefixConn{Conn: c, prefix: raw}, nil
		}
		if loft {
			if d.mask != nil && d.mask.Ready() {
				if d.onLoft != nil {
					go d.maskThen(c, raw, hello, d.onLoft)
					continue
				}
				if d.handler != nil {
					go d.serveMask(c, raw, hello)
					continue
				}
			}
			return &prefixConn{Conn: c, prefix: raw}, nil
		}
		go spliceDecoy(c, raw, d.pick(hello.SNI), d.proxyOn())
	}
}

// ApplyDecoyProxy turns the PROXY v1 header on TCP splice on or off. The
// origin must expect it (nginx `listen … proxy_protocol`); a public site
// would see garbage, so this is for the node's own web server only.
func ApplyDecoyProxy(ln net.Listener, on bool) {
	if d, ok := ln.(*decoyListener); ok {
		d.mu.Lock()
		d.proxy = on
		d.mu.Unlock()
	}
}

func (d *decoyListener) proxyOn() bool {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.proxy
}

// proxyHeader is a PROXY protocol v1 line for c: client address first, then
// the node address it reached. Non-TCP or mixed families are UNKNOWN.
func proxyHeader(c net.Conn) []byte {
	src, ok1 := c.RemoteAddr().(*net.TCPAddr)
	dst, ok2 := c.LocalAddr().(*net.TCPAddr)
	if !ok1 || !ok2 {
		return []byte("PROXY UNKNOWN\r\n")
	}
	s4, d4 := src.IP.To4(), dst.IP.To4()
	switch {
	case s4 != nil && d4 != nil:
		return []byte(fmt.Sprintf("PROXY TCP4 %s %s %d %d\r\n", s4, d4, src.Port, dst.Port))
	case s4 == nil && d4 == nil:
		return []byte(fmt.Sprintf("PROXY TCP6 %s %s %d %d\r\n", src.IP, dst.IP, src.Port, dst.Port))
	}
	return []byte("PROXY UNKNOWN\r\n")
}

// ApplyCDNPeers sets origin-pull CIDRs on a decoy listener. No-op otherwise.
func ApplyCDNPeers(ln net.Listener, cidrs []string) {
	if d, ok := ln.(*decoyListener); ok {
		d.SetCDNPeers(cidrs)
	}
}

// SetCDNPeers replaces the CIDR list that may use own SNI without session_id.
func (d *decoyListener) SetCDNPeers(cidrs []string) {
	var nets []net.IPNet
	for _, s := range cidrs {
		s = strings.TrimSpace(s)
		if s == "" {
			continue
		}
		if _, n, err := net.ParseCIDR(s); err == nil {
			nets = append(nets, *n)
			continue
		}
		if ip := net.ParseIP(s); ip != nil {
			bits := 32
			if ip.To4() == nil {
				bits = 128
			}
			nets = append(nets, net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
		}
	}
	d.mu.Lock()
	d.cdn = nets
	d.mu.Unlock()
}

func (d *decoyListener) isCDNPeer(addr net.Addr) bool {
	ip := addrIP(addr)
	if ip == nil {
		return false
	}
	d.mu.Lock()
	defer d.mu.Unlock()
	for i := range d.cdn {
		if d.cdn[i].Contains(ip) {
			return true
		}
	}
	return false
}

// SetOrigins replaces the splice destination list. First entry is primary.
func (d *decoyListener) SetOrigins(origins []string) {
	m := map[string]string{}
	var primary string
	for i, o := range origins {
		host, addr := splitDecoy(o)
		m[strings.ToLower(host)] = addr
		if i == 0 {
			primary = addr
		}
	}
	d.mu.Lock()
	d.primary = primary
	d.origins = m
	d.mu.Unlock()
}

// pick selects the origin for sni, or primary if unmatched.
func (d *decoyListener) pick(sni string) string {
	sni = strings.ToLower(sni)
	d.mu.Lock()
	defer d.mu.Unlock()
	if a, ok := d.origins[sni]; ok {
		return a
	}
	return d.primary
}

// maskThen completes mask TLS then calls fn with the masked conn.
// It runs in its own goroutine so a slow handshake does not block Accept.
func (d *decoyListener) maskThen(c net.Conn, raw []byte, hello clientHello, fn func(net.Conn, clientHello)) {
	mc, err := d.mask.Handshake(c, raw, hello)
	if err != nil {
		c.Close()
		return
	}
	fn(mc, hello)
}

// serveMask completes mask TLS and serves HTTP/2 on the masked conn.
// When the client offered only http/1.1 (the WebSocket front), it serves
// HTTP/1.1 instead.
func (d *decoyListener) serveMask(c net.Conn, raw []byte, hello clientHello) {
	mc, err := d.mask.Handshake(c, raw, hello)
	if err != nil {
		c.Close()
		return
	}
	alpn := ""
	if cs, ok := mc.(interface{ ConnectionState() tls.ConnectionState }); ok {
		alpn = cs.ConnectionState().NegotiatedProtocol
	}
	if alpn == "http/1.1" {
		srv := &http.Server{Handler: d.handler, ReadHeaderTimeout: 10 * time.Second}
		ln := &onceLn{c: mc, ch: make(chan struct{})}
		_ = srv.Serve(ln)
		return
	}
	h2s := H2HTTPServer()
	h2s.ServeConn(mc, &http2.ServeConnOpts{Handler: d.handler})
}

// spliceDecoy writes hello to origin (after a PROXY v1 line when proxy) and
// copies both directions.
func spliceDecoy(c net.Conn, hello []byte, origin string, proxy bool) {
	defer c.Close()
	if origin == "" {
		return
	}
	up, err := net.DialTimeout("tcp", origin, 8*time.Second)
	if err != nil {
		return
	}
	defer up.Close()
	tuneTCP(up)
	if proxy {
		if _, err := up.Write(proxyHeader(c)); err != nil {
			return
		}
	}
	if len(hello) > 0 {
		if _, err := up.Write(hello); err != nil {
			return
		}
	}
	errc := make(chan struct{}, 2)
	go func() { _, _ = io.Copy(up, c); errc <- struct{}{} }()
	go func() { _, _ = io.Copy(c, up); errc <- struct{}{} }()
	<-errc
}

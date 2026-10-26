package transport

// CONNECT splice dials host:port without inner mux. Overlay IPs stay on mux.
// Origin mounts this only when fronts lists connect. Every CONNECT carries 0x02.
//
// The connect front is the simplest one: one HTTP CONNECT request carries one
// TCP connection to the internet, with no mux and no extra frame encryption
// inside TLS. It costs a full 0x02 check per connection but has no framing
// overhead. Addresses inside the overlay are refused here because overlay
// traffic needs the mux (member identity, switch rules).

import (
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"syscall"
	"time"

	ecrypto "github.com/a3app/loft/internal/crypto"
	"github.com/a3app/loft/internal/egress"
	"github.com/a3app/loft/internal/wire"
)

// spliceDial, when set, replaces dialOut (tests).
var spliceDial func(network, addr string, d time.Duration) (net.Conn, error)

// spliceLookup is LookupIP; tests stub it.
var spliceLookup = net.LookupIP

// spliceOpen opens the outbound TCP connection for one CONNECT.
func spliceOpen(target string) (net.Conn, error) {
	if spliceDial != nil {
		return spliceDial("tcp4", target, 10*time.Second)
	}
	return dialOut(egress.Who{}, target) // no member behind a bare CONNECT: the default rule
}

// SpliceServer is HTTP CONNECT splice after 0x02. Off unless fronts lists connect.
type SpliceServer struct {
	Token []byte
	Seen  *ecrypto.Nonces
}

// ServeHTTP is ServeSplice with this server's token and nonce cache.
func (s *SpliceServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if s == nil {
		http.NotFound(w, r)
		return
	}
	if s.Seen == nil {
		s.Seen = ecrypto.NewNonces()
	}
	ServeSplice(w, r, s.Token, s.Seen)
}

// ServeSplice is HTTP CONNECT without inner mux. Overlay member IPs stay on mux.
// Failed 0x02 is 404, the same answer an ordinary site gives, so a prober
// learns nothing. Loopback, link-local, multicast, and unspecified are 403.
// The target is also checked against the lists (an ActDrop entry is 403).
// After 200 the connection is hijacked and bytes are copied both ways; any
// bytes the client already sent after the request are forwarded first.
func ServeSplice(w http.ResponseWriter, r *http.Request, token []byte, seen *ecrypto.Nonces) {
	if r.Method != http.MethodConnect {
		http.NotFound(w, r)
		return
	}
	if seen == nil {
		seen = ecrypto.NewNonces()
	}
	blob, _ := wire.DecodeUplink(uplinkHeader(r))
	if _, err := frontAuth(token, blob, seen); err != nil {
		http.NotFound(w, r)
		return
	}
	target := r.Host
	if target == "" {
		target = r.URL.Host
	}
	if _, _, err := net.SplitHostPort(target); err != nil {
		target = net.JoinHostPort(target, "443")
	}
	host, _, err := net.SplitHostPort(target)
	if err != nil || host == "" {
		http.Error(w, "target", http.StatusBadRequest)
		return
	}
	if o := NetSwitch(); o != nil && (o.Contains(host) || o.Routed(host) != "") {
		http.Error(w, "overlay mux", http.StatusBadGateway)
		return
	}
	if spliceDenied(host) {
		http.Error(w, "drop", http.StatusForbidden)
		return
	}
	ip := net.ParseIP(host)
	if LookupAddr(host, ip) == ActDrop {
		http.Error(w, "drop", http.StatusForbidden)
		return
	}
	conn, err := spliceOpen(target)
	if err != nil {
		http.Error(w, "refused", http.StatusBadGateway)
		return
	}
	defer conn.Close()
	w.WriteHeader(http.StatusOK)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	if hj, ok := w.(http.Hijacker); ok {
		raw, bufrw, err := hj.Hijack()
		if err == nil {
			defer raw.Close()
			_ = bufrw.Flush()
			if n := bufrw.Reader.Buffered(); n > 0 {
				buf := make([]byte, n)
				if _, err := io.ReadFull(bufrw.Reader, buf); err == nil {
					_, _ = conn.Write(buf)
				}
			}
			spliceRelay(raw, conn)
			return
		}
	}
	spliceRelay(newHTTPConn(w, r.Body), conn)
}

// LoopbackOpen lets members dial the node's loopback. Only tests set it:
// their echo servers listen on 127.0.0.1.
var LoopbackOpen bool

var errDenied = errors.New("drop")

// spliceDenied is loopback, link-local (includes 169.254.169.254), multicast,
// unspecified, localhost, and cloud metadata names. It keeps members from
// reaching the node's own services and the cloud metadata endpoint. A name
// that does not resolve is denied too.
func spliceDenied(host string) bool {
	h := strings.ToLower(strings.TrimSuffix(strings.Trim(host, "[]"), "."))
	if denied, known := spliceNameDenied(h); known {
		return denied
	}
	ips, err := spliceLookup(h)
	if err != nil || len(ips) == 0 {
		return true
	}
	for _, ip := range ips {
		if spliceIPDenied(ip) {
			return true
		}
	}
	return false
}

// spliceNameDenied decides without a lookup: an IP literal, localhost, or a
// metadata name. known is false for any other name.
func spliceNameDenied(h string) (denied, known bool) {
	if h == "localhost" || strings.HasSuffix(h, ".localhost") ||
		h == "metadata.google.internal" || h == "metadata" {
		return true, true
	}
	if ip := net.ParseIP(h); ip != nil {
		return spliceIPDenied(ip), true
	}
	return false, false
}

// spliceIPDenied is the address check itself; loopback is allowed only
// when LoopbackOpen is set (tests).
func spliceIPDenied(ip net.IP) bool {
	if ip != nil && ip.IsLoopback() {
		return !LoopbackOpen
	}
	return ip == nil || ip.IsMulticast() || ip.IsUnspecified() || ip.IsLinkLocalUnicast()
}

// DeniedIP is the address check egress applies at connect and send time.
func DeniedIP(ip net.IP) bool { return spliceIPDenied(ip) }

// dialGuard checks the address the socket really connects to, so a name
// that resolves differently on the second lookup still drops.
func dialGuard(_, address string, _ syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		return err
	}
	if spliceIPDenied(net.ParseIP(host)) {
		return errDenied
	}
	return nil
}

// spliceRelay copies both directions until either side ends.
func spliceRelay(client, remote net.Conn) {
	errc := make(chan struct{}, 2)
	go func() {
		_, _ = io.Copy(remote, client)
		closeWrite(remote)
		errc <- struct{}{}
	}()
	go func() {
		_, _ = io.Copy(client, remote)
		closeWrite(client)
		errc <- struct{}{}
	}()
	<-errc
	<-errc
}

// closeWrite half-closes c when it supports CloseWrite.
func closeWrite(c net.Conn) {
	if cw, ok := c.(interface{ CloseWrite() error }); ok {
		_ = cw.CloseWrite()
		return
	}
	_ = c.Close()
}

// openConnect issues HTTP CONNECT :authority target on a live GET HTTP/2
// client from the pool (same pick as mux GETs, including h2_max_age recycle).
// This is the client side of the connect front: each call makes a fresh
// 0x02 and sends it in the Cookie header, the same place GET uses.
func (g *GET) openConnect(target string) (net.Conn, error) {
	cl := g.pickClient()
	if cl == nil {
		return nil, fmt.Errorf("connect: no client")
	}
	pr, pw := io.Pipe()
	req, err := http.NewRequest(http.MethodConnect, g.base+"/", pr)
	if err != nil {
		_ = pw.Close()
		return nil, err
	}
	req.Host = target
	req.Header.Set("Host", target)
	blob, _, _, err := ecrypto.MakeAuth(g.token)
	if err != nil {
		_ = pw.Close()
		return nil, err
	}
	req.Header.Set("Cookie", "session="+wire.EncodeUplink(blob, 0))
	resp, err := cl.Do(req)
	if err != nil {
		_ = pw.Close()
		return nil, err
	}
	if resp.StatusCode != http.StatusOK {
		_ = pw.Close()
		slurp, _ := io.ReadAll(io.LimitReader(resp.Body, 256))
		_ = resp.Body.Close()
		return nil, fmt.Errorf("CONNECT %s: status %d %s", target, resp.StatusCode, slurp)
	}
	return &countConn{
		Conn: &pipeConn{Reader: resp.Body, Writer: pw, closer: func() error {
			_ = pw.Close()
			return resp.Body.Close()
		}},
		up:   &g.connUp,
		down: &g.connDown,
	}, nil
}

// countConn counts spliced bytes toward GET.ConnBytes.
type countConn struct {
	net.Conn
	up, down *atomic.Uint64
}

// Read counts downlink bytes.
func (c *countConn) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	if n > 0 && c.down != nil {
		c.down.Add(uint64(n))
	}
	return n, err
}

// Write counts uplink bytes.
func (c *countConn) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	if n > 0 && c.up != nil {
		c.up.Add(uint64(n))
	}
	return n, err
}

// CloseWrite forwards CloseWrite when the inner conn supports it.
func (c *countConn) CloseWrite() error {
	if cw, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return cw.CloseWrite()
	}
	return nil
}

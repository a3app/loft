package transport

// CONNECT splice dials host:port without inner mux. Overlay IPs stay on mux.
// Origin mounts this only when fronts lists connect. Every CONNECT carries 0x02.

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync/atomic"
	"time"

	ecrypto "github.com/a3app/loft/internal/crypto"
	"github.com/a3app/loft/internal/egress"
	"github.com/a3app/loft/internal/wire"
)

// spliceDial, when set, replaces dialOut (tests).
var spliceDial func(network, addr string, d time.Duration) (net.Conn, error)

// spliceLookup is LookupIP; tests stub it.
var spliceLookup = net.LookupIP

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
// Failed 0x02 is 404. Loopback, link-local, multicast, and unspecified are 403.
func ServeSplice(w http.ResponseWriter, r *http.Request, token []byte, seen *ecrypto.Nonces) {
	if r.Method != http.MethodConnect {
		http.NotFound(w, r)
		return
	}
	if seen == nil {
		seen = ecrypto.NewNonces()
	}
	raw := r.Header.Get("X-Request-Id")
	if c := r.Header.Get("Cookie"); strings.HasPrefix(c, "session=") {
		raw = strings.TrimPrefix(c, "session=")
	}
	blob, _ := wire.DecodeUplink(raw)
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

// spliceDenied is loopback, link-local (includes 169.254.169.254), multicast,
// unspecified, localhost, and cloud metadata names.
func spliceDenied(host string) bool {
	h := strings.ToLower(strings.Trim(host, "[]"))
	if h == "localhost" || strings.HasSuffix(h, ".localhost") ||
		h == "metadata.google.internal" || h == "metadata" {
		return true
	}
	if ip := net.ParseIP(h); ip != nil {
		return spliceIPDenied(ip)
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

func spliceIPDenied(ip net.IP) bool {
	return ip == nil || ip.IsLoopback() || ip.IsMulticast() || ip.IsUnspecified() || ip.IsLinkLocalUnicast()
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

// openConnect issues HTTP CONNECT :authority target on the GET HTTP/2 client.
func (g *GET) openConnect(target string) (net.Conn, error) {
	if g.client == nil {
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
	req.Header.Set("X-Auth", "1")
	resp, err := g.client.Do(req)
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

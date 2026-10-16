package transport

// CONNECT splice dials host:port without inner mux. Overlay IPs stay on mux.

import (
	"fmt"
	"io"
	"net"
	"net/http"
	"sync/atomic"
	"time"
)

// ServeSplice is HTTP CONNECT without inner mux. Overlay member IPs stay on mux.
func ServeSplice(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodConnect {
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
	if NetSwitch != nil && NetSwitch.Contains(host) {
		http.Error(w, "overlay mux", http.StatusBadGateway)
		return
	}
	ip := net.ParseIP(host)
	if LookupAddr(host, ip) == ActDrop {
		http.Error(w, "drop", http.StatusForbidden)
		return
	}
	conn, err := net.DialTimeout("tcp4", target, 10*time.Second)
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

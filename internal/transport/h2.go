package transport

// HTTP/2 CONNECT front: ALPN h2, CONNECT to a node-local path, then raw mux.

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/http2"

	"github.com/a3app/loft/internal/config"
	ecrypto "github.com/a3app/loft/internal/crypto"
	"github.com/a3app/loft/internal/mux"
	"github.com/a3app/loft/internal/wire"
)

// h2StreamWindow is the HTTP/2 stream flow-control window for CONNECT.
// Spec default 64 KiB (and x/net server default 1 MiB) stalls a ~80–200 ms
// RTT CONNECT at a few hundred kbit/s; 4 MiB matches the client transport.
const h2StreamWindow = 4 << 20

// h2ConnWindow is the connection window. Must be ≥ stream window.
const h2ConnWindow = 8 << 20

// H2HTTPServer is the origin HTTP/2 settings for CONNECT, cover, and hub:2.
func H2HTTPServer() *http2.Server {
	return &http2.Server{
		MaxConcurrentStreams:         256,
		MaxReadFrameSize:             1 << 20,
		IdleTimeout:                  120 * time.Second,
		MaxUploadBufferPerStream:     h2StreamWindow,
		MaxUploadBufferPerConnection: h2ConnWindow,
	}
}

// H2 is HTTP/2 CONNECT. Extra front; get/ws/quic stay.
type H2 struct {
	cfg   config.Outbound
	token []byte
	offer wire.Cap
	inner *rawSess
	cc    *http2.ClientConn
}

// NewH2 builds an H2 client that has not yet CONNECT'd.
func NewH2(token string, o config.Outbound, offer wire.Cap) *H2 {
	return &H2{cfg: o, token: []byte(token), offer: offer}
}

// Session returns the inner mux session.
func (h *H2) Session() *mux.Session {
	if h.inner == nil {
		return nil
	}
	return h.inner.Session()
}

// Done is closed when the CONNECT stream or mux dies.
func (h *H2) Done() <-chan struct{} {
	if h.inner == nil {
		ch := make(chan struct{})
		close(ch)
		return ch
	}
	return h.inner.Done()
}

// Open starts the front if needed and opens a mux stream to target.
func (h *H2) Open(target string) (net.Conn, error) {
	if h.inner == nil {
		if err := h.Start(context.Background()); err != nil {
			return nil, err
		}
	}
	return h.inner.Open(target)
}

// Park drops CONNECT without Leave or mux.Close.
func (h *H2) Park() error {
	var err error
	if h.inner != nil {
		err = h.inner.Park()
	}
	if h.cc != nil {
		_ = h.cc.Close()
	}
	return err
}

// Close sends CmdLeave and closes the CONNECT stream.
func (h *H2) Close() error {
	var err error
	if h.inner != nil {
		err = h.inner.Close()
	}
	if h.cc != nil {
		_ = h.cc.Close()
	}
	return err
}

// Start dials TLS (or mask) and issues HTTP/2 CONNECT.
func (h *H2) Start(ctx context.Context) error {
	addr, sni, insecure, err := resolveTLS(h.cfg)
	if err != nil {
		return err
	}
	path := h.cfg.Path
	if path == "" {
		path = "/api/v1/tunnel"
	}
	tok := h.token
	if pub := CachedMaskPub(addr); len(pub) == 32 {
		c, err := DialMask(addr, sni, h.cfg.Fingerprint, tok, pub)
		if err != nil {
			return err
		}
		return h.connectH2(ctx, c, sni, path, addr)
	}
	c, err := dialTLS(addr, sni, h.cfg.Fingerprint, insecure, tok)
	if err != nil {
		return err
	}
	return h.connectH2(ctx, c, sni, path, addr)
}

// connectH2 issues CONNECT on c and starts raw mux on the 200 body.
func (h *H2) connectH2(ctx context.Context, c net.Conn, sni, path, addr string) error {
	t2 := &http2.Transport{
		ReadIdleTimeout:  20 * time.Second,
		MaxReadFrameSize: 1 << 20,
	}
	cc, err := t2.NewClientConn(c)
	if err != nil {
		c.Close()
		return err
	}
	pr, pw := io.Pipe()
	// Handshake ctx must not own the CONNECT stream: loft-probe cancels it
	// after Start, which would RST a live mux.
	req, err := http.NewRequestWithContext(context.Background(), http.MethodConnect, "https://"+sni+path, pr)
	if err != nil {
		_ = cc.Close()
		return err
	}
	req.Host = sni
	req.Header.Set(":protocol", "loft")
	type rt struct {
		resp *http.Response
		err  error
	}
	ch := make(chan rt, 1)
	go func() {
		resp, err := cc.RoundTrip(req)
		ch <- rt{resp, err}
	}()
	var resp *http.Response
	select {
	case <-ctx.Done():
		_ = pw.Close()
		_ = cc.Close()
		return ctx.Err()
	case r := <-ch:
		resp, err = r.resp, r.err
	}
	if err != nil {
		_ = pw.Close()
		_ = cc.Close()
		return fmt.Errorf("h2 connect: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		_ = pw.Close()
		_ = resp.Body.Close()
		_ = cc.Close()
		return fmt.Errorf("h2 connect: status %d", resp.StatusCode)
	}
	conn := &pipeConn{Reader: resp.Body, Writer: pw, closer: func() error {
		_ = pw.Close()
		return resp.Body.Close()
	}}
	inner, err := startRaw(ctx, conn, h.token, h.offer, addr, sni)
	if err != nil {
		_ = cc.Close()
		return err
	}
	h.cc = cc
	h.inner = inner
	return nil
}

// pipeConn is a net.Conn over an HTTP CONNECT request body and response body.
type pipeConn struct {
	io.Reader
	io.Writer
	closer func() error
}

// Close closes both sides of the CONNECT stream.
func (p *pipeConn) Close() error {
	if p.closer != nil {
		return p.closer()
	}
	return nil
}

// CloseWrite closes the request body pipe.
func (p *pipeConn) CloseWrite() error {
	if cw, ok := p.Writer.(interface{ Close() error }); ok {
		return cw.Close()
	}
	return nil
}

// LocalAddr implements net.Conn.
func (p *pipeConn) LocalAddr() net.Addr { return dummyAddr("h2") }

// RemoteAddr implements net.Conn.
func (p *pipeConn) RemoteAddr() net.Addr { return dummyAddr("h2") }

// SetDeadline implements net.Conn.
func (p *pipeConn) SetDeadline(t time.Time) error { return nil }

// SetReadDeadline implements net.Conn.
func (p *pipeConn) SetReadDeadline(t time.Time) error { return nil }

// SetWriteDeadline implements net.Conn.
func (p *pipeConn) SetWriteDeadline(t time.Time) error { return nil }

// dummyAddr is a stub net.Addr for CONNECT streams that have no TCP addr.
type dummyAddr string

// Network returns the stub network name.
func (d dummyAddr) Network() string { return string(d) }

// String returns the stub address.
func (d dummyAddr) String() string { return string(d) }

// H2Server is the origin for HTTP/2 CONNECT (and HTTP/3 CONNECT via the same handler).
type H2Server struct {
	Info NodeInfo
	Path string
	Seen *ecrypto.Nonces
}

// Match reports whether r is CONNECT to this server's path.
func (s *H2Server) Match(r *http.Request) bool {
	if r.Method != http.MethodConnect {
		return false
	}
	p := r.URL.Path
	if p == "" || p == "/" {
		p = r.Host
	}
	want := s.Path
	if want == "" {
		want = "/api/v1/tunnel"
	}
	return p == want || strings.HasPrefix(p, want+"/")
}

// ServeHTTP answers CONNECT 200 and runs raw mux on the hijacked body.
func (s *H2Server) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodConnect {
		http.NotFound(w, r)
		return
	}
	w.WriteHeader(http.StatusOK)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	conn := newHTTPConn(w, r.Body)
	serveRaw(conn, s.Info, s.Seen)
}

// corkBuf / corkWait match SSTP: coalesce DATA frames instead of Flush
// after every mux write (that ping-ponged a 64–48 KiB window per RTT).
const corkBuf = 16 << 10
const corkWait = 2 * time.Millisecond

// corkWriter batches writes and delayed-flushes the inner writer.
type corkWriter struct {
	mu     sync.Mutex
	bw     *bufio.Writer
	closed chan struct{}
	once   sync.Once
}

func newCorkWriter(w io.Writer) *corkWriter {
	c := &corkWriter{
		bw:     bufio.NewWriterSize(w, corkBuf),
		closed: make(chan struct{}),
	}
	go c.flushLoop()
	return c
}

func (c *corkWriter) Write(p []byte) (int, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.bw.Write(p)
}

func (c *corkWriter) Flush() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.bw.Flush()
}

func (c *corkWriter) Close() error {
	c.once.Do(func() {
		close(c.closed)
		c.mu.Lock()
		_ = c.bw.Flush()
		c.mu.Unlock()
	})
	return nil
}

func (c *corkWriter) flushLoop() {
	t := time.NewTicker(corkWait)
	defer t.Stop()
	for {
		select {
		case <-c.closed:
			return
		case <-t.C:
			c.mu.Lock()
			_ = c.bw.Flush()
			c.mu.Unlock()
		}
	}
}

// flushWriter writes then HTTP/2-flushes so corked bytes leave as DATA frames.
type flushWriter struct{ w io.Writer }

func (f flushWriter) Write(p []byte) (int, error) {
	n, err := f.w.Write(p)
	if err == nil {
		if fl, ok := f.w.(http.Flusher); ok {
			fl.Flush()
		}
	}
	return n, err
}

// httpConn is a net.Conn over ResponseWriter and request body.
type httpConn struct {
	cork *corkWriter
	r    io.Reader
}

func newHTTPConn(w io.Writer, r io.Reader) *httpConn {
	return &httpConn{cork: newCorkWriter(flushWriter{w: w}), r: r}
}

// Read reads the request body.
func (c *httpConn) Read(p []byte) (int, error) { return c.r.Read(p) }

// Write corks the response body; the ticker or a full buffer flushes DATA.
func (c *httpConn) Write(p []byte) (int, error) {
	return c.cork.Write(p)
}

// Close flushes corked DATA and closes the request body.
func (c *httpConn) Close() error {
	if c.cork != nil {
		_ = c.cork.Close()
	}
	if rc, ok := c.r.(io.Closer); ok {
		_ = rc.Close()
	}
	return nil
}

// CloseWrite flushes corked DATA.
func (c *httpConn) CloseWrite() error {
	if c.cork != nil {
		return c.cork.Flush()
	}
	return nil
}

// LocalAddr implements net.Conn.
func (c *httpConn) LocalAddr() net.Addr { return dummyAddr("h2") }

// RemoteAddr implements net.Conn.
func (c *httpConn) RemoteAddr() net.Addr { return dummyAddr("h2") }

// SetDeadline implements net.Conn.
func (c *httpConn) SetDeadline(t time.Time) error { return nil }

// SetReadDeadline implements net.Conn.
func (c *httpConn) SetReadDeadline(t time.Time) error { return nil }

// SetWriteDeadline implements net.Conn.
func (c *httpConn) SetWriteDeadline(t time.Time) error { return nil }

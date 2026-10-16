package xtls

// XHTTP (SplitHTTP) stream-one: one HTTP/2 POST, body is the proxy stream.

import (
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"golang.org/x/net/http2"
)

const xhttpUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

// XHTTP is path / Host for a stream-one POST.
type XHTTP struct {
	Host string
	SNI  string
	Addr string
	Path string
}

type xhttpConn struct {
	tls  net.Conn
	pw   *io.PipeWriter
	body io.ReadCloser
	once sync.Once
	mu   sync.Mutex
	err  error
	got  chan struct{}
}

// DialXHTTP runs an HTTP/2 POST over an already-handshaked TLS or REALITY conn.
func DialXHTTP(tlsConn net.Conn, x XHTTP) (net.Conn, error) {
	host := x.Host
	if host == "" {
		host = x.SNI
	}
	if host == "" {
		h, _, _ := net.SplitHostPort(x.Addr)
		host = h
	}
	path := x.Path
	if path == "" {
		path = "/"
	}
	pr, pw := io.Pipe()
	req, err := http.NewRequest(http.MethodPost, "https://"+host+path, pr)
	if err != nil {
		tlsConn.Close()
		return nil, err
	}
	req.Header.Set("User-Agent", xhttpUA)
	req.Host = host
	req.ContentLength = -1
	tr := &http2.Transport{
		AllowHTTP: true,
		DialTLS: func(network, addr string, cfg *tls.Config) (net.Conn, error) {
			return tlsConn, nil
		},
	}
	xc := &xhttpConn{tls: tlsConn, pw: pw, got: make(chan struct{})}
	go func() {
		resp, err := tr.RoundTrip(req)
		if err != nil {
			xc.fail(err)
			_ = pr.CloseWithError(err)
			return
		}
		if resp.StatusCode != 200 {
			resp.Body.Close()
			err = fmt.Errorf("xhttp: HTTP %d", resp.StatusCode)
			xc.fail(err)
			_ = pr.CloseWithError(err)
			return
		}
		xc.mu.Lock()
		xc.body = resp.Body
		xc.mu.Unlock()
		close(xc.got)
	}()
	return xc, nil
}

func (c *xhttpConn) fail(err error) {
	c.mu.Lock()
	if c.err == nil {
		c.err = err
	}
	c.mu.Unlock()
	c.once.Do(func() { close(c.got) })
}

func (c *xhttpConn) Read(p []byte) (int, error) {
	<-c.got
	c.mu.Lock()
	body, err := c.body, c.err
	c.mu.Unlock()
	if body == nil {
		if err != nil {
			return 0, err
		}
		return 0, io.EOF
	}
	return body.Read(p)
}

func (c *xhttpConn) Write(p []byte) (int, error) {
	return c.pw.Write(p)
}

func (c *xhttpConn) Close() error {
	_ = c.pw.Close()
	c.mu.Lock()
	body := c.body
	c.mu.Unlock()
	if body != nil {
		_ = body.Close()
	}
	return c.tls.Close()
}

func (c *xhttpConn) LocalAddr() net.Addr                { return c.tls.LocalAddr() }
func (c *xhttpConn) RemoteAddr() net.Addr               { return c.tls.RemoteAddr() }
func (c *xhttpConn) SetDeadline(t time.Time) error      { return c.tls.SetDeadline(t) }
func (c *xhttpConn) SetReadDeadline(t time.Time) error  { return c.tls.SetReadDeadline(t) }
func (c *xhttpConn) SetWriteDeadline(t time.Time) error { return c.tls.SetWriteDeadline(t) }

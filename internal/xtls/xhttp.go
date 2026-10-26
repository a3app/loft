package xtls

// XHTTP (SplitHTTP). stream-one is one HTTP/2 POST. stream-up, which current
// Xray panels put on the share link, is two requests on that connection:
// GET carries the download, POST carries the upload. Both use one session
// id appended to the path. Padding is a Referer query x_padding of X's,
// length picked from the link's range (default 100-1000). The POST is
// marked application/grpc, which is Xray's default camouflage and what
// grpc_pass frontends require.

import (
	"context"
	"crypto/rand"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/http2"
)

// xhttpUA is the User-Agent of the request: a desktop Chrome, to look like a
// browser.
const xhttpUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

// XHTTP is path, Host and the Xray mode for one session.
// PadFrom, PadTo are the inclusive xPaddingBytes range. 0,0 is 100-1000.
type XHTTP struct {
	Host    string
	SNI     string
	Addr    string
	Path    string
	Mode    string
	PadFrom int
	PadTo   int
}

// xhttpConn is a net.Conn over one POST: writes go into the request body
// through a pipe, reads come from the response body. Reads wait until the
// response headers arrive (got); if the request failed they return its
// error. A read deadline also covers that wait: the TLS deadline does not,
// because the body is not readable until got closes.
type xhttpConn struct {
	tls  net.Conn
	pw   *io.PipeWriter
	body io.ReadCloser
	once sync.Once
	mu   sync.Mutex
	err  error
	got  chan struct{}
	rdl  time.Time
}

// DialXHTTP runs XHTTP over an already-handshaked TLS or REALITY conn.
// It returns at once. The read side opens on a 200. packet-up is refused:
// that mode is a sequence of short POSTs this client does not speak.
func DialXHTTP(tlsConn net.Conn, x XHTTP) (net.Conn, error) {
	switch strings.ToLower(strings.TrimSpace(x.Mode)) {
	case "packet-up":
		tlsConn.Close()
		return nil, fmt.Errorf("xhttp: packet-up")
	case "stream-up":
		return dialStreamUp(tlsConn, x)
	default:
		return dialStreamOne(tlsConn, x)
	}
}

// dialStreamOne is one POST: the request body is the upload, the 200 body
// is the download. Path is used as written.
func dialStreamOne(tlsConn net.Conn, x XHTTP) (net.Conn, error) {
	host := xhttpHost(x)
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
	xc := &xhttpConn{tls: tlsConn, pw: pw, got: make(chan struct{})}
	tr := xhttpTransport(tlsConn)
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

// dialStreamUp matches Xray stream-up. The session id is a UUID on the
// path. Referer is built from the path before that id is appended, with a
// fresh x_padding on each request. GET is the download. POST is the
// upload and its response is not the byte stream.
func dialStreamUp(tlsConn net.Conn, x XHTTP) (net.Conn, error) {
	host := xhttpHost(x)
	base := xhttpBasePath(x.Path)
	id := newSessionID()
	full := base + id
	pr, pw := io.Pipe()
	get, err := http.NewRequest(http.MethodGet, "https://"+host+full, nil)
	if err != nil {
		tlsConn.Close()
		_ = pw.Close()
		return nil, err
	}
	post, err := http.NewRequest(http.MethodPost, "https://"+host+full, pr)
	if err != nil {
		tlsConn.Close()
		_ = pw.Close()
		return nil, err
	}
	dress(get, host, base, x)
	dress(post, host, base, x)
	post.Header.Set("Content-Type", "application/grpc")
	post.ContentLength = -1
	xc := &xhttpConn{tls: tlsConn, pw: pw, got: make(chan struct{})}
	tr := xhttpTransport(tlsConn)
	go func() {
		resp, err := tr.RoundTrip(get)
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
		xc.ready(resp.Body)
	}()
	go func() {
		resp, err := tr.RoundTrip(post)
		if err != nil {
			xc.fail(err)
			_ = pr.CloseWithError(err)
			return
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			err = fmt.Errorf("xhttp: HTTP %d", resp.StatusCode)
			xc.fail(err)
			_ = pr.CloseWithError(err)
			return
		}
		_, _ = io.Copy(io.Discard, resp.Body)
	}()
	return xc, nil
}

// dress sets the headers Xray puts on a stream-up request. Referer uses
// base, the path before the session id.
func dress(req *http.Request, host, base string, x XHTTP) {
	req.Header.Set("User-Agent", xhttpUA)
	req.Header.Set("Referer", paddingReferer(host, base, pickPad(x.PadFrom, x.PadTo)))
	req.Host = host
}

func xhttpHost(x XHTTP) string {
	if x.Host != "" {
		return x.Host
	}
	if x.SNI != "" {
		return x.SNI
	}
	h, _, _ := net.SplitHostPort(x.Addr)
	return h
}

// xhttpBasePath is the directory the session id is appended to.
// Xray adds a trailing slash whenever the session lives in the path.
func xhttpBasePath(p string) string {
	if i := strings.IndexAny(p, "?#"); i >= 0 {
		p = p[:i]
	}
	if p == "" {
		p = "/"
	}
	if !strings.HasPrefix(p, "/") {
		p = "/" + p
	}
	if !strings.HasSuffix(p, "/") {
		p += "/"
	}
	return p
}

// xhttpTransport speaks HTTP/2 on the one conn the caller already
// handshaked. Compression stays off: the body is a raw byte stream.
func xhttpTransport(tlsConn net.Conn) *http2.Transport {
	var once sync.Once
	return &http2.Transport{
		AllowHTTP:          true,
		DisableCompression: true,
		DialTLSContext: func(ctx context.Context, network, addr string, cfg *tls.Config) (net.Conn, error) {
			var c net.Conn
			once.Do(func() { c = tlsConn })
			if c == nil {
				return nil, fmt.Errorf("xhttp: conn")
			}
			return c, nil
		},
	}
}

// paddingReferer is the Xray default placement: Referer query x_padding,
// value a run of X of the rolled length. The session id is not in this URL.
func paddingReferer(host, path string, n int) string {
	u := url.URL{
		Scheme:   "https",
		Host:     host,
		Path:     path,
		RawQuery: "x_padding=" + strings.Repeat("X", n),
	}
	return u.String()
}

// pickPad rolls an inclusive length. 0,0 is the Xray default 100-1000.
func pickPad(from, to int) int {
	if to <= 0 {
		from, to = 100, 1000
	}
	if from < 0 {
		from = 0
	}
	if to < from {
		to = from
	}
	if from == to {
		return from
	}
	return from + randIntN(to-from+1)
}

func randIntN(n int) int {
	if n <= 1 {
		return 0
	}
	var b [4]byte
	_, _ = rand.Read(b[:])
	u := uint32(b[0]) | uint32(b[1])<<8 | uint32(b[2])<<16 | uint32(b[3])<<24
	return int(u % uint32(n))
}

// newSessionID is an RFC 4122 UUID string, the Xray default session id.
func newSessionID() string {
	var b [16]byte
	_, _ = rand.Read(b[:])
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:])
}

// ready publishes the download body and wakes readers.
func (c *xhttpConn) ready(body io.ReadCloser) {
	c.mu.Lock()
	c.body = body
	c.mu.Unlock()
	c.once.Do(func() { close(c.got) })
}

// fail records the first error and wakes readers.
func (c *xhttpConn) fail(err error) {
	c.mu.Lock()
	if c.err == nil {
		c.err = err
	}
	c.mu.Unlock()
	c.once.Do(func() { close(c.got) })
}

func (c *xhttpConn) Read(p []byte) (int, error) {
	if err := c.waitBody(); err != nil {
		return 0, err
	}
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

// waitBody blocks until the download headers arrive or the read deadline
// passes. A closed got wins over a deadline that fires in the same instant.
func (c *xhttpConn) waitBody() error {
	c.mu.Lock()
	dl := c.rdl
	c.mu.Unlock()
	if !dl.IsZero() && !time.Now().Before(dl) {
		select {
		case <-c.got:
			return nil
		default:
			return errXHTTPTimeout
		}
	}
	if dl.IsZero() {
		<-c.got
		return nil
	}
	timer := time.NewTimer(time.Until(dl))
	defer timer.Stop()
	select {
	case <-c.got:
		return nil
	case <-timer.C:
		select {
		case <-c.got:
			return nil
		default:
			return errXHTTPTimeout
		}
	}
}

// errXHTTPTimeout is a net.Error. os.ErrDeadlineExceeded is not, and the
// Mac UI treats only an "i/o timeout" as «сервер не отвечает».
var errXHTTPTimeout timeoutError

type timeoutError struct{}

func (timeoutError) Error() string   { return "xhttp: i/o timeout" }
func (timeoutError) Timeout() bool   { return true }
func (timeoutError) Temporary() bool { return true }

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

func (c *xhttpConn) LocalAddr() net.Addr  { return c.tls.LocalAddr() }
func (c *xhttpConn) RemoteAddr() net.Addr { return c.tls.RemoteAddr() }

func (c *xhttpConn) SetDeadline(t time.Time) error {
	c.setReadDeadline(t)
	return c.tls.SetDeadline(t)
}

func (c *xhttpConn) SetReadDeadline(t time.Time) error {
	c.setReadDeadline(t)
	return c.tls.SetReadDeadline(t)
}

func (c *xhttpConn) SetWriteDeadline(t time.Time) error { return c.tls.SetWriteDeadline(t) }

func (c *xhttpConn) setReadDeadline(t time.Time) {
	c.mu.Lock()
	c.rdl = t
	c.mu.Unlock()
}

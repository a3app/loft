// Package vless is a client for VLESS, the proxy protocol of Xray and
// V2Ray. loft speaks it so its apps can also connect to an existing Xray
// server: the user pastes a vless:// share link and the app sends traffic
// through that server instead of a loft node.
//
// VLESS is simple: every TCP connection to the server opens with a short
// header (version, the user's UUID as the password, command, target
// address) and the server answers with a 2-byte header; after that the
// connection is a plain byte stream to the target. Encryption comes from
// the layer below: ordinary TLS or REALITY (see internal/xtls), optionally
// inside an XHTTP POST. "Vision" (flow xtls-rprx-vision) pads the first
// packets so the TLS handshake of the site inside the tunnel does not show
// through as a recognisable size pattern.
//
// There is no loft mux here: every stream is its own connection, and there
// is no overlay address, so the apps use the fixed TunIP.
package vless

// Client is a transport.Dialer: each Open is a new VLESS TCP stream.

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/a3app/loft/internal/mux"
	"github.com/a3app/loft/internal/protect"
	"github.com/a3app/loft/internal/xtls"
)

// Config is one VLESS outbound (UUID + server).
type Config struct {
	URL      string
	UUID     string
	SNI      string
	Insecure bool
	Log      func(string)
}

// Client dials VLESS TCP (+ TLS) per Open. Session is always nil (no loft mux).
type Client struct {
	ep       Endpoint
	uuid     [16]byte
	insecure bool
	log      func(string)
	up, down atomic.Uint64
	done     chan struct{}
	once     sync.Once
}

// New parses cfg and prepares a dialer. Call Probe before serving SOCKS.
func New(cfg Config) (*Client, error) {
	ep, err := ParseURL(cfg.URL, cfg.UUID)
	if err != nil {
		return nil, err
	}
	if cfg.SNI != "" {
		ep.SNI = cfg.SNI
	}
	switch ep.Network {
	case "", "tcp", "xhttp":
	default:
		return nil, fmt.Errorf("vless transport %s", ep.Network)
	}
	if ep.Reality {
		if _, err := decodePbk(ep.PublicKey); err != nil {
			return nil, err
		}
		if _, err := decodeSid(ep.ShortID); err != nil {
			return nil, err
		}
	}
	id, err := parseUUID(ep.UUID)
	if err != nil {
		return nil, err
	}
	c := &Client{ep: ep, uuid: id, insecure: cfg.Insecure, log: cfg.Log, done: make(chan struct{})}
	return c, nil
}

// Probe dials TLS (if any) and one VLESS header so a bad UUID fails before SOCKS.
// The test target is 1.1.1.1:443; a server that rejects the UUID sends
// no valid response header within 12 s, and that is the error.
func (c *Client) Probe(ctx context.Context) error {
	c.stage("vless")
	conn, err := c.dial(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	c.stage("handshake")
	if err := writeRequest(conn, c.uuid, "1.1.1.1:443", c.ep.Vision); err != nil {
		return err
	}
	_ = conn.SetReadDeadline(time.Now().Add(12 * time.Second))
	if err := readResponse(conn); err != nil {
		return fmt.Errorf("vless: %w", err)
	}
	return nil
}

// Open dials the server and starts a VLESS TCP stream to target (host:port).
// It waits up to 15 s for the response header, so a refused UUID fails here
// and not on the first read.
func (c *Client) Open(target string) (net.Conn, error) {
	conn, err := c.dial(context.Background())
	if err != nil {
		return nil, err
	}
	if err := writeRequest(conn, c.uuid, target, c.ep.Vision); err != nil {
		conn.Close()
		return nil, err
	}
	_ = conn.SetReadDeadline(time.Now().Add(15 * time.Second))
	if err := readResponse(conn); err != nil {
		conn.Close()
		return nil, err
	}
	_ = conn.SetReadDeadline(time.Time{})
	if c.ep.Vision {
		conn = wrapVision(conn, c.uuid)
	}
	return &countedConn{Conn: conn, c: c}, nil
}

// dial makes the connection a VLESS header goes on: TCP, then REALITY or
// TLS (or neither, for security=none), then XHTTP if asked for. Without a
// context deadline the setup gets 15 s.
func (c *Client) dial(ctx context.Context) (net.Conn, error) {
	d := protect.NetDialer(15 * time.Second)
	c.stage("tcp")
	raw, err := d.DialContext(ctx, "tcp", c.ep.Addr)
	if err != nil {
		return nil, err
	}
	if dl, ok := ctx.Deadline(); ok {
		_ = raw.SetDeadline(dl)
	} else {
		_ = raw.SetDeadline(time.Now().Add(15 * time.Second))
	}
	var conn net.Conn = raw
	switch {
	case c.ep.Reality:
		c.stage("reality")
		conn, err = xtls.DialReality(raw, c.ep.reality(), c.insecure)
		if err != nil {
			return nil, err
		}
	case c.ep.TLS:
		c.stage("tls")
		cfg := &tls.Config{
			ServerName:         c.ep.SNI,
			InsecureSkipVerify: c.insecure,
			MinVersion:         tls.VersionTLS12,
			NextProtos:         c.alpn(),
		}
		tc := tls.Client(raw, cfg)
		if err := tc.Handshake(); err != nil {
			raw.Close()
			return nil, err
		}
		conn = tc
	}
	_ = conn.SetDeadline(time.Time{})
	if c.ep.xhttp() {
		c.stage("xhttp")
		return xtls.DialXHTTP(conn, c.ep.xhttpCfg())
	}
	return conn, nil
}

// alpn asks for HTTP/2 when XHTTP runs over the TLS connection.
func (c *Client) alpn() []string {
	if c.ep.xhttp() {
		return []string{"h2"}
	}
	return nil
}

// stage logs the step being tried, so a failed connect shows where it stopped.
func (c *Client) stage(s string) {
	if c.log != nil {
		c.log(s)
	}
}

// Session is nil: VLESS is not a loft mux.
func (c *Client) Session() *mux.Session { return nil }

// Done closes when Close is called.
func (c *Client) Done() <-chan struct{} { return c.done }

// Close stops the helper; in-flight streams keep their sockets.
func (c *Client) Close() error {
	c.once.Do(func() { close(c.done) })
	return nil
}

// Bytes is STATUS spark counters.
func (c *Client) Bytes() (up, down uint64) { return c.up.Load(), c.down.Load() }

// countedConn adds a stream's bytes to the client's up/down totals.
type countedConn struct {
	net.Conn
	c *Client
}

func (w *countedConn) Read(p []byte) (int, error) {
	n, err := w.Conn.Read(p)
	if n > 0 {
		w.c.down.Add(uint64(n))
	}
	return n, err
}

func (w *countedConn) Write(p []byte) (int, error) {
	n, err := w.Conn.Write(p)
	if n > 0 {
		w.c.up.Add(uint64(n))
	}
	return n, err
}

// CloseWrite passes a half-close through when the connection below has one.
func (w *countedConn) CloseWrite() error {
	if cw, ok := w.Conn.(interface{ CloseWrite() error }); ok {
		return cw.CloseWrite()
	}
	return nil
}

var _ io.ReadWriteCloser = (*countedConn)(nil)

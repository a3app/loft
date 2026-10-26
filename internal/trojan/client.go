// Package trojan is a client for Trojan, a proxy protocol that looks like
// an ordinary HTTPS site: the client opens TLS to the server and sends a
// hash of the password, a command and a target address. A server that
// does not recognise the hash hands the connection to a real web site, so
// a scanner never sees anything unusual. loft speaks it so its apps can use
// an existing Trojan or Xray server from a trojan:// share link.
//
// The server sends no answer to the header: if the password is wrong the
// stream simply carries the fallback site's reply. Like VLESS, every stream
// is its own connection, over TLS or REALITY and optionally XHTTP (see
// internal/xtls). UDP goes inside a TCP stream with the UDP ASSOCIATE
// command, one length-prefixed datagram after another.
package trojan

// Client is a transport.Dialer: each Open is a new Trojan TCP stream.

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

// Config is one Trojan outbound (password + server).
type Config struct {
	URL      string
	Password string
	SNI      string
	Insecure bool
	Log      func(string)
}

// Client dials Trojan TCP (+ TLS / REALITY / XHTTP) per Open.
type Client struct {
	ep       Endpoint
	insecure bool
	log      func(string)
	up, down atomic.Uint64
	done     chan struct{}
	once     sync.Once
}

// New parses cfg and prepares a dialer. Call Probe before serving SOCKS.
func New(cfg Config) (*Client, error) {
	ep, err := ParseURL(cfg.URL, cfg.Password)
	if err != nil {
		return nil, err
	}
	if cfg.SNI != "" {
		ep.SNI = cfg.SNI
	}
	switch ep.Network {
	case "", "tcp", "xhttp":
	default:
		return nil, fmt.Errorf("trojan transport %s", ep.Network)
	}
	if ep.Reality {
		if _, err := xtls.DecodePbk(ep.PublicKey); err != nil {
			return nil, err
		}
		if _, err := xtls.DecodeSid(ep.ShortID); err != nil {
			return nil, err
		}
	}
	if ep.Password == "" {
		return nil, fmt.Errorf("trojan password")
	}
	c := &Client{ep: ep, insecure: cfg.Insecure || ep.Insecure, log: cfg.Log, done: make(chan struct{})}
	return c, nil
}

// Probe dials TLS (if any) and one Trojan header so a bad URL fails before SOCKS.
// Trojan sends no reply, so this proves the server and its TLS work, but
// not that the password is right.
func (c *Client) Probe(ctx context.Context) error {
	c.stage("trojan")
	conn, err := c.dial(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	c.stage("handshake")
	if err := writeRequest(conn, c.ep.Password, "1.1.1.1:443", false); err != nil {
		return fmt.Errorf("trojan: %w", err)
	}
	return nil
}

// Open dials the server and starts a Trojan TCP stream to target (host:port).
func (c *Client) Open(target string) (net.Conn, error) {
	conn, err := c.dial(context.Background())
	if err != nil {
		return nil, err
	}
	if err := writeRequest(conn, c.ep.Password, target, false); err != nil {
		conn.Close()
		return nil, err
	}
	return &countedConn{Conn: conn, c: c}, nil
}

// ListenUDP opens one Trojan UDP ASSOCIATE stream. Every datagram on it
// names its own destination, so one stream serves all targets.
func (c *Client) ListenUDP() (net.PacketConn, error) {
	conn, err := c.dial(context.Background())
	if err != nil {
		return nil, err
	}
	if err := writeRequest(conn, c.ep.Password, "0.0.0.0:0", true); err != nil {
		conn.Close()
		return nil, err
	}
	return &udpConn{Conn: conn, c: c, local: &net.UDPAddr{IP: net.ParseIP(TunIP), Port: 0}}, nil
}

// dial makes the connection a Trojan header goes on: TCP, then REALITY or
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

// Session is nil: Trojan is not a loft mux.
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

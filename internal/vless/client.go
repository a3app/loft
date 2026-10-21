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

func (c *Client) alpn() []string {
	if c.ep.xhttp() {
		return []string{"h2"}
	}
	return nil
}

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

func (w *countedConn) CloseWrite() error {
	if cw, ok := w.Conn.(interface{ CloseWrite() error }); ok {
		return cw.CloseWrite()
	}
	return nil
}

var _ io.ReadWriteCloser = (*countedConn)(nil)

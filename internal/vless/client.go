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
	"bytes"
	"context"
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

// Probe dials and writes one VLESS header, then returns. Xray keeps the
// 2-byte response buffered until the target sends its first byte, so a
// read here against 1.1.1.1:443 only burns the deadline. A rejected UUID
// shows up on the first Open, the same way Trojan reports a bad password.
func (c *Client) Probe(ctx context.Context) error {
	c.stage("vless")
	conn, err := c.dial(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()
	c.stage("handshake")
	if err := writeUplink(conn, c.uuid, "1.1.1.1:443", c.ep.Vision); err != nil {
		return err
	}
	return nil
}

// writeUplink writes the VLESS header and, for Vision, the empty padding
// frame, as one Write. Xray's inbound does a single Read into the first
// buffer and only then parses the header. The UUID frame has to already
// be in that buffer. A second TLS record is a later Read: if it does not
// start with the UUID the unpadder forwards it raw and never recovers, so
// the target never sees a ClientHello and the response stays buffered.
func writeUplink(conn net.Conn, uuid [16]byte, target string, vision bool) error {
	var buf bytes.Buffer
	if err := writeRequest(&buf, uuid, target, vision); err != nil {
		return err
	}
	if vision {
		buf.Write(visionHello(uuid))
	}
	_, err := conn.Write(buf.Bytes())
	return err
}

// Open dials the server and writes one VLESS header. The 2-byte response is
// consumed on the first Read, not before return. Xray keeps that response
// buffered until the target sends a byte, and a TLS target sends nothing
// until it has the ClientHello. Waiting here deadlocks the copy: SOCKS
// never sees a success, and the bytes that would unblock the server are
// never written. A refused UUID fails that first Read, within 15 s.
func (c *Client) Open(target string) (net.Conn, error) {
	conn, err := c.dial(context.Background())
	if err != nil {
		return nil, err
	}
	if err := writeUplink(conn, c.uuid, target, c.ep.Vision); err != nil {
		conn.Close()
		return nil, err
	}
	var out net.Conn = &headerConn{Conn: conn}
	if c.ep.Vision {
		out = wrapVisionContinue(out, c.uuid)
	}
	return &countedConn{Conn: out, c: c}, nil
}

// headerConn strips the VLESS response header on the first Read. Write goes
// to the server immediately, so the target can answer and Xray can flush.
type headerConn struct {
	net.Conn
	once sync.Once
	err  error
}

// NetConn lets Vision walk out to the record gate under REALITY.
func (h *headerConn) NetConn() net.Conn { return h.Conn }

func (h *headerConn) Read(p []byte) (int, error) {
	h.once.Do(func() {
		_ = h.Conn.SetReadDeadline(time.Now().Add(15 * time.Second))
		h.err = readResponse(h.Conn)
		_ = h.Conn.SetReadDeadline(time.Time{})
	})
	if h.err != nil {
		return 0, h.err
	}
	return h.Conn.Read(p)
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
	// Vision command direct on the downlink is a raw copy on this socket.
	// The gate returns one TLS record per Read so that copy is not swallowed
	// by uTLS. security=none has no outer TLS, and the gate would treat the
	// VLESS bytes as records.
	if c.ep.Vision && (c.ep.Reality || c.ep.TLS) {
		raw = xtls.NewRecordGate(raw)
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
		conn, err = xtls.DialTLS(raw, c.ep.SNI, c.ep.Fingerprint, c.alpn(), c.insecure)
		if err != nil {
			return nil, err
		}
	}
	_ = conn.SetDeadline(time.Time{})
	if c.ep.xhttp() {
		c.stage("xhttp")
		return xtls.DialXHTTP(conn, c.ep.xhttpCfg())
	}
	return conn, nil
}

// alpn is the TLS NextProtos list: the share-link alpn= field, else h2
// when XHTTP runs over the TLS connection.
func (c *Client) alpn() []string {
	if list := xtls.SplitALPN(c.ep.ALPN); len(list) > 0 {
		return list
	}
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

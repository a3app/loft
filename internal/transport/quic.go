package transport

// QUIC mux front: first client-initiated bidi stream, length-prefixed like h2.

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/quic-go/quic-go"

	"github.com/a3app/loft/internal/config"
	ecrypto "github.com/a3app/loft/internal/crypto"
	"github.com/a3app/loft/internal/mux"
	"github.com/a3app/loft/internal/protect"
	"github.com/a3app/loft/internal/wire"
)

// quicALPN is the QUIC TLS ALPN. Default h3 so the Initial is not a unique
// "loft" token. Raw mux still runs on the first bidi stream when the h3 HTTP
// front is off. Set server quic_alpn: loft only for old clients.
const quicALPN = "h3"

// quicSess is the QUIC TLS session cache.
var quicSess = tls.NewLRUClientSessionCache(64)

// dialQUIC dials addr with a UDP socket that goes through protect (Android).
func dialQUIC(ctx context.Context, addr string, tc *tls.Config, qc *quic.Config) (*quic.Conn, error) {
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, err
	}
	pc, err := protect.ListenUDP4()
	if err != nil {
		return nil, err
	}
	conn, err := quic.Dial(ctx, pc, udpAddr, tc, qc)
	if err != nil {
		_ = pc.Close()
		return nil, err
	}
	return conn, nil
}

// quicCfg is the shared client and server QUIC config.
func quicCfg() *quic.Config {
	return &quic.Config{
		EnableDatagrams:      true,
		Allow0RTT:            true,
		KeepAlivePeriod:      15 * time.Second,
		MaxIdleTimeout:       120 * time.Second,
		HandshakeIdleTimeout: 10 * time.Second,
	}
}

// QUIC is the loss-tolerant front. Retransmit and path change are QUIC's.
type QUIC struct {
	cfg    config.Outbound
	token  []byte
	sess   *mux.Session
	conn   *quic.Conn
	stream *quic.Stream
	key    []byte
	ticket []byte
	done   chan struct{}
	dead   sync.Once
	offer  wire.Cap
}

// NewQUIC builds a QUIC client that has not yet dialed.
func NewQUIC(token string, o config.Outbound) *QUIC {
	return &QUIC{cfg: o, token: []byte(token), sess: clientHoldMux(), done: make(chan struct{})}
}

// Session returns the live mux session.
func (q *QUIC) Session() *mux.Session { return q.sess }

// Done is closed when the stream or mux dies.
func (q *QUIC) Done() <-chan struct{} { return q.done }

// die closes Done once.
func (q *QUIC) die() { q.dead.Do(func() { close(q.done) }) }

// Start dials QUIC, writes auth on the first bidi stream, and starts mux loops.
func (q *QUIC) Start(ctx context.Context) error {
	addr, sni, insecure, err := resolveTLS(q.cfg)
	if err != nil {
		return err
	}
	tc := &tls.Config{
		ServerName:         sni,
		InsecureSkipVerify: insecure,
		NextProtos:         []string{quicALPN},
		MinVersion:         tls.VersionTLS13,
		ClientSessionCache: quicSess,
	}
	conn, err := dialQUIC(ctx, addr, tc, quicCfg())
	if err != nil {
		return fmt.Errorf("quic dial: %w", err)
	}
	st, err := conn.OpenStreamSync(ctx)
	if err != nil {
		conn.CloseWithError(0, "stream")
		return err
	}
	hello := newClientHello(q.offer)
	var auth, nonce []byte
	if len(q.ticket) == 0 {
		q.ticket = CachedTicket()
	}
	if len(q.ticket) > 0 {
		auth = append(append([]byte{}, q.ticket...), hello.Encode()...)
		_, nonce, _, err = ecrypto.OpenTicket(q.token, q.ticket)
		if err != nil {
			q.ticket, auth = nil, nil
		}
	}
	if len(auth) == 0 {
		var proof []byte
		proof, nonce, err = ecrypto.MakeAuth(q.token)
		if err != nil {
			return err
		}
		auth = append(proof, hello.Encode()...)
	}
	key, err := ecrypto.DeriveKey(q.token, nonce)
	if err != nil {
		return err
	}
	if err := writeMsg(st, auth); err != nil {
		conn.CloseWithError(0, "auth")
		return fmt.Errorf("quic mux: %w", err)
	}
	raw, err := readMsg(st)
	if err != nil {
		conn.CloseWithError(0, "status")
		es := err.Error()
		if strings.Contains(es, "HEADERS") || strings.Contains(es, "0x105") {
			return fmt.Errorf("quic mux: node is HTTP/3 (use type h3)")
		}
		return err
	}
	plain, err := ecrypto.Open(key, raw, nil)
	if err != nil {
		return err
	}
	status, err := wire.DecodeStatus(plain)
	if err != nil {
		return err
	}
	if !status.Code.OK() {
		return wire.Fail(status.Code, status.Program)
	}
	RememberVis(status)
	q.sess.SetHold(status.Caps.Has(wire.CapHold))
	if len(status.Ticket) > 0 {
		q.ticket = status.Ticket
		RememberTicket(status.Ticket)
	}
	if err := adoptDecoy(addr, sni, status, true); err != nil {
		_ = conn.CloseWithError(0, "decoy")
		return err
	}
	q.conn, q.stream, q.key = conn, st, key
	go q.readLoop()
	go q.writeLoop()
	return nil
}

// Open starts the front if needed and opens a mux stream to target.
func (q *QUIC) Open(target string) (net.Conn, error) {
	if q.stream == nil {
		if err := q.Start(context.Background()); err != nil {
			return nil, err
		}
	}
	return q.sess.Open(target)
}

// Park drops QUIC without Leave or mux.Close.
func (q *QUIC) Park() error {
	parkClient(q.sess, "", nil, 0)
	q.die()
	if q.conn != nil {
		return q.conn.CloseWithError(0, "")
	}
	return nil
}

// Close sends CmdLeave and closes the QUIC connection.
func (q *QUIC) Close() error {
	if q.stream != nil && len(q.key) > 0 {
		if sealed, err := sealLeave(q.key, nil); err == nil {
			_ = q.stream.SetWriteDeadline(time.Now().Add(800 * time.Millisecond))
			_ = writeMsg(q.stream, sealed)
		}
	}
	clearClientHold()
	q.sess.Close()
	q.die()
	if q.conn != nil {
		return q.conn.CloseWithError(0, "bye")
	}
	return nil
}

// readLoop decrypts length-prefixed batches and pushes them into mux.
func (q *QUIC) readLoop() {
	for {
		raw, err := readMsg(q.stream)
		if err != nil {
			if q.sess.Holding() {
				q.die()
				return
			}
			q.sess.Close()
			q.die()
			return
		}
		plain, err := ecrypto.Open(q.key, raw, nil)
		if err != nil {
			continue
		}
		frames, err := wire.DecodeBatch(plain)
		if err != nil {
			continue
		}
		q.sess.Push(frames)
	}
}

// writeLoop Pulls mux, seals, and writes length-prefixed batches.
func (q *QUIC) writeLoop() {
	pumpWS(q.sess, time.Second, q.done, q.deadConn, func(frames []wire.Frame) ([]byte, error) {
		raw, err := wire.EncodeBatch(frames)
		if err != nil {
			return nil, err
		}
		return ecrypto.Seal(q.key, raw, nil)
	}, func(sealed []byte) error {
		return writeMsg(q.stream, sealed)
	})
	q.die()
}

func (q *QUIC) deadConn() bool {
	select {
	case <-q.done:
		return true
	default:
		return false
	}
}

// ListenQUIC serves the quic front on UDP (ALPN h3). Prefer ListenUDP when h3 CONNECT is also on.
func ListenQUIC(addr, certFile, keyFile, token string, decoy []string) error {
	info := NodeInfo{Token: []byte(token), Decoy: decoy, Caps: wire.NodeCaps}
	return ListenUDP(addr, certFile, keyFile, info, []string{wire.FrontQUIC})
}

// serveQUIC authenticates the first bidi stream and relays mux on it.
func serveQUIC(c *quic.Conn, seen *ecrypto.Nonces, info NodeInfo) {
	defer c.CloseWithError(0, "")
	ctx := c.Context()
	st, err := c.AcceptStream(ctx)
	if err != nil {
		return
	}
	raw, err := readMsg(st)
	if err != nil {
		return
	}
	token, nonce, rest, err := frontAuth(info.Token, raw, seen)
	if err != nil {
		return
	}
	key, err := ecrypto.DeriveKey(token, nonce)
	if err != nil {
		return
	}
	ms, reused := adoptServerMux(rest)
	status := info.Status("quic")
	finishStatus(&status, token, "quic", rest, ms, c.RemoteAddr())
	sealed, err := ecrypto.Seal(key, status.Encode(), nil)
	if err != nil {
		return
	}
	if err := writeMsg(st, sealed); err != nil {
		return
	}
	if !reused {
		go serveMux(ms)
		go serveDgrams(ms)
	}
	go pumpMux(ms, st, key, ctx.Done())
	for {
		raw, err := readMsg(st)
		if err != nil {
			dropServerMux(ms)
			return
		}
		plain, err := ecrypto.Open(key, raw, nil)
		if err != nil {
			continue
		}
		frames, err := wire.DecodeBatch(plain)
		if err != nil {
			continue
		}
		ms.Push(frames)
	}
}

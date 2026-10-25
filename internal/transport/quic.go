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
// The Initial carries the loft mark (quicmark.go) so a decoy node routes it
// to loft: v3 when mask_pub for addr is known, else v2 under token.
func dialQUIC(ctx context.Context, addr string, token []byte, tc *tls.Config, qc *quic.Config) (*quic.Conn, error) {
	udpAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return nil, err
	}
	random, scid, err := newQUICMark(token, CachedMaskPub(addr))
	if err != nil {
		return nil, err
	}
	pc, err := protect.ListenUDP4()
	if err != nil {
		return nil, err
	}
	// Wildcard UDP can leave the GET/TCP egress (VPN). Bind the same IPv4.
	if ua, ok := pc.LocalAddr().(*net.UDPAddr); ok && (ua.IP == nil || ua.IP.IsUnspecified()) {
		if ip := net.ParseIP(physicalAddr()); ip != nil && ip.To4() != nil {
			_ = pc.Close()
			pc, err = net.ListenUDP("udp4", &net.UDPAddr{IP: ip.To4()})
			if err != nil {
				return nil, err
			}
			protect.Apply(pc)
		}
	}
	tc = tc.Clone()
	tc.Rand = &markRand{first: random}
	tr := &quic.Transport{Conn: pc, ConnectionIDGenerator: &markCIDs{first: scid}}
	conn, err := tr.Dial(ctx, udpAddr, tc, qc)
	if err != nil {
		_ = tr.Close()
		_ = pc.Close()
		return nil, err
	}
	go func() {
		<-conn.Context().Done()
		_ = tr.Close()
		_ = pc.Close()
	}()
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

// QUIC is the loss-tolerant front. Retransmit is QUIC's. Path change still
// Parks: one UDP 4-tuple, no CID migration onto a new socket.
type QUIC struct {
	cfg     config.Outbound
	token   []byte
	sess    *mux.Session
	conn    *quic.Conn
	stream  *quic.Stream
	key     []byte
	holdGen uint64
	done    chan struct{}
	dead    sync.Once
	offer   wire.Cap
	lanes   *GET
	gate    pullGate
}

// NewQUIC builds a QUIC client that has not yet dialed.
func NewQUIC(token string, o config.Outbound) *QUIC {
	sess, gen := takeClientHold()
	return &QUIC{cfg: o, token: []byte(token), sess: sess, holdGen: gen, done: make(chan struct{})}
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
	conn, err := dialQUIC(ctx, addr, q.token, tc, quicCfg())
	if err != nil {
		return fmt.Errorf("quic dial: %w", err)
	}
	st, err := conn.OpenStreamSync(ctx)
	if err != nil {
		conn.CloseWithError(0, "stream")
		return err
	}
	hello := newClientHello(q.offer)
	proof, nonce, sk, err := ecrypto.MakeAuth(q.token)
	if err != nil {
		conn.CloseWithError(0, "auth")
		return err
	}
	auth := append(proof, hello.Encode()...)
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
	key, plain, err := ecrypto.OpenHello(sk, nonce, raw, nil)
	if err != nil {
		conn.CloseWithError(0, "status")
		return err
	}
	status, err := wire.DecodeStatus(plain)
	if err != nil {
		conn.CloseWithError(0, "status")
		return err
	}
	if !status.Code.OK() {
		// The node already admitted this conn and is blocked in its read
		// loop. Leaving it open holds the member until idle timeout, so the
		// next front cannot take the session.
		conn.CloseWithError(0, "status")
		return wire.Fail(status.Code, status.Program)
	}
	RememberVis(status)
	q.sess.SetHold(status.Caps.Has(wire.CapHold))
	if err := adoptDecoy(addr, sni, status, true); err != nil {
		_ = conn.CloseWithError(0, "decoy")
		return err
	}
	q.conn, q.stream, q.key = conn, st, key
	noteQUICKEX(conn)
	q.lanes = startBulkLanes(ctx, wire.FrontQUIC, q.cfg, q.token, key, status, q.offer, q.sess, q.done, func(b *GET) {
		q.gate.handover()
		b.adoptLane(noopCloser{}, func(p []byte) error { return writeMsg(st, p) }, wire.FrontQUIC)
	}, func() { q.gate.resume() })
	go q.readLoop()
	go q.writeLoop()
	return nil
}

// noteQUICKEX records the TLS group quic-go actually negotiated.
// quic and h3 do not go through the mask stack, so without this the debug
// line stays "kex=-" after a successful UDP handshake.
func noteQUICKEX(conn *quic.Conn) {
	if conn == nil {
		return
	}
	id := uint16(conn.ConnectionState().TLS.CurveID)
	if id == 0 {
		noteKEX("tls")
		return
	}
	noteKEX(kexName(id))
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
	if q.lanes != nil {
		q.lanes.closeAttach()
	}
	parkClient(q.sess, "", nil, 0)
	q.die()
	if q.conn != nil {
		return q.conn.CloseWithError(0, "")
	}
	return nil
}

// Close sends CmdLeave and closes the QUIC connection.
func (q *QUIC) Close() error {
	if q.lanes != nil {
		q.lanes.closeAttach()
	}
	if holdOwner(q.holdGen) && q.stream != nil && len(q.key) > 0 {
		if sealed, err := sealLeave(q.key, nil); err == nil {
			_ = q.stream.SetWriteDeadline(time.Now().Add(800 * time.Millisecond))
			_ = writeMsg(q.stream, sealed)
		}
	}
	// Hold keeps this mux. The server parks it when the QUIC conn drops
	// (Leave is often lost). Closing the client mux here made the next
	// front start stream ids at 1 and read the parked server stream.
	if q.sess != nil && q.sess.Holding() {
		parkClient(q.sess, "", nil, 0)
	} else if releaseClientHold(q.holdGen) {
		q.sess.Close()
	}
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
	}, &q.gate)
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
	st, err := c.AcceptStream(c.Context())
	if err != nil {
		c.CloseWithError(0, "")
		return
	}
	serveQUICStream(c, st, seen, info)
}

// serveQUICStream is serveQUIC on an already accepted first stream.
func serveQUICStream(c *quic.Conn, st *quic.Stream, seen *ecrypto.Nonces, info NodeInfo) {
	defer c.CloseWithError(0, "")
	ctx := c.Context()
	raw, err := readMsg(st)
	if err != nil {
		return
	}
	if ecrypto.LooksLikeAttach(raw) {
		serveAttach(st, raw, wire.FrontQUIC)
		return
	}
	a, err := frontAuth(info.Token, raw, seen)
	if err != nil || a.Replay {
		return
	}
	ms, reused := adoptServerMux(a.Rest, a.Token)
	status := info.Status("quic")
	finishStatus(&status, a.Token, "quic", a.Rest, ms, c.RemoteAddr())
	key := a.Key
	gate := &pullGate{}
	ss := bindTicket(ms, key, wire.FrontQUIC, func(b []byte) error { return writeMsg(st, b) }, gate)
	if ss != nil {
		status.Ticket = []byte(ss.id)
		defer ss.releaseTicket()
	}
	sealed, err := ecrypto.Seal(key, status.Encode(), nil)
	if err != nil {
		return
	}
	if err := writeMsg(st, ecrypto.WrapHello(a.EphN, sealed)); err != nil {
		return
	}
	if !reused {
		go serveMux(ms)
		go serveDgrams(ms)
	}
	go pumpMux(ms, st, key, ctx.Done(), gate)
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

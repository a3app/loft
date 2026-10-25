package transport

// Length-prefixed auth, AEAD(Status), and AEAD(mux) over a byte stream.

import (
	"context"
	"io"
	"net"
	"sync"
	"time"

	"github.com/a3app/loft/internal/config"
	ecrypto "github.com/a3app/loft/internal/crypto"
	"github.com/a3app/loft/internal/mux"
	"github.com/a3app/loft/internal/wire"
)

// rawSess runs mux over a bidirectional byte stream (h2 CONNECT, h3 CONNECT).
type rawSess struct {
	conn    net.Conn
	sess    *mux.Session
	key     []byte
	holdGen uint64
	done    chan struct{}
	dead    sync.Once
	lanes   *GET
	gate    pullGate
}

// Session returns the live mux session.
func (r *rawSess) Session() *mux.Session { return r.sess }

// Done is closed when the stream or mux dies.
func (r *rawSess) Done() <-chan struct{} { return r.done }

// die closes Done once.
func (r *rawSess) die() { r.dead.Do(func() { close(r.done) }) }

// Open opens a mux stream to target.
func (r *rawSess) Open(target string) (net.Conn, error) {
	return r.sess.Open(target)
}

// Park drops the stream without Leave or mux.Close.
func (r *rawSess) Park() error {
	if r.lanes != nil {
		r.lanes.closeAttach()
	}
	parkClient(r.sess, "", nil, 0)
	r.die()
	if r.conn != nil {
		return r.conn.Close()
	}
	return nil
}

// Close sends CmdLeave, then closes mux and the stream.
func (r *rawSess) Close() error {
	if r.lanes != nil {
		r.lanes.closeAttach()
	}
	if holdOwner(r.holdGen) && r.conn != nil && len(r.key) > 0 {
		if sealed, err := sealLeave(r.key, nil); err == nil {
			_ = r.conn.SetWriteDeadline(time.Now().Add(800 * time.Millisecond))
			_ = writeMsg(r.conn, sealed)
			_ = r.conn.SetWriteDeadline(time.Time{})
		}
	}
	// Same as QUIC: a held mux stays parked so the next front keeps stream
	// ids. Closing it here aliased a fresh client stream onto a parked one.
	if r.sess != nil && r.sess.Holding() {
		parkClient(r.sess, "", nil, 0)
	} else if releaseClientHold(r.holdGen) {
		r.sess.Close()
	}
	r.die()
	if r.conn != nil {
		return r.conn.Close()
	}
	return nil
}

// startRaw writes auth, reads AEAD(Status), and starts mux loops.
func startRaw(ctx context.Context, conn net.Conn, token []byte, offer wire.Cap, addr, sni string, cfg config.Outbound, front string) (*rawSess, error) {
	hello := newClientHello(offer)
	proof, nonce, sk, err := ecrypto.MakeAuth(token)
	if err != nil {
		conn.Close()
		return nil, err
	}
	auth := append(proof, hello.Encode()...)
	if err := writeMsg(conn, auth); err != nil {
		conn.Close()
		return nil, err
	}
	raw, err := readMsg(conn)
	if err != nil {
		conn.Close()
		return nil, afterProof(err)
	}
	key, plain, err := ecrypto.OpenHello(sk, nonce, raw, nil)
	if err != nil {
		conn.Close()
		return nil, err
	}
	st, err := wire.DecodeStatus(plain)
	if err != nil {
		conn.Close()
		return nil, err
	}
	if !st.Code.OK() {
		conn.Close()
		return nil, wire.Fail(st.Code, st.Program)
	}
	RememberVis(st)
	if addr == "" {
		addr = conn.RemoteAddr().String()
	}
	if err := adoptDecoy(addr, sni, st, true); err != nil {
		conn.Close()
		return nil, err
	}
	sess, gen := takeClientHold()
	sess.SetHold(st.Caps.Has(wire.CapHold))
	takeInner(front, key, st)
	s := &rawSess{conn: conn, sess: sess, key: key, holdGen: gen, done: make(chan struct{})}
	s.lanes = startBulkLanes(ctx, front, cfg, token, key, st, offer, sess, s.done, func(b *GET) {
		s.gate.handover()
		b.adoptLane(noopCloser{}, func(p []byte) error { return writeMsg(conn, p) }, front)
	}, func() { s.gate.resume() })
	go s.readLoop()
	go s.writeLoop()
	return s, nil
}

// readLoop decrypts length-prefixed batches and pushes them into mux.
func (r *rawSess) readLoop() {
	for {
		raw, err := readMsg(r.conn)
		if err != nil {
			if r.sess.Holding() {
				r.die()
				return
			}
			r.sess.Close()
			r.die()
			return
		}
		plain, err := ecrypto.Open(r.key, raw, nil)
		if err != nil {
			continue
		}
		ingestPlain(r.sess, plain)
	}
}

// writeLoop Pulls mux, seals, and writes length-prefixed batches.
func (r *rawSess) writeLoop() {
	pumpWS(r.sess, time.Second, r.done, r.deadConn, func(frames []wire.Frame) ([]byte, error) {
		raw, err := wire.EncodeBatch(frames)
		if err != nil {
			return nil, err
		}
		return ecrypto.Seal(r.key, raw, nil)
	}, func(sealed []byte) error {
		return writeMsg(r.conn, sealed)
	}, &r.gate)
	r.die()
}

func (r *rawSess) deadConn() bool {
	select {
	case <-r.done:
		return true
	default:
		return false
	}
}

// pumpMux is the origin write path for h2/h3/quic: prefetch like WSS so
// seal overlaps the previous write instead of Pull(48KiB, 200ms) one-at-a-time.
func pumpMux(ms *mux.Session, conn io.Writer, key []byte, done <-chan struct{}, gate *pullGate) {
	pumpWS(ms, 4*time.Millisecond, done, func() bool {
		select {
		case <-done:
			return true
		default:
			return false
		}
	}, func(frames []wire.Frame) ([]byte, error) {
		raw, err := wire.EncodeBatch(frames)
		if err != nil {
			return nil, err
		}
		return ecrypto.Seal(key, raw, nil)
	}, func(sealed []byte) error {
		return writeMsg(conn, sealed)
	}, gate)
}

// serveRaw authenticates, writes AEAD(Status), and relays mux on conn.
func serveRaw(conn net.Conn, info NodeInfo, seen *ecrypto.Nonces) {
	serveRawAs(conn, info, seen, wire.FrontH2)
}

// serveRawAs is serveRaw tagged with the front that accepted conn (h2 or h3).
func serveRawAs(conn net.Conn, info NodeInfo, seen *ecrypto.Nonces, front string) {
	defer conn.Close()
	raw, err := readMsg(conn)
	if err != nil {
		return
	}
	if front == "" {
		front = wire.FrontH2
	}
	if ecrypto.LooksLikeAttach(raw) {
		serveAttach(conn, raw, front)
		return
	}
	if seen == nil {
		seen = ecrypto.NewNonces()
	}
	a, err := frontAuth(info.Token, raw, seen)
	if err != nil || a.Replay {
		return
	}
	h, _ := wire.DecodeHello(a.Rest)
	if h.Caps.Has(wire.CapL3) && info.Caps.Has(wire.CapL3) {
		serveL3(conn, info, a)
		return
	}
	if !info.Caps.Has(wire.CapH2) && !info.Caps.Has(wire.CapH3) {
		return
	}
	ms, reused := adoptServerMux(a.Rest, a.Token)
	st := info.Status(front)
	finishStatus(&st, a.Token, front, a.Rest, ms, conn.RemoteAddr())
	key := a.Key
	gate := &pullGate{}
	ss := bindTicket(ms, key, front, func(b []byte) error { return writeMsg(conn, b) }, gate)
	if ss != nil {
		st.Ticket = []byte(ss.id)
		defer ss.releaseTicket()
	}
	applyInner(&st, front, a.Rest)
	sealed, err := ecrypto.Seal(key, st.Encode(), nil)
	if err != nil {
		return
	}
	commitInner(key, st)
	if err := writeMsg(conn, ecrypto.WrapHello(a.EphN, sealed)); err != nil {
		return
	}
	if !reused {
		go serveMux(ms)
		go serveDgrams(ms)
	}
	done := make(chan struct{})
	defer close(done)
	go pumpMux(ms, conn, key, done, gate)
	for {
		raw, err := readMsg(conn)
		if err != nil {
			dropServerMux(ms)
			return
		}
		plain, err := ecrypto.Open(key, raw, nil)
		if err != nil {
			continue
		}
		ingestPlain(ms, plain)
	}
}

package transport

// Length-prefixed auth, AEAD(Status), and AEAD(mux) over a byte stream.

import (
	"context"
	"io"
	"net"
	"sync"
	"time"

	ecrypto "github.com/a3app/loft/internal/crypto"
	"github.com/a3app/loft/internal/mux"
	"github.com/a3app/loft/internal/wire"
)

// rawSess runs mux over a bidirectional byte stream (h2 CONNECT, h3 CONNECT).
type rawSess struct {
	conn   net.Conn
	sess   *mux.Session
	key    []byte
	ticket []byte
	done   chan struct{}
	dead   sync.Once
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
	parkClient(r.sess, "", nil, 0)
	r.die()
	if r.conn != nil {
		return r.conn.Close()
	}
	return nil
}

// Close sends CmdLeave, then closes mux and the stream.
func (r *rawSess) Close() error {
	if r.conn != nil && len(r.key) > 0 {
		if sealed, err := sealLeave(r.key, nil); err == nil {
			_ = r.conn.SetWriteDeadline(time.Now().Add(800 * time.Millisecond))
			_ = writeMsg(r.conn, sealed)
			_ = r.conn.SetWriteDeadline(time.Time{})
		}
	}
	clearClientHold()
	r.sess.Close()
	r.die()
	if r.conn != nil {
		return r.conn.Close()
	}
	return nil
}

// startRaw writes auth, reads AEAD(Status), and starts mux loops.
func startRaw(ctx context.Context, conn net.Conn, token []byte, offer wire.Cap, addr, sni string) (*rawSess, error) {
	hello := newClientHello(offer)
	var auth, nonce []byte
	var err error
	ticket := CachedTicket()
	if len(ticket) > 0 {
		auth = append(append([]byte{}, ticket...), hello.Encode()...)
		_, nonce, _, err = ecrypto.OpenTicket(token, ticket)
		if err != nil {
			auth, nonce = nil, nil
		}
	}
	if len(auth) == 0 {
		var proof []byte
		proof, nonce, err = ecrypto.MakeAuth(token)
		if err != nil {
			conn.Close()
			return nil, err
		}
		auth = append(proof, hello.Encode()...)
	}
	key, err := ecrypto.DeriveKey(token, nonce)
	if err != nil {
		conn.Close()
		return nil, err
	}
	if err := writeMsg(conn, auth); err != nil {
		conn.Close()
		return nil, err
	}
	raw, err := readMsg(conn)
	if err != nil {
		conn.Close()
		return nil, err
	}
	plain, err := ecrypto.Open(key, raw, nil)
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
	if len(st.Ticket) > 0 {
		RememberTicket(st.Ticket)
	}
	sess := clientHoldMux()
	sess.SetHold(st.Caps.Has(wire.CapHold))
	s := &rawSess{conn: conn, sess: sess, key: key, ticket: st.Ticket, done: make(chan struct{})}
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
		frames, err := wire.DecodeBatch(plain)
		if err != nil {
			continue
		}
		r.sess.Push(frames)
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
	})
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
func pumpMux(ms *mux.Session, conn io.Writer, key []byte, done <-chan struct{}) {
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
	})
}

// serveRaw authenticates, writes AEAD(Status), and relays mux on conn.
func serveRaw(conn net.Conn, info NodeInfo, seen *ecrypto.Nonces) {
	defer conn.Close()
	raw, err := readMsg(conn)
	if err != nil {
		return
	}
	if seen == nil {
		seen = ecrypto.NewNonces()
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
	st := info.Status("h2")
	finishStatus(&st, token, "h2", rest, ms, conn.RemoteAddr())
	sealed, err := ecrypto.Seal(key, st.Encode(), nil)
	if err != nil {
		return
	}
	if err := writeMsg(conn, sealed); err != nil {
		return
	}
	if !reused {
		go serveMux(ms)
		go serveDgrams(ms)
	}
	done := make(chan struct{})
	defer close(done)
	go pumpMux(ms, conn, key, done)
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
		frames, err := wire.DecodeBatch(plain)
		if err != nil {
			continue
		}
		ms.Push(frames)
	}
}

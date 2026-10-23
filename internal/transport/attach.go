package transport

// 0x04 attach: a second front joins a live GET session without a new 0x02.
// GET stays up. Bulk mux runs on the attached front until it dies, then GET.

import (
	"io"
	"sync"
	"time"

	ecrypto "github.com/a3app/loft/internal/crypto"
	"github.com/a3app/loft/internal/mux"
	"github.com/a3app/loft/internal/wire"
)

type attachLink struct {
	front string
	conn  io.Closer
	stop  func()
	done  <-chan struct{}
}

func (ss *serverSess) setAttach(front string, conn io.Closer, stop func(), done <-chan struct{}) *attachLink {
	link := &attachLink{front: front, conn: conn, stop: stop, done: done}
	ss.mu.Lock()
	old := ss.att
	ss.att = link
	ss.bulk = front
	ss.mu.Unlock()
	stopAttach(old)
	if ss.cond != nil {
		ss.cond.Broadcast()
	}
	return link
}

func stopAttach(att *attachLink) {
	if att == nil {
		return
	}
	if att.stop != nil {
		att.stop()
	}
	if att.conn != nil {
		_ = att.conn.Close()
	}
	if att.done != nil {
		<-att.done
	}
}

func (ss *serverSess) clearAttach(link *attachLink) {
	ss.mu.Lock()
	if ss.att == link {
		ss.att = nil
		ss.bulk = wire.FrontGET
	}
	ss.mu.Unlock()
	if ss.cond != nil {
		ss.cond.Broadcast()
	}
}

func (ss *serverSess) bulkFront() string {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	if ss.bulk == "" {
		return wire.FrontGET
	}
	return ss.bulk
}

func (ss *serverSess) attachPulls() bool {
	return ss.bulkFront() != wire.FrontGET
}

func (ss *serverSess) closeAttach() {
	ss.mu.Lock()
	att := ss.att
	ss.mu.Unlock()
	stopAttach(att)
}

func (ss *serverSess) takeAttachNonce(nonce []byte) error {
	ss.mu.Lock()
	if ss.seen == nil {
		ss.seen = ecrypto.NewNonces()
	}
	n := ss.seen
	ss.mu.Unlock()
	return n.TakeAttach(nonce)
}

func lookupTicket(sid string) *serverSess {
	if v, ok := ticketTab.Load(sid); ok {
		return v.(*serverSess)
	}
	return nil
}

// serveAttach binds conn to the GET session in blob (0x04).
func serveAttach(conn io.ReadWriteCloser, blob []byte, front string) {
	defer conn.Close()
	sid, gotFront, _, err := ecrypto.ParseAttach(blob)
	if err != nil || gotFront != front {
		return
	}
	ss := lookupTicket(sid)
	if ss == nil || len(ss.key) == 0 || ss.mux == nil || ss.mux.Closed() {
		return
	}
	if _, _, nonce, err := ecrypto.FinishAttach(ss.key, blob); err != nil {
		return
	} else if err := ss.takeAttachNonce(nonce); err != nil {
		return
	}
	ack, err := wire.EncodeBatch([]wire.Frame{wire.FrontFrame(front)})
	if err != nil {
		return
	}
	sealed, err := ecrypto.Seal(ss.key, ack, nil)
	if err != nil {
		return
	}
	if err := writeAttach(conn, sealed); err != nil {
		return
	}
	done := make(chan struct{})
	pumpDone := make(chan struct{})
	var once sync.Once
	stop := func() { once.Do(func() { close(done) }) }
	link := ss.setAttach(front, conn, stop, pumpDone)
	go func() {
		defer close(pumpDone)
		pumpAttach(ss.mux, func(b []byte) error { return writeAttach(conn, b) }, ss.key, done)
	}()
	defer func() {
		stop()
		_ = conn.Close()
		<-pumpDone
		ss.clearAttach(link)
	}()
	for {
		select {
		case <-done:
			return
		default:
		}
		raw, err := readAttach(conn)
		if err != nil {
			return
		}
		plain, err := ecrypto.Open(ss.key, raw, nil)
		if err != nil {
			continue
		}
		frames, err := wire.DecodeBatch(plain)
		if err != nil {
			continue
		}
		frames, bulk := wire.SplitFront(frames)
		if bulk == wire.FrontGET {
			ss.closeAttach()
			return
		}
		if len(frames) > 0 {
			ss.mux.Push(frames)
		}
		if ss.mux.Closed() {
			return
		}
	}
}

func pumpAttach(ms *mux.Session, write func([]byte) error, key []byte, done <-chan struct{}) {
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
	}, write)
}

func writeAttach(w io.Writer, b []byte) error {
	if ws, ok := w.(wsBinary); ok {
		return ws.WriteBinary(b)
	}
	return writeMsg(w, b)
}

func readAttach(r io.Reader) ([]byte, error) {
	if ws, ok := r.(wsBinary); ok {
		return ws.ReadBinary()
	}
	return readMsg(r)
}

// wsBinary is one binary WebSocket message per AEAD blob.
type wsBinary interface {
	WriteBinary([]byte) error
	ReadBinary() ([]byte, error)
}

// wsConn adapts a websocket.Conn used as attach.
type wsAttach struct {
	write func([]byte) error
	read  func() ([]byte, error)
	close func() error
}

func (w wsAttach) Write(p []byte) (int, error) { return 0, w.WriteBinary(p) }
func (w wsAttach) WriteBinary(p []byte) error  { return w.write(p) }
func (w wsAttach) Read(p []byte) (int, error)  { return 0, io.ErrUnexpectedEOF }
func (w wsAttach) ReadBinary() ([]byte, error) { return w.read() }
func (w wsAttach) Close() error {
	if w.close != nil {
		return w.close()
	}
	return nil
}

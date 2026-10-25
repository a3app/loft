package transport

// 0x04 attach: a second front joins a live GET session without a new 0x02.
// GET stays up. Bulk mux runs on the attached front until it dies, then GET.

import (
	"io"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	ecrypto "github.com/a3app/loft/internal/crypto"
	"github.com/a3app/loft/internal/mux"
	"github.com/a3app/loft/internal/wire"
)

type attachLink struct {
	id    int
	front string
	conn  io.Closer
	write func([]byte) error
	stop  func()
	done  <-chan struct{}
	dead  atomic.Bool
}

func (ss *serverSess) setAttach(front string, conn io.Closer, write func([]byte) error, stop func(), done <-chan struct{}) *attachLink {
	ss.mu.Lock()
	ss.linkSeq++
	link := &attachLink{id: ss.linkSeq, front: front, conn: conn, write: write, stop: stop, done: done}
	ss.links = append(ss.links, link)
	ss.att = link
	ss.bulk = joinBulk(ss.links)
	start := !ss.pumping
	ss.pumping = true
	ss.mu.Unlock()
	if ss.cond != nil {
		ss.cond.Broadcast()
	}
	if start {
		go ss.dispatchAttach()
	}
	return link
}

func joinBulk(links []*attachLink) string {
	var b strings.Builder
	seen := map[string]bool{}
	for _, l := range links {
		if l == nil || l.front == "" || l.dead.Load() || seen[l.front] {
			continue
		}
		seen[l.front] = true
		if b.Len() > 0 {
			b.WriteByte('+')
		}
		b.WriteString(l.front)
	}
	if b.Len() == 0 {
		return wire.FrontGET
	}
	return b.String()
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
	var left []*attachLink
	for _, l := range ss.links {
		if l == nil || l == link {
			continue
		}
		left = append(left, l)
	}
	ss.links = left
	if len(left) == 0 {
		ss.att = nil
		ss.bulk = wire.FrontGET
	} else {
		ss.att = left[len(left)-1]
		ss.bulk = joinBulk(left)
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
	links := ss.links
	ss.links = nil
	ss.att = nil
	ss.bulk = wire.FrontGET
	ss.mu.Unlock()
	for _, l := range links {
		if l == nil {
			continue
		}
		l.dead.Store(true)
		stopAttach(l)
	}
	if ss.cond != nil {
		ss.cond.Broadcast()
	}
}

func (ss *serverSess) fanLanes() []fanLane {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	var out []fanLane
	for _, l := range ss.links {
		if l == nil || l.write == nil || l.dead.Load() {
			continue
		}
		out = append(out, fanLane{id: l.id, dead: &l.dead, write: l.write})
	}
	return out
}

// dispatchAttach is the only server Puller for attached fronts. One stream
// stays on one front. GET resumes when the last link is gone.
func (ss *serverSess) dispatchAttach() {
	fan := &attachFan{key: append([]byte(nil), ss.key...), bind: map[uint32]int{}}
	for {
		if ss.mux == nil || ss.mux.Closed() {
			return
		}
		lanes := ss.fanLanes()
		if len(lanes) == 0 {
			ss.mu.Lock()
			if ss.liveLinkCount() == 0 {
				ss.pumping = false
				ss.mu.Unlock()
				return
			}
			ss.mu.Unlock()
			continue
		}
		max, wait := wsPullBytes, 4*time.Millisecond
		if ss.mux.PriQueued() > 0 {
			max = wsPriBytes
			wait = 0
		}
		var frames []wire.Frame
		if DraftPaused() {
			frames = ss.mux.PullPri(max, wait)
		} else {
			frames = ss.mux.Pull(max, wait)
		}
		if len(frames) == 0 {
			continue
		}
		fan.send(lanes, frames)
	}
}

func (ss *serverSess) liveLinkCount() int {
	n := 0
	for _, l := range ss.links {
		if l != nil && !l.dead.Load() {
			n++
		}
	}
	return n
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
	var once sync.Once
	stop := func() { once.Do(func() { close(done) }) }
	link := ss.setAttach(front, conn, func(b []byte) error { return writeAttach(conn, b) }, stop, done)
	defer func() {
		link.dead.Store(true)
		stop()
		_ = conn.Close()
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

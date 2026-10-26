package transport

// 0x04 attach: a second front joins the live session without a new 0x02.
// The front that finished 0x02 keeps the mux. Other fronts are lanes on that
// sid. One dispatcher Pulls. When the owner is GET, bulk leaves GET (the CDN
// cookie path) and rides the lanes. When the owner is ws/h2/quic/h3, that
// socket stays a lane too.
//
// Words used here. The ticket (sid) is the session id the node sends in
// Status; a 0x04 message names it and proves the session key, so no new
// handshake is needed. A lane (attachLink on the node, attachLane on the
// client) is one attached socket. "Bulk" is the set of fronts that carry
// mux traffic right now, written like "ws+h2"; plain "get" means no lanes.
// This file is the node side; attach_client.go is the client side.

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

// attachLink is one socket attached to a node session. write sends one
// sealed batch on it; stop and done end its read loop. dead is set once the
// socket is gone so the dispatcher stops using it.
type attachLink struct {
	id    int
	front string
	conn  io.Closer
	write func([]byte) error
	stop  func()
	done  <-chan struct{}
	dead  atomic.Bool
}

// setAttach adds a lane to the session and starts the dispatcher if it is
// not running yet. From then on the dispatcher, not the GET handler, Pulls
// the mux.
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

// joinBulk names the live lanes' fronts once each, joined with "+"
// ("ws+h2"). No live lane gives "get".
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

// stopAttach closes one lane and waits for its read loop to finish.
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

// clearAttach removes one lane after its socket ended. The other lanes
// keep going; with none left, bulk falls back to GET.
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

// attachPulls is true while lanes carry the mux. The GET handler then only
// feeds uplink and answers with a Report, and does not Pull downlink itself.
func (ss *serverSess) attachPulls() bool {
	return ss.bulkFront() != wire.FrontGET
}

// closeAttach drops every lane and gives the mux back to its owner: GET
// Pulls again, and a ws/h2/quic/h3 owner's write loop resumes.
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
	ss.ownerOn.Store(false)
	if ss.gate != nil {
		ss.gate.resume()
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
		max, wait := standPull(), 4*time.Millisecond
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

// takeAttachNonce records the nonce of a 0x04 message and fails on a
// repeat, so a recorded attach message cannot be replayed.
func (ss *serverSess) takeAttachNonce(nonce []byte) error {
	ss.mu.Lock()
	if ss.seen == nil {
		ss.seen = ecrypto.NewNonces()
	}
	n := ss.seen
	ss.mu.Unlock()
	return n.TakeAttach(nonce)
}

// lookupTicket finds the live session a 0x04 names, on any front.
func lookupTicket(sid string) *serverSess {
	if v, ok := ticketTab.Load(sid); ok {
		return v.(*serverSess)
	}
	return nil
}

// muxTickets is the live ticket for a mux. A new 0x02 on the same mux
// retires the previous sid so lanes sealed under the old key die.
var muxTickets sync.Map // *mux.Session → *serverSess

func rememberMuxTicket(ss *serverSess) {
	if ss == nil || ss.mux == nil {
		return
	}
	muxTickets.Store(ss.mux, ss)
}

func forgetMuxTicket(ss *serverSess) {
	if ss == nil || ss.mux == nil {
		return
	}
	if cur, ok := muxTickets.Load(ss.mux); ok && cur == ss {
		muxTickets.Delete(ss.mux)
	}
}

// noopCloser stands in for a lane socket that must not be closed when the
// lane ends, such as the owner socket, which the owner front still holds.
type noopCloser struct{}

func (noopCloser) Close() error { return nil }

// bindTicket registers mux under a fresh 24-hex sid and this 0x02 key.
// Status.Ticket carries the sid. 0x04 on any other front looks it up.
func bindTicket(ms *mux.Session, key []byte, front string, write func([]byte) error, gate *pullGate) *serverSess {
	if ms == nil || len(key) == 0 {
		return nil
	}
	if v, ok := muxTickets.Load(ms); ok {
		if old, _ := v.(*serverSess); old != nil {
			old.releaseTicket()
		}
	}
	ss := &serverSess{
		id:         ecrypto.RandomID(),
		key:        append([]byte(nil), key...),
		mux:        ms,
		held:       map[uint64][]wire.Frame{},
		skipPull:   map[uint64]struct{}{},
		replies:    map[uint64][]byte{},
		ownerFront: front,
		ownerWrite: write,
		gate:       gate,
		seen:       ecrypto.NewNonces(),
	}
	ss.cond = sync.NewCond(&ss.mu)
	ticketTab.Store(ss.id, ss)
	rememberMuxTicket(ss)
	return ss
}

// releaseTicket forgets the ticket and drops every lane. Safe to call more
// than once.
func (ss *serverSess) releaseTicket() {
	if ss == nil {
		return
	}
	ss.relOnce.Do(func() {
		ticketTab.Delete(ss.id)
		forgetMuxTicket(ss)
		ss.closeAttach()
	})
}

// armOwner pauses the owner write loop and puts that socket in the fan.
// GET has no ownerWrite: bulk stays off the CDN socket.
func (ss *serverSess) armOwner() {
	if ss == nil || ss.ownerWrite == nil || ss.ownerFront == "" || ss.ownerFront == wire.FrontGET {
		return
	}
	if ss.gate != nil {
		ss.gate.handover()
	}
	if !ss.ownerOn.CompareAndSwap(false, true) {
		return
	}
	ss.setAttach(ss.ownerFront, noopCloser{}, ss.ownerWrite, func() {}, nil)
}

// serveAttach binds conn to the GET session in blob (0x04).
// The node checks that the front named inside the blob is the one it
// arrived on, that the ticket is live, that the blob is sealed with the
// session key, and that its nonce is new. Any failure just closes conn.
// It then puts the owner socket in the fan (armOwner), answers with a
// sealed Front frame naming this front, and reads uplink batches until the
// socket ends. A Front frame naming "get" from the client means "stop using
// lanes": every lane closes and GET carries the mux again.
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
	// A clear session has one frame cipher. GET would show those frames to a CDN.
	if front == wire.FrontGET && ecrypto.InnerOf(ss.key) == wire.InnerClear {
		return
	}
	if _, _, nonce, err := ecrypto.FinishAttach(ss.key, blob); err != nil {
		return
	} else if err := ss.takeAttachNonce(nonce); err != nil {
		return
	}
	ss.armOwner()
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
		if isStripe(plain) {
			stripeIngest(ss.mux, plain[1:])
			if ss.mux.Closed() {
				return
			}
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

// pumpAttach Pulls ms onto one attached socket. Nothing calls it now:
// dispatchAttach does this job for all lanes at once.
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
	}, write, nil)
}

// writeAttach sends one sealed batch: a binary message on WebSocket, a
// length-prefixed message on every other front.
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

// wsAttach adapts a websocket.Conn used as attach. Plain Read is not used:
// messages come whole through ReadBinary.
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

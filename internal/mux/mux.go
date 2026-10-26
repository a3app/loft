// Package mux multiplexes streams and datagrams over AEAD-protected frames
// after Status. Client stream IDs are odd starting at 1; node IDs are even
// starting at 2. CmdLeave (stream 0) ends the session.
//
// This is the heart of loft/1.0. After the handshake (the client's 0x02
// message and the node's Status answer) both sides hold one Session. Every
// user connection (a browser TCP connection, a SOCKS5 request, an overlay
// flow) becomes a Stream inside it, and UDP/ICMP become datagrams. The
// Session turns all of that into wire.Frame values.
//
// The mux does not touch the network itself. A "front" (the way the frames
// are carried over HTTPS: GET requests, WebSocket, HTTP/2, QUIC, ...) calls
// Pull to take frames that are ready to send, seals them, and sends them.
// Frames that arrive from the peer are opened by the front and handed to
// Push. Because the front only pulls, the mux works the same on every front.
//
// Two separate odd/even ID ranges mean the client and the node can open
// streams at the same time without agreeing on numbers first.
package mux

import (
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/a3app/loft/internal/wire"
)

// ErrClosed is returned after Leave or Close.
var ErrClosed = errors.New("mux: closed")

// ErrCongested is a dropped datagram or control frame: the outbound queue
// is full. UDP/ICMP senders treat this as a silent loss.
var ErrCongested = errors.New("mux: congested")

// Session is one mux session: streams, datagrams, and an outbound frame queue.
//
// Outbound frames wait in two queues. pri holds control frames and small
// packets (Open, Ping, DNS, TCP ACK-sized Data); out holds bulk Data. Pull
// always drains pri first, so a large download on one stream cannot delay
// a new connection or an ACK on another stream.
type Session struct {
	mu        sync.Mutex
	streams   map[uint32]*Stream
	accept    chan *Stream    // streams opened by the peer, waiting for Accept
	out       chan wire.Frame // bulk Data waiting for Pull
	pri       chan wire.Frame // control and small frames; Pull takes these first
	dgrams    chan dgram      // inbound datagrams waiting for ReadDgram
	dgramFn   func(string, []byte) bool
	nextID    atomic.Uint32
	closed    atomic.Bool
	client    bool
	relay     atomic.Bool
	relayID   atomic.Pointer[string]
	bytesUp   atomic.Uint64
	bytesDown atomic.Uint64
	die       chan struct{} // closed by Close; wakes every waiter
	// hold: the node keeps ("parks") this mux alive for a short time after
	// the front drops, so the client can reconnect and get its streams back.
	hold     atomic.Bool
	instance string
	owner    string // member id from the authenticated key; park/adopt key
	// Data that arrived before Open (GET/HTTP2 reordering). Flushed on Open.
	pending  map[uint32][][]byte
	pendByte map[uint32]int
	pongs    map[[8]byte]chan struct{}
	pingSeq  atomic.Uint64
	rate     atomic.Pointer[bucket] // nil = unlimited; bulk Data/Dgram only
}

// dgram is one CmdDgram: Open-target address plus UDP payload.
type dgram struct {
	addr string
	data []byte
}

// NewClient allocates odd stream IDs starting at 1.
func NewClient() *Session { return newSession(true) }

// NewServer allocates even stream IDs starting at 2.
func NewServer() *Session { return newSession(false) }

// SetRelay marks a relay trunk (may Open hub:* and ctl:1) of relay id, the
// id its key was issued for. A relay is an extra entry node that forwards
// its members' traffic to the controller over one mux, the "trunk". On it
// the relay opens the service addresses ctl:1 (control channel) and hub:*
// (forwarded member traffic), which an ordinary member session may not open.
func (s *Session) SetRelay(id string) {
	s.relayID.Store(&id)
	s.relay.Store(true)
}

// ClearRelay drops the trunk mark. Does not Close streams or touch hold /
// instance_id.
func (s *Session) ClearRelay() {
	s.relay.Store(false)
	s.relayID.Store(nil)
}

// Relay reports a relay trunk session.
func (s *Session) Relay() bool { return s.relay.Load() }

// RelayID is the relay id of a trunk session, or "".
func (s *Session) RelayID() string {
	if p := s.relayID.Load(); p != nil && s.relay.Load() {
		return *p
	}
	return ""
}

// newSession is a client (odd IDs) or server (even IDs) mux.
func newSession(client bool) *Session {
	s := &Session{
		streams:  make(map[uint32]*Stream),
		accept:   make(chan *Stream, 64),
		out:      make(chan wire.Frame, 48),
		pri:      make(chan wire.Frame, 64),
		dgrams:   make(chan dgram, 64),
		pending:  make(map[uint32][][]byte),
		pendByte: make(map[uint32]int),
		die:      make(chan struct{}),
		client:   client,
	}
	if client {
		s.nextID.Store(1)
	} else {
		s.nextID.Store(2)
	}
	return s
}

// Open starts a stream with CmdOpen. target is host:port (overlay or Internet).
func (s *Session) Open(target string) (*Stream, error) {
	if s.closed.Load() {
		return nil, ErrClosed
	}
	id := s.nextID.Add(2) - 2
	st := newStream(s, id)
	st.target = target
	s.mu.Lock()
	s.streams[id] = st
	s.mu.Unlock()
	host, portStr, err := net.SplitHostPort(target)
	if err != nil {
		st.kill()
		return nil, err
	}
	if err := s.send(wire.Frame{Cmd: wire.CmdOpen, StreamID: id, Payload: EncodeTarget(host, wire.Port(portStr))}); err != nil {
		st.Close()
		return nil, err
	}
	return st, nil
}

// Accept waits for a peer CmdOpen.
func (s *Session) Accept() (*Stream, error) {
	st, ok := <-s.accept
	if !ok {
		return nil, ErrClosed
	}
	return st, nil
}

// Push delivers decoded frames from the AEAD plaintext. The front calls it
// after it has received and decrypted one batch of frames from the peer.
// Data for a full stream is deferred so a bulk download cannot stall
// Open/ICMP/small HTTP on another stream in the same batch.
// Push never blocks: a hog stream parks then Resets; the fabric stays live.
func (s *Session) Push(frames []wire.Frame) {
	var rest []wire.Frame
	for _, f := range frames {
		if s.closed.Load() {
			return
		}
		if f.Cmd == wire.CmdData && s.dataFull(f.StreamID) {
			rest = append(rest, f)
			continue
		}
		s.handle(f)
	}
	for _, f := range rest {
		if s.closed.Load() {
			return
		}
		s.handle(f)
	}
}

// dataFull is true when stream id cannot accept another Data without parking
// past the per-stream cap (ingest would have to block or Reset).
func (s *Session) dataFull(id uint32) bool {
	s.mu.Lock()
	st := s.streams[id]
	s.mu.Unlock()
	return st != nil && st.full()
}

// Closed reports Leave or Close.
func (s *Session) Closed() bool { return s.closed.Load() }

// Done is closed when the session is closed, for cleanup of per-session state.
func (s *Session) Done() <-chan struct{} { return s.die }

// SetHold marks streams to survive a brief front drop (cap hold). With hold
// on, the node "parks" the mux instead of closing it when the front goes
// away; a client that comes back with the same instance_id and member key
// adopts the parked mux and its inner TCP connections keep running.
func (s *Session) SetHold(on bool) { s.hold.Store(on) }

// Holding reports cap hold is on and the mux is still live.
func (s *Session) Holding() bool { return s.hold.Load() && !s.closed.Load() }

// SetInstance stores the Hello instance_id for mux park/resume. The
// instance_id is a persistent id of one client install; it is how the node
// recognises "the same" client after a reconnect.
func (s *Session) SetInstance(id string) {
	s.mu.Lock()
	s.instance = id
	s.mu.Unlock()
}

// Instance is the Hello instance_id, or empty.
func (s *Session) Instance() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.instance
}

// SetOwner stores the authenticated member id for mux park/resume.
func (s *Session) SetOwner(id string) {
	s.mu.Lock()
	s.owner = id
	s.mu.Unlock()
}

// Owner is the member id bound at handshake, or empty when the ring is empty.
func (s *Session) Owner() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.owner
}

// StreamCount is the number of live streams.
func (s *Session) StreamCount() int {
	s.mu.Lock()
	n := len(s.streams)
	s.mu.Unlock()
	return n
}

// Leave queues CmdLeave. The peer MUST close the mux session.
func (s *Session) Leave() error {
	return s.send(wire.Frame{Cmd: wire.CmdLeave})
}

// OutQueued is frames waiting for the front to send.
func (s *Session) OutQueued() int { return len(s.out) + len(s.pri) }

// PriQueued is control/small frames waiting ahead of bulk Data.
func (s *Session) PriQueued() int { return len(s.pri) }

// smallPri is Data that jumps the bulk queue: TCP ACKs, TLS records of a
// new request, Cloudflare loaded-ping HTTP. 12 KiB stream chunks stay on out.
const smallPri = 4 << 10

// dgramPri is CmdDgram that may jump bulk Data. DNS/ICMP/STUN fit.
// TUN default-route QUIC (~1200 B) must stay in out: GET Pull drains
// pri first, so Apple UDP otherwise starves TCP ACKs for the whole RTT.
const dgramPri = 512

// priFrame is Open/Ping/Pong/Leave, small DNS/ICMP datagrams, or small
// Data on a stream with no bulk waiting in out. Same-stream TLS order
// stays (queued>0 → out). A new Speedtest ping stream has queued=0 so
// its HTTP jumps download bulk.
func (s *Session) priFrame(f wire.Frame) bool {
	switch f.Cmd {
	case wire.CmdOpen, wire.CmdPing, wire.CmdPong, wire.CmdLeave, wire.CmdTune, wire.CmdFront:
		return true
	case wire.CmdDgram:
		return len(f.Payload) <= dgramPri
	case wire.CmdData:
		if len(f.Payload) > smallPri {
			return false
		}
		st := s.stream(f.StreamID)
		return st != nil && st.queued.Load() == 0
	default:
		return false
	}
}

// Pull drains outbound frames up to maxBytes (including headers). maxWait 0 does
// not use a timer so a queued frame is not missed. Control frames drain first.
//
// The front calls Pull when it is ready to send: for the GET front, frames
// go into the next request's Cookie, so maxBytes is that request's budget.
// The caller that is allowed to Pull at a given moment is called the
// Puller. On GET there is exactly one, so frames leave strictly in order.
// With maxWait > 0, Pull waits up to maxWait for the first frame and then
// takes whatever else is already queued without waiting more.
func (s *Session) Pull(maxBytes int, maxWait time.Duration) []wire.Frame {
	if maxBytes <= 0 {
		maxBytes = 1
	}
	var out []wire.Frame
	n := 0
	takePri := func(f wire.Frame) {
		out = append(out, f)
		n += wire.HeaderSize + len(f.Payload)
	}
	takeOut := func(f wire.Frame) {
		takePri(f)
		if f.Cmd == wire.CmdData {
			if st := s.stream(f.StreamID); st != nil && st.queued.Load() > 0 {
				st.queued.Add(-1)
			}
		}
	}
	drain := func() {
		for n < maxBytes {
			select {
			case f := <-s.pri:
				takePri(f)
				continue
			default:
			}
			select {
			case f := <-s.out:
				takeOut(f)
			default:
				return
			}
		}
	}
	// NewTimer(0) is already fired: select races the queued frame and
	// often returns empty while Data sits in out. Extra GET loops then
	// send pads, the node holds those 250ms, upload sticks at ~100 kbit/s.
	if maxWait <= 0 {
		drain()
		return out
	}
	timer := time.NewTimer(maxWait)
	defer timer.Stop()
	select {
	case f := <-s.pri:
		takePri(f)
	case f := <-s.out:
		takeOut(f)
	case <-timer.C:
		return out
	case <-s.die:
		return out
	}
	drain()
	return out
}

// PullPri drains only control/small frames (pri). Draft Data stays in out
// so a client pause schedule does not drop bulk. A front uses it when it
// may only send a little right now but must keep pings and new Opens moving.
func (s *Session) PullPri(maxBytes int, maxWait time.Duration) []wire.Frame {
	if maxBytes <= 0 {
		maxBytes = 1
	}
	var out []wire.Frame
	n := 0
	drain := func() {
		for n < maxBytes {
			select {
			case f := <-s.pri:
				out = append(out, f)
				n += wire.HeaderSize + len(f.Payload)
			default:
				return
			}
		}
	}
	if maxWait <= 0 {
		drain()
		return out
	}
	timer := time.NewTimer(maxWait)
	defer timer.Stop()
	select {
	case f := <-s.pri:
		out = append(out, f)
		n += wire.HeaderSize + len(f.Payload)
	case <-timer.C:
		return out
	case <-s.die:
		return out
	}
	drain()
	return out
}

// Bytes counts CmdData and CmdDgram payload on this session.
func (s *Session) Bytes() (up, down uint64) {
	return s.bytesUp.Load(), s.bytesDown.Load()
}

// Ping sends CmdPing and waits for CmdPong with the same 8-byte id.
func (s *Session) Ping(ctx context.Context) (time.Duration, error) {
	if s == nil || s.Closed() {
		return 0, ErrClosed
	}
	var id [8]byte
	binary.BigEndian.PutUint64(id[:], s.pingSeq.Add(1))
	ch := make(chan struct{}, 1)
	s.mu.Lock()
	if s.pongs == nil {
		s.pongs = map[[8]byte]chan struct{}{}
	}
	s.pongs[id] = ch
	s.mu.Unlock()
	defer func() {
		s.mu.Lock()
		delete(s.pongs, id)
		s.mu.Unlock()
	}()
	t0 := time.Now()
	if err := s.sendCtx(ctx, wire.Frame{Cmd: wire.CmdPing, Payload: id[:]}); err != nil {
		return time.Since(t0), err
	}
	select {
	case <-ctx.Done():
		return time.Since(t0), ctx.Err()
	case <-s.die:
		return time.Since(t0), ErrClosed
	case <-ch:
		return time.Since(t0), nil
	}
}

// notePong wakes the Ping call waiting for this 8-byte id. A Pong with an
// unknown id (a late answer to a Ping that already gave up) is ignored.
func (s *Session) notePong(payload []byte) {
	if len(payload) < 8 {
		return
	}
	var id [8]byte
	copy(id[:], payload[:8])
	s.mu.Lock()
	ch := s.pongs[id]
	if ch != nil {
		delete(s.pongs, id)
	}
	s.mu.Unlock()
	if ch != nil {
		select {
		case ch <- struct{}{}:
		default:
		}
	}
}

// noteTraffic adds Data/Dgram payload length to a counter.
func noteTraffic(f wire.Frame, ctr *atomic.Uint64) {
	switch f.Cmd {
	case wire.CmdData, wire.CmdDgram:
		ctr.Add(uint64(len(f.Payload)))
	}
}

// send queues f on out or fails if the session is dead.
func (s *Session) send(f wire.Frame) error {
	return s.sendCtx(context.Background(), f)
}

// trySend is send that never waits. A full queue returns ErrCongested so a
// TUN/SOCKS UDP reader can keep draining the kernel.
func (s *Session) trySend(f wire.Frame) error {
	if s.closed.Load() {
		return ErrClosed
	}
	if s.shaped(f) && !s.tryRate(len(f.Payload)) {
		return ErrCongested
	}
	ch := s.out
	dataQueued := false
	if s.priFrame(f) {
		ch = s.pri
	} else if f.Cmd == wire.CmdData {
		if st := s.stream(f.StreamID); st != nil {
			st.queued.Add(1)
			dataQueued = true
		}
	}
	undo := func() {
		if !dataQueued {
			return
		}
		if st := s.stream(f.StreamID); st != nil && st.queued.Load() > 0 {
			st.queued.Add(-1)
		}
	}
	select {
	case ch <- f:
		noteTraffic(f, &s.bytesUp)
		return nil
	case <-s.die:
		undo()
		return ErrClosed
	default:
		undo()
		return ErrCongested
	}
}

// sendCtx is send with a deadline. Ping uses this so a full pri queue
// cannot pin WatchStall until die.
func (s *Session) sendCtx(ctx context.Context, f wire.Frame) error {
	if s.closed.Load() {
		return ErrClosed
	}
	if s.shaped(f) {
		if err := s.waitRate(len(f.Payload)); err != nil {
			return err
		}
	}
	ch := s.out
	dataQueued := false
	if s.priFrame(f) {
		ch = s.pri
	} else if f.Cmd == wire.CmdData {
		if st := s.stream(f.StreamID); st != nil {
			st.queued.Add(1)
			dataQueued = true
		}
	}
	undo := func() {
		if !dataQueued {
			return
		}
		if st := s.stream(f.StreamID); st != nil && st.queued.Load() > 0 {
			st.queued.Add(-1)
		}
	}
	var done <-chan struct{}
	if ctx != nil {
		done = ctx.Done()
	}
	select {
	case ch <- f:
		noteTraffic(f, &s.bytesUp)
		return nil
	case <-s.die:
		undo()
		return ErrClosed
	case <-done:
		undo()
		if ctx != nil && ctx.Err() != nil {
			return ctx.Err()
		}
		return context.Canceled
	}
}

// handle dispatches one inbound frame. Unknown commands are ignored.
//
// Notes on the cases below:
//   - Open: a new stream from the peer. If Accept is not keeping up (the
//     accept queue is full), the stream is refused with Reset at once.
//     Data that arrived earlier for this id is delivered now.
//   - Data for an unknown stream is kept (up to 256 KiB per stream),
//     because GET and HTTP/2 can deliver Data before its Open.
//   - Dgram goes to the FilterDgram hook first, then to ReadDgram; if
//     nobody is reading, it is dropped like a lost UDP packet.
//   - Pad, Tune, Report and Front are handled by the front, not the mux.
func (s *Session) handle(f wire.Frame) {
	// Police inbound datagrams with a drop. Never waitRate here: a full
	// token bucket would stall Push and stop reading the front socket.
	if s.shaped(f) && f.Cmd == wire.CmdDgram && !s.tryRate(len(f.Payload)) {
		return
	}
	noteTraffic(f, &s.bytesDown)
	switch f.Cmd {
	case wire.CmdOpen:
		s.mu.Lock()
		if _, exists := s.streams[f.StreamID]; exists {
			s.mu.Unlock()
			return
		}
		st := newStream(s, f.StreamID)
		if host, port, err := DecodeTarget(f.Payload); err == nil {
			st.target = net.JoinHostPort(host, strconv.Itoa(int(port)))
		}
		s.streams[f.StreamID] = st
		bufs := s.pending[f.StreamID]
		delete(s.pending, f.StreamID)
		delete(s.pendByte, f.StreamID)
		s.mu.Unlock()
		select {
		case s.accept <- st:
			for _, b := range bufs {
				st.push(b)
			}
		default:
			st.kill()
			_ = s.trySend(wire.Frame{Cmd: wire.CmdReset, StreamID: f.StreamID, Payload: wire.ResetPayload(wire.Internal)})
		}
	case wire.CmdData:
		s.mu.Lock()
		st := s.streams[f.StreamID]
		if st != nil {
			s.mu.Unlock()
			st.push(f.Payload)
			break
		}
		n := s.pendByte[f.StreamID]
		if n+len(f.Payload) <= 256*1024 {
			s.pending[f.StreamID] = append(s.pending[f.StreamID], append([]byte(nil), f.Payload...))
			s.pendByte[f.StreamID] = n + len(f.Payload)
		}
		s.mu.Unlock()
	case wire.CmdClose, wire.CmdReset:
		s.mu.Lock()
		st := s.streams[f.StreamID]
		delete(s.pending, f.StreamID)
		delete(s.pendByte, f.StreamID)
		s.mu.Unlock()
		if st != nil {
			st.kill()
		}
	case wire.CmdPing:
		_ = s.trySend(wire.Frame{Cmd: wire.CmdPong, StreamID: f.StreamID, Payload: f.Payload})
	case wire.CmdDgram:
		host, port, err := DecodeTarget(f.Payload)
		if err != nil {
			return
		}
		n := 2 + len(host) + 2
		if n > len(f.Payload) {
			return
		}
		dg := dgram{addr: net.JoinHostPort(host, strconv.Itoa(int(port))), data: append([]byte(nil), f.Payload[n:]...)}
		s.mu.Lock()
		fn := s.dgramFn
		s.mu.Unlock()
		if fn != nil && fn(dg.addr, dg.data) {
			return
		}
		select {
		case s.dgrams <- dg:
		default:
		}
	case wire.CmdPong:
		s.notePong(f.Payload)
	case wire.CmdPad, wire.CmdTune, wire.CmdReport, wire.CmdFront:
	case wire.CmdLeave:
		s.Close()
	}
}

// stream looks up a live stream by id.
func (s *Session) stream(id uint32) *Stream {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.streams[id]
}

// drop removes a stream after Close or Reset.
func (s *Session) drop(id uint32) {
	s.mu.Lock()
	delete(s.streams, id)
	s.mu.Unlock()
}

// FilterDgram steals matching datagrams from ReadDgram. fn true = consumed.
func (s *Session) FilterDgram(fn func(addr string, data []byte) bool) {
	s.mu.Lock()
	s.dgramFn = fn
	s.mu.Unlock()
}

// WriteDgram sends CmdDgram: Open-target prefix plus payload, truncated to MaxPayload.
// A full outbound queue drops the datagram (ErrCongested) so the caller keeps
// reading the kernel socket.
func (s *Session) WriteDgram(addr string, data []byte) error {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return err
	}
	pl := append(EncodeTarget(host, wire.Port(portStr)), data...)
	if len(pl) > wire.MaxPayload {
		pl = pl[:wire.MaxPayload]
	}
	return s.trySend(wire.Frame{Cmd: wire.CmdDgram, Payload: pl})
}

// ReadDgram waits for the next datagram not stolen by FilterDgram.
func (s *Session) ReadDgram() (addr string, data []byte, err error) {
	dg, ok := <-s.dgrams
	if !ok {
		return "", nil, ErrClosed
	}
	return dg.addr, dg.data, nil
}

// Close ends the session locally: streams, Accept, and datagrams.
func (s *Session) Close() {
	if s.closed.Swap(true) {
		return
	}
	s.mu.Lock()
	streams := s.streams
	s.streams = map[uint32]*Stream{}
	s.pending = map[uint32][][]byte{}
	s.pendByte = map[uint32]int{}
	s.mu.Unlock()
	for _, st := range streams {
		st.kill()
	}
	close(s.accept)
	close(s.dgrams)
	close(s.die)
}

// Stream is one mux stream. It implements net.Conn.
//
// Inbound Data goes into in (64 chunks). When in is full, extra chunks go
// into park, an overflow list of up to streamParkMax bytes. This "park" is
// a per-stream buffer and is not the same thing as parking a whole mux on
// the node (see SetHold). The reader drains in first and refills it from
// park, so the byte order is kept.
type Stream struct {
	sess   *Session
	id     uint32
	target string
	in     chan []byte
	inMu   sync.RWMutex // senders hold it shared; closing in takes it alone
	remain []byte
	done   chan struct{}
	dead   atomic.Bool
	rdead  atomic.Int64 // unix nano; 0 = none
	wdead  atomic.Int64
	queued atomic.Int32 // bulk Data frames sitting in sess.out
	parkMu sync.Mutex
	park   [][]byte
	parkN  int
	wake   chan struct{} // cap 1; parked Data is readable
}

// newStream is an inbound or outbound stream on s.
func newStream(s *Session, id uint32) *Stream {
	return &Stream{sess: s, id: id, in: make(chan []byte, 64), done: make(chan struct{}), wake: make(chan struct{}, 1)}
}

// Target is the Open host:port.
func (st *Stream) Target() string { return st.target }

// Sess is the parent mux session.
func (st *Stream) Sess() *Session { return st.sess }

// SessRelay reports whether the parent session is a relay trunk.
func (st *Stream) SessRelay() bool {
	if st.sess == nil {
		return false
	}
	return st.sess.Relay()
}

// Read copies inbound Data, honoring SetReadDeadline.
func (st *Stream) Read(p []byte) (int, error) {
	if len(st.remain) > 0 {
		n := copy(p, st.remain)
		st.remain = st.remain[n:]
		return n, nil
	}
	if err := st.pastDeadline(&st.rdead); err != nil {
		return 0, err
	}
	chunk, ok, err := st.recv()
	if err != nil {
		return 0, err
	}
	if !ok {
		return 0, io.EOF
	}
	n := copy(p, chunk)
	st.remain = chunk[n:]
	return n, nil
}

// streamParkMax is extra Data a full stream may hold before Reset.
// in cap 64 × 12 KiB plus this. 256 KiB is about 20 ms at 100 Mbit/s, and a
// short stall then Reset a live download (one stream died around 39 Mbit/s).
// 8 MiB is about 0.6 s at 100 Mbit/s. Push still never blocks; a real hog is
// Reset so mux ingest keeps reading the front.
const streamParkMax = 8 << 20

// full is true when in and the park are both at cap (next Data Resets).
func (st *Stream) full() bool {
	if len(st.in) < cap(st.in) {
		return false
	}
	st.parkMu.Lock()
	n := st.parkN
	st.parkMu.Unlock()
	return n >= streamParkMax
}

// recv waits for the next Data chunk or a read deadline. It checks in, then
// park, and sleeps on in or wake. After the stream is closed it still
// returns the chunks left in park before reporting end of stream.
func (st *Stream) recv() (chunk []byte, ok bool, err error) {
	for {
		if b, ok := st.takeIn(); ok {
			if b != nil {
				st.pumpPark()
				return b, true, nil
			}
			if p := st.takePark(); p != nil {
				return p, true, nil
			}
			return nil, false, nil
		}
		if p := st.takePark(); p != nil {
			return p, true, nil
		}
		ns := st.rdead.Load()
		if ns == 0 {
			select {
			case chunk, ok = <-st.in:
				if ok {
					st.pumpPark()
					return chunk, true, nil
				}
				if p := st.takePark(); p != nil {
					return p, true, nil
				}
				return nil, false, nil
			case <-st.wake:
				continue
			}
		}
		d := time.Until(time.Unix(0, ns))
		if d <= 0 {
			return nil, false, errTimeout
		}
		t := time.NewTimer(d)
		select {
		case chunk, ok = <-st.in:
			t.Stop()
			if ok {
				st.pumpPark()
				return chunk, true, nil
			}
			if p := st.takePark(); p != nil {
				return p, true, nil
			}
			return nil, false, nil
		case <-st.wake:
			t.Stop()
			continue
		case <-t.C:
			return nil, false, errTimeout
		}
	}
}

// takeIn is a non-blocking read from in. ok false = empty; b nil + ok true = closed.
func (st *Stream) takeIn() (b []byte, ok bool) {
	select {
	case chunk, open := <-st.in:
		if !open {
			return nil, true
		}
		return chunk, true
	default:
		return nil, false
	}
}

// takePark pops the oldest parked chunk.
func (st *Stream) takePark() []byte {
	st.parkMu.Lock()
	defer st.parkMu.Unlock()
	if len(st.park) == 0 {
		return nil
	}
	b := st.park[0]
	st.park[0] = nil
	st.park = st.park[1:]
	st.parkN -= len(b)
	if st.parkN < 0 {
		st.parkN = 0
	}
	return b
}

// pumpPark moves parked chunks into in without blocking.
func (st *Stream) pumpPark() {
	for {
		b := st.takePark()
		if b == nil || st.dead.Load() {
			return
		}
		if !st.send(b) {
			if !st.dead.Load() {
				st.unpark(b)
			}
			return
		}
	}
}

// unpark puts b back at the head of the park (order-preserving).
func (st *Stream) unpark(b []byte) {
	st.parkMu.Lock()
	st.park = append([][]byte{b}, st.park...)
	st.parkN += len(b)
	st.parkMu.Unlock()
}

// kick wakes a reader waiting in recv after a park.
func (st *Stream) kick() {
	select {
	case st.wake <- struct{}{}:
	default:
	}
}

// pastDeadline is a timeout if the stored unix-nano deadline has passed.
func (st *Stream) pastDeadline(slot *atomic.Int64) error {
	ns := slot.Load()
	if ns == 0 {
		return nil
	}
	if time.Now().UnixNano() >= ns {
		return errTimeout
	}
	return nil
}

// streamChunk is one CmdData. 12 KiB plaintext seals to ~16 KiB Cookie,
// one GET under Yandex concurrent HeaderBudget. 14 KiB × two frames
// needed 40 KiB Cookie and the edge GOAWAY ENHANCE_YOUR_CALM. Must stay
// > smallPri so bulk Data does not jump the ACK queue.
const streamChunk = 12 << 10

// Write sends CmdData in streamChunk pieces so a GET Cookie stays in budget.
func (st *Stream) Write(p []byte) (int, error) {
	if st.dead.Load() {
		return 0, io.ErrClosedPipe
	}
	if err := st.pastDeadline(&st.wdead); err != nil {
		return 0, err
	}
	n := 0
	for len(p) > 0 {
		chunk := p
		if len(chunk) > streamChunk {
			chunk = chunk[:streamChunk]
		}
		buf := append([]byte(nil), chunk...)
		if err := st.sess.send(wire.Frame{Cmd: wire.CmdData, StreamID: st.id, Payload: buf}); err != nil {
			return n, err
		}
		n += len(chunk)
		p = p[len(chunk):]
	}
	return n, nil
}

// Reset sends CmdReset with code c and kills the stream.
func (st *Stream) Reset(c wire.Code) {
	if st.dead.Load() {
		return
	}
	_ = st.sess.trySend(wire.Frame{Cmd: wire.CmdReset, StreamID: st.id, Payload: wire.ResetPayload(c)})
	st.kill()
}

// Close sends CmdClose. It ends this stream, not the mux session.
func (st *Stream) Close() error {
	if st.dead.Swap(true) {
		return nil
	}
	_ = st.sess.send(wire.Frame{Cmd: wire.CmdClose, StreamID: st.id})
	st.closeIn()
	st.sess.drop(st.id)
	return nil
}

// closeIn ends the stream for readers. dead is already set, so a sender
// that gets the lock after this sees it and does not send on the closed
// channel; a sender already holding the lock finishes first (it never
// blocks).
func (st *Stream) closeIn() {
	st.inMu.Lock()
	close(st.done)
	close(st.in)
	st.inMu.Unlock()
}

// send offers b to in without blocking. It reports false when in is full;
// on a dead stream it drops b and reports true.
func (st *Stream) send(b []byte) bool {
	st.inMu.RLock()
	defer st.inMu.RUnlock()
	if st.dead.Load() {
		return true
	}
	select {
	case st.in <- b:
		return true
	default:
		return false
	}
}

// kill closes the stream locally without sending CmdClose.
func (st *Stream) kill() {
	if st.dead.Swap(true) {
		return
	}
	st.closeIn()
	st.parkMu.Lock()
	st.park = nil
	st.parkN = 0
	st.parkMu.Unlock()
	st.kick()
	st.sess.drop(st.id)
}

// push queues inbound Data. Never blocks. A hog stream parks up to
// streamParkMax then Resets so mux ingest keeps reading the front.
func (st *Stream) push(b []byte) {
	if st.dead.Load() {
		return
	}
	buf := append([]byte(nil), b...)
	if st.send(buf) {
		return
	}
	st.parkMu.Lock()
	if st.dead.Load() {
		st.parkMu.Unlock()
		return
	}
	if st.parkN+len(buf) > streamParkMax {
		st.parkMu.Unlock()
		st.Reset(wire.StreamReset)
		return
	}
	st.park = append(st.park, buf)
	st.parkN += len(buf)
	st.parkMu.Unlock()
	st.kick()
}

// LocalAddr is a dummy mux address for net.Conn.
func (st *Stream) LocalAddr() net.Addr { return dummyAddr("mux") }

// RemoteAddr is the Open target.
func (st *Stream) RemoteAddr() net.Addr { return dummyAddr(st.target) }

// SetDeadline sets both read and write deadlines.
func (st *Stream) SetDeadline(t time.Time) error {
	_ = st.SetReadDeadline(t)
	return st.SetWriteDeadline(t)
}

// SetReadDeadline uses unix nano; zero clears it.
func (st *Stream) SetReadDeadline(t time.Time) error {
	if t.IsZero() {
		st.rdead.Store(0)
		return nil
	}
	st.rdead.Store(t.UnixNano())
	return nil
}

// SetWriteDeadline uses unix nano; zero clears it.
func (st *Stream) SetWriteDeadline(t time.Time) error {
	if t.IsZero() {
		st.wdead.Store(0)
		return nil
	}
	st.wdead.Store(t.UnixNano())
	return nil
}

var errTimeout = &timeoutError{}

// timeoutError implements net.Error for stream deadlines.
type timeoutError struct{}

// Error is "mux: i/o timeout".
func (*timeoutError) Error() string { return "mux: i/o timeout" }

// Timeout is always true.
func (*timeoutError) Timeout() bool { return true }

// Temporary is always true so callers may retry.
func (*timeoutError) Temporary() bool { return true }

// dummyAddr is a net.Addr for mux streams.
type dummyAddr string

// Network is "mux".
func (d dummyAddr) Network() string { return "mux" }

// String is the Open target or "mux".
func (d dummyAddr) String() string { return string(d) }

// EncodeTarget is the Open payload: u16be host_len || host || u16be port.
func EncodeTarget(host string, port uint16) []byte {
	b := make([]byte, 2+len(host)+2)
	binary.BigEndian.PutUint16(b[0:2], uint16(len(host)))
	copy(b[2:], host)
	binary.BigEndian.PutUint16(b[2+len(host):], port)
	return b
}

// DecodeTarget reads an Open or Dgram target prefix.
func DecodeTarget(b []byte) (string, uint16, error) {
	if len(b) < 4 {
		return "", 0, errors.New("short target")
	}
	n := int(binary.BigEndian.Uint16(b[0:2]))
	if len(b) < 2+n+2 {
		return "", 0, errors.New("short target")
	}
	host := string(b[2 : 2+n])
	port := binary.BigEndian.Uint16(b[2+n:])
	return host, port, nil
}

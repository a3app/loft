package transport

import (
	"encoding/binary"
	"io"
	"log"
	"os"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/a3app/loft/internal/mux"
	"github.com/a3app/loft/internal/wire"
)

// Stand measurement switches. Unset matches the shipping path.
// LOFT_STAND_TCPBUF and LOFT_STAND_LOWAT override the socket limits; unset is
// kernel autotune and a 16 KiB unsent cap.
//
//	LOFT_STAND_INNER     seal | cache | clear     (crypto package reads this)
//	LOFT_STAND_FAN       affinity | rr | stripe | off
//	LOFT_STAND_DISPATCH  sync | prefetch | queue
//
// stripe puts a per-frame sequence in the sealed batch (leading 0xA5, which a
// normal length prefix cannot be: MaxPayload is 16 KiB). Both peers must set
// the same LOFT_STAND_INNER and LOFT_STAND_FAN. Do not set these on :444.

func standFan() string {
	switch os.Getenv("LOFT_STAND_FAN") {
	case "off", "rr", "stripe":
		return os.Getenv("LOFT_STAND_FAN")
	default:
		return "affinity"
	}
}

func standSpread() bool {
	switch standFan() {
	case "rr", "stripe":
		return true
	default:
		return false
	}
}

func standDispatch() string {
	switch os.Getenv("LOFT_STAND_DISPATCH") {
	case "prefetch", "queue":
		return os.Getenv("LOFT_STAND_DISPATCH")
	default:
		return "sync"
	}
}

// standPull is one sealed batch. Unset is wsPullBytes (48 KiB).
// Clamped under the 1 MiB h2/quic message cap.
func standPull() int {
	n := standEnvInt("LOFT_STAND_PULL", wsPullBytes)
	if n > (1<<20)-4096 {
		return (1 << 20) - 4096
	}
	return n
}

// notsentLowat is the shipping cap on unsent socket bytes.
const notsentLowat = 16 << 10

// standTCP reports a pinned SO_RCVBUF/SO_SNDBUF. Unset and "auto" do not pin:
// the kernel grows the buffer, which is the shipping path. A positive byte
// count pins that size for a stand comparison. A bad value does not pin.
func standTCP() (n int, pin bool) {
	v := os.Getenv("LOFT_STAND_TCPBUF")
	if v == "" || v == "auto" {
		return 0, false
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return 0, false
	}
	return n, true
}

// standCopies is how many TCP sockets carry one mux when the fan is on.
// Unset is 1 (the owner plus the usual extra fronts). A direct path between
// these hosts puts about 20 Mbit/s on one TCP and keeps climbing past 8–32
// sockets, so one stream needs several sockets to use the path. Clamped 1..64.
func standCopies() int {
	n := standEnvInt("LOFT_STAND_COPIES", 1)
	if n < 1 {
		return 1
	}
	if n > 64 {
		return 64
	}
	return n
}

// standLowat is TCP_NOTSENT_LOWAT. Unset is notsentLowat. "0" and "off" leave
// the kernel limit. A positive count overrides the cap.
func standLowat() int {
	v := os.Getenv("LOFT_STAND_LOWAT")
	switch v {
	case "":
		return notsentLowat
	case "0", "off":
		return 0
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return notsentLowat
	}
	return n
}

// standCopy is the hub splice buffer. Unset is io.Copy's 32 KiB.
func standCopy() int {
	n := standEnvInt("LOFT_STAND_COPY", 32<<10)
	if n > 4<<20 {
		return 4 << 20
	}
	return n
}

// standH2Win makes the H2 client honor the peer SETTINGS_INITIAL_WINDOW_SIZE.
func standH2Win() bool { return os.Getenv("LOFT_STAND_H2WIN") == "1" }

func standTrace() bool { return os.Getenv("LOFT_STAND_TRACE") == "1" }

func standEnvInt(name string, def int) int {
	v := os.Getenv(name)
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n <= 0 {
		return def
	}
	return n
}

// standRelay is io.Copy, or CopyBuffer when LOFT_STAND_COPY is set above 32 KiB.
func standRelay(dst io.Writer, src io.Reader) {
	n := standCopy()
	if n <= 32<<10 {
		_, _ = io.Copy(dst, src)
		return
	}
	buf := make([]byte, n)
	_, _ = io.CopyBuffer(dst, src, buf)
}

// standWrote logs the first writes and a one-line summary so a WAN run shows
// whether the sender waits a full RTT between batches.
func standWrote(n int, last *time.Time, writes, bytes *int, maxGap *time.Duration) {
	if !standTrace() || n <= 0 {
		return
	}
	now := time.Now()
	var gap time.Duration
	if !last.IsZero() {
		gap = now.Sub(*last)
		if gap > *maxGap {
			*maxGap = gap
		}
	}
	*last = now
	*writes++
	*bytes += n
	if *writes <= 8 || *writes%64 == 0 {
		log.Printf("stand write n=%d gap=%s total=%d writes=%d", n, gap.Round(time.Millisecond), *bytes, *writes)
	}
}

func (f *attachFan) pickRR(lanes []fanLane) int {
	if len(lanes) == 0 {
		return -1
	}
	start := f.rr
	for n := 0; n < len(lanes); n++ {
		i := (start + n) % len(lanes)
		ln := lanes[i]
		if ln.write == nil || (ln.dead != nil && ln.dead.Load()) {
			continue
		}
		f.rr = i + 1
		return i
	}
	return -1
}

type standWrite struct {
	write func([]byte) error
	dead  *atomic.Bool
	blob  []byte
}

func (f *attachFan) emit(ln fanLane, blob []byte) {
	switch standDispatch() {
	case "prefetch", "queue":
		f.enqueue(ln, blob)
	default:
		if err := ln.write(blob); err != nil && ln.dead != nil {
			ln.dead.Store(true)
		}
	}
}

func (f *attachFan) enqueue(ln fanLane, blob []byte) {
	depth := 2
	if standDispatch() == "queue" {
		depth = 4
	}
	f.qmu.Lock()
	if f.qs == nil {
		f.qs = map[int]chan standWrite{}
	}
	ch := f.qs[ln.id]
	if ch == nil {
		ch = make(chan standWrite, depth)
		f.qs[ln.id] = ch
		go standWriter(ch)
	}
	f.qmu.Unlock()
	ch <- standWrite{write: ln.write, dead: ln.dead, blob: blob}
}

func standWriter(ch <-chan standWrite) {
	for w := range ch {
		if w.write == nil {
			continue
		}
		if err := w.write(w.blob); err != nil && w.dead != nil {
			w.dead.Store(true)
		}
	}
}

// stripePack is 0xA5 || repeated u32be seq || u16be len || frame.
func stripePack(frames []wire.Frame, seqs []uint32) ([]byte, error) {
	out := []byte{0xA5}
	for i, f := range frames {
		b, err := f.Encode()
		if err != nil {
			return nil, err
		}
		var hdr [6]byte
		seq := uint32(0)
		if i < len(seqs) {
			seq = seqs[i]
		}
		binary.BigEndian.PutUint32(hdr[0:4], seq)
		binary.BigEndian.PutUint16(hdr[4:6], uint16(len(b)))
		out = append(out, hdr[:]...)
		out = append(out, b...)
	}
	return out, nil
}

func stripeParse(plain []byte) ([]uint32, []wire.Frame, error) {
	var seqs []uint32
	var frames []wire.Frame
	for len(plain) > 0 {
		if len(plain) < 6 {
			return nil, nil, wire.ErrShort
		}
		seq := binary.BigEndian.Uint32(plain[0:4])
		n := int(binary.BigEndian.Uint16(plain[4:6]))
		plain = plain[6:]
		if len(plain) < n {
			return nil, nil, wire.ErrShort
		}
		f, _, err := wire.Decode(plain[:n])
		if err != nil {
			return nil, nil, err
		}
		plain = plain[n:]
		seqs = append(seqs, seq)
		frames = append(frames, f)
	}
	return seqs, frames, nil
}

type stripeRB struct {
	mu   sync.Mutex
	next uint32
	hold map[uint32]wire.Frame
}

var stripeTabs sync.Map // *mux.Session -> *stripeRB

func stripeIngest(sess *mux.Session, body []byte) {
	seqs, frames, err := stripeParse(body)
	if err != nil || sess == nil {
		return
	}
	v, _ := stripeTabs.LoadOrStore(sess, &stripeRB{hold: map[uint32]wire.Frame{}})
	rb := v.(*stripeRB)
	rb.mu.Lock()
	var ready []wire.Frame
	for i, f := range frames {
		seq := seqs[i]
		if seq < rb.next {
			continue
		}
		if seq == rb.next {
			ready = append(ready, f)
			rb.next++
			for {
				h, ok := rb.hold[rb.next]
				if !ok {
					break
				}
				delete(rb.hold, rb.next)
				ready = append(ready, h)
				rb.next++
			}
			continue
		}
		rb.hold[seq] = f
	}
	rb.mu.Unlock()
	// One gap filling releases every frame that was waiting. Pushing that
	// pile at once overflows the stream park and Reset drops a live download.
	// Hand the reader a slice at a time.
	const step = 8
	for len(ready) > 0 {
		n := step
		if n > len(ready) {
			n = len(ready)
		}
		sess.Push(ready[:n])
		ready = ready[n:]
		if len(ready) > 0 {
			runtime.Gosched()
		}
	}
}

func isStripe(plain []byte) bool {
	return len(plain) > 0 && plain[0] == 0xA5
}

// ingestPlain pushes one opened batch. A stripe batch is reordered first.
func ingestPlain(sess *mux.Session, plain []byte) bool {
	if isStripe(plain) {
		stripeIngest(sess, plain[1:])
		return true
	}
	frames, err := wire.DecodeBatch(plain)
	if err != nil {
		return false
	}
	if len(frames) > 0 && sess != nil {
		sess.Push(frames)
	}
	return true
}

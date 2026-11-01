package transport

// One mux, many fronts. Each stream stays on the front that took its first
// frame so CmdData is not reordered across different RTTs.
//
// The "fan" is the code that spreads Pulled frames over the attached lanes.
// Both sides use it: the client's dispatchLoop and the node's
// dispatchAttach. The default mode is affinity (a stream sticks to its
// lane). Test stands can pick rr or stripe with LOFT_STAND_FAN: frames go
// round-robin, and stripe adds a per-frame sequence so the receiver can put
// them back in order.

import (
	"sync"
	"sync/atomic"

	ecrypto "github.com/a3app/loft/internal/crypto"
	"github.com/a3app/loft/internal/wire"
)

// fanLane is a snapshot of one live lane for a single send.
type fanLane struct {
	id    int
	dead  *atomic.Bool
	write func([]byte) error
}

// attachFan holds the per-session fan state. bind maps a stream id to the
// lane id it is stuck to; rr is the next lane for a new stream; seq numbers
// frames in stripe mode; qs are per-lane write queues, used only when a test
// stand sets LOFT_STAND_DISPATCH.
type attachFan struct {
	key  []byte
	mu   sync.Mutex
	bind map[uint32]int
	rr   int
	seq  atomic.Uint32
	qmu  sync.Mutex
	qs   map[int]chan standWrite
}

// sealFrames encodes one lane's share of frames (as a stripe batch in
// stripe mode) and seals it with the session key.
func sealFrames(key []byte, frames []wire.Frame, seqs []uint32) ([]byte, error) {
	var raw []byte
	var err error
	if standFan() == "stripe" {
		raw, err = stripePack(frames, seqs)
	} else {
		raw, err = wire.EncodeBatch(frames)
	}
	if err != nil {
		return nil, err
	}
	return ecrypto.Seal(key, raw, nil)
}

// send seals frames per lane. A stream's later frames stay on the lane that
// took the first one. A lane that is gone releases the stream onto a live one.
func (f *attachFan) send(lanes []fanLane, frames []wire.Frame) {
	if f == nil || len(lanes) == 0 || len(frames) == 0 {
		return
	}
	if f.bind == nil {
		f.bind = map[uint32]int{}
	}
	groups := map[int][]wire.Frame{}
	gseq := map[int][]uint32{}
	var order []int
	f.mu.Lock()
	for _, fr := range frames {
		i := f.pickLocked(lanes, fr)
		if i < 0 {
			continue
		}
		if _, ok := groups[i]; !ok {
			order = append(order, i)
		}
		groups[i] = append(groups[i], fr)
		if standFan() == "stripe" {
			gseq[i] = append(gseq[i], f.seq.Add(1)-1)
		}
	}
	f.mu.Unlock()
	var wg sync.WaitGroup
	for _, i := range order {
		wg.Add(1)
		ln := lanes[i]
		part := groups[i]
		seqs := gseq[i]
		go func() {
			defer wg.Done()
			blob, err := sealFrames(f.key, part, seqs)
			if err != nil {
				return
			}
			f.emit(ln, blob)
		}()
	}
	wg.Wait()
}

// pickLocked returns the index in lanes for frame fr, or -1 if no lane is
// live. A stream keeps its lane while that lane lives; a new stream, or one
// whose lane died, takes the next live lane round-robin. Frames with no
// stream (StreamID 0) just take the next lane. f.mu must be held.
func (f *attachFan) pickLocked(lanes []fanLane, fr wire.Frame) int {
	if standSpread() {
		return f.pickRR(lanes)
	}
	live := func(i int) bool {
		ln := lanes[i]
		return ln.write != nil && (ln.dead == nil || !ln.dead.Load())
	}
	if fr.StreamID != 0 {
		if id, ok := f.bind[fr.StreamID]; ok {
			for i := range lanes {
				if lanes[i].id == id && live(i) {
					return i
				}
			}
			delete(f.bind, fr.StreamID)
		}
	}
	if len(lanes) == 0 {
		return -1
	}
	start := f.rr
	for n := 0; n < len(lanes); n++ {
		i := (start + n) % len(lanes)
		if !live(i) {
			continue
		}
		f.rr = i + 1
		if fr.StreamID != 0 {
			f.bind[fr.StreamID] = lanes[i].id
		}
		return i
	}
	return -1
}

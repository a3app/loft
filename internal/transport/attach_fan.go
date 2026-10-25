package transport

// One mux, many fronts. Each stream stays on the front that took its first
// frame so CmdData is not reordered across different RTTs.

import (
	"sync"
	"sync/atomic"

	ecrypto "github.com/a3app/loft/internal/crypto"
	"github.com/a3app/loft/internal/wire"
)

type fanLane struct {
	id    int
	dead  *atomic.Bool
	write func([]byte) error
}

type attachFan struct {
	key  []byte
	mu   sync.Mutex
	bind map[uint32]int
	rr   int
}

func sealFrames(key []byte, frames []wire.Frame) ([]byte, error) {
	raw, err := wire.EncodeBatch(frames)
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
	}
	f.mu.Unlock()
	var wg sync.WaitGroup
	for _, i := range order {
		wg.Add(1)
		ln := lanes[i]
		part := groups[i]
		go func() {
			defer wg.Done()
			blob, err := sealFrames(f.key, part)
			if err != nil {
				return
			}
			if err := ln.write(blob); err != nil && ln.dead != nil {
				ln.dead.Store(true)
			}
		}()
	}
	wg.Wait()
}

func (f *attachFan) pickLocked(lanes []fanLane, fr wire.Frame) int {
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

package mux

// Per-session payload cap: bulk CmdData/CmdDgram only. Control and small
// frames (ACK, DNS, ICMP) skip the bucket so a cap does not kill the session.

import (
	"sync"
	"time"

	"github.com/a3app/loft/internal/wire"
)

const (
	rateBurstMin = 64 << 10
	rateBurstMax = 1 << 20
)

// bucket is a token bucket in bytes.
type bucket struct {
	mu     sync.Mutex
	rate   float64 // bytes/s
	burst  float64
	tokens float64
	last   time.Time
}

// SetRate caps bulk Data/Dgram on this session to bps bytes/s, both ways.
// 0 clears the cap. Burst is one second of rate, clamped 64 KiB–1 MiB.
func (s *Session) SetRate(bps uint64) {
	s.setRate(bps, 0)
}

func (s *Session) setRate(bps uint64, burst int) {
	if bps == 0 {
		s.rate.Store(nil)
		return
	}
	b := float64(burst)
	if b <= 0 {
		b = float64(bps)
		if b < rateBurstMin {
			b = rateBurstMin
		}
		if b > rateBurstMax {
			b = rateBurstMax
		}
	}
	s.rate.Store(&bucket{rate: float64(bps), burst: b, tokens: b, last: time.Now()})
}

func (s *Session) waitRate(n int) error {
	if n <= 0 {
		return nil
	}
	b := s.rate.Load()
	if b == nil {
		return nil
	}
	return b.wait(n, s.die)
}

func (s *Session) shaped(f wire.Frame) bool {
	switch f.Cmd {
	case wire.CmdData, wire.CmdDgram:
		return !s.priFrame(f)
	default:
		return false
	}
}

func (b *bucket) wait(n int, die <-chan struct{}) error {
	for n > 0 {
		chunk := n
		b.mu.Lock()
		if b.rate <= 0 {
			b.mu.Unlock()
			return nil
		}
		if float64(chunk) > b.burst {
			chunk = int(b.burst)
			if chunk < 1 {
				chunk = 1
			}
		}
		b.mu.Unlock()
		if err := b.waitChunk(float64(chunk), die); err != nil {
			return err
		}
		n -= chunk
	}
	return nil
}

func (b *bucket) waitChunk(need float64, die <-chan struct{}) error {
	for {
		b.mu.Lock()
		if b.rate <= 0 {
			b.mu.Unlock()
			return nil
		}
		now := time.Now()
		b.tokens += now.Sub(b.last).Seconds() * b.rate
		if b.tokens > b.burst {
			b.tokens = b.burst
		}
		b.last = now
		if b.tokens >= need {
			b.tokens -= need
			b.mu.Unlock()
			return nil
		}
		deficit := need - b.tokens
		d := time.Duration(deficit / b.rate * float64(time.Second))
		if d < time.Millisecond {
			d = time.Millisecond
		}
		b.mu.Unlock()
		t := time.NewTimer(d)
		select {
		case <-t.C:
		case <-die:
			t.Stop()
			return ErrClosed
		}
	}
}

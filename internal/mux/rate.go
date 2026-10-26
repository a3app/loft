package mux

// Per-session payload cap: bulk CmdData/CmdDgram only. Control and small
// frames (ACK, DNS, ICMP) skip the bucket so a cap does not kill the session.
//
// The node sets the cap from the member's rate_bps setting. Outbound bulk
// frames wait for tokens (the sender slows down). Inbound datagrams over
// the cap are dropped instead, because waiting there would stop the mux
// from reading the network at all.

import (
	"sync"
	"time"

	"github.com/a3app/loft/internal/wire"
)

// Burst limits: the bucket holds one second of rate, but never less than
// 64 KiB (so a slow cap still lets one stream chunk through) and never more
// than 1 MiB.
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

// setRate is SetRate with an explicit burst; burst <= 0 picks the default.
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

// waitRate blocks until n tokens are available or the session dies.
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

// tryRate takes n tokens or returns false. Never waits.
func (s *Session) tryRate(n int) bool {
	if n <= 0 {
		return true
	}
	b := s.rate.Load()
	if b == nil {
		return true
	}
	return b.try(n)
}

// try refills the bucket for the time since the last call and takes n
// tokens if they are there.
func (b *bucket) try(n int) bool {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.rate <= 0 {
		return true
	}
	now := time.Now()
	b.tokens += now.Sub(b.last).Seconds() * b.rate
	if b.tokens > b.burst {
		b.tokens = b.burst
	}
	b.last = now
	need := float64(n)
	if b.tokens < need {
		return false
	}
	b.tokens -= need
	return true
}

// shaped reports whether f counts against the rate cap: only bulk Data and
// Dgram, never frames that go to the priority queue.
func (s *Session) shaped(f wire.Frame) bool {
	switch f.Cmd {
	case wire.CmdData, wire.CmdDgram:
		return !s.priFrame(f)
	default:
		return false
	}
}

// wait takes n tokens, sleeping as needed. A request larger than the burst
// is split into burst-sized pieces, because the bucket can never hold more
// than burst tokens at once.
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

// waitChunk sleeps until need tokens (at most burst) have built up, then
// takes them. It returns ErrClosed if the session dies while waiting.
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

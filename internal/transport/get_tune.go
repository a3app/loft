package transport

import (
	"sync"
	"time"

	"github.com/a3app/loft/internal/cdn"
	"github.com/a3app/loft/internal/wire"
)

// frontTune is per-GET-front advertised Cookie budget and inflight.
// Client Notes (CALM/413/…) land here; a Tune downlink is queued only
// when those values actually change.
type frontTune struct {
	mu        sync.Mutex
	budget    uint16
	inflight  uint8
	defBudget uint16
	defIn     uint8
	calmN     int
	calmWin   time.Time
	quietAt   time.Time
	reply     bool
}

func newFrontTune(prof cdn.Profile) *frontTune {
	b := prof.HeaderBudget
	if b <= 0 {
		b = 16 << 10
	}
	if b > 0xffff {
		b = 0xffff
	}
	in := prof.Inflight
	if in <= 0 {
		in = 8
	}
	if in > 255 {
		in = 255
	}
	now := time.Now()
	return &frontTune{
		budget:    uint16(b),
		inflight:  uint8(in),
		defBudget: uint16(b),
		defIn:     uint8(in),
		quietAt:   now,
	}
}

func (t *frontTune) advertised() (budget uint16, inflight uint8) {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.budget, t.inflight
}

func (t *frontTune) observe(ev byte) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	now := time.Now()
	switch ev {
	case wire.TuneCalm, wire.Tune404:
		if now.Sub(t.calmWin) > 30*time.Second {
			t.calmN = 0
			t.calmWin = now
		}
		t.calmN++
		if t.calmN >= 3 {
			t.calmN = 0
			t.calmWin = now
			t.dropLocked()
		}
	case wire.Tune413:
		n := t.budget / 2
		if n < 1024 {
			n = 1024
		}
		if n != t.budget {
			t.budget = n
			t.reply = true
			t.quietAt = now
		}
	case wire.Tune429:
		if t.inflight > 4 {
			n := t.inflight / 2
			if n < 4 {
				n = 4
			}
			t.inflight = n
			t.reply = true
			t.quietAt = now
		}
	}
}

func (t *frontTune) dropLocked() {
	if t.inflight > 8 {
		n := t.inflight / 2
		if n < 8 {
			n = 8
		}
		t.inflight = n
		t.reply = true
		t.quietAt = time.Now()
		return
	}
	if t.budget > 8<<10 {
		n := t.budget / 2
		if n < 8<<10 {
			n = 8 << 10
		}
		t.budget = n
		t.reply = true
		t.quietAt = time.Now()
		return
	}
	if t.inflight > 4 {
		t.inflight = 4
		t.reply = true
		t.quietAt = time.Now()
	}
}

func (t *frontTune) recover() {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if time.Since(t.quietAt) < 2*time.Minute {
		return
	}
	if t.inflight < t.defIn {
		n := t.inflight * 2
		if n > t.defIn {
			n = t.defIn
		}
		if n < 1 {
			n = 1
		}
		t.inflight = n
		t.reply = true
		t.quietAt = time.Now()
		return
	}
	if t.budget < t.defBudget {
		n := uint16(int(t.budget) * 2)
		if n > t.defBudget {
			n = t.defBudget
		}
		t.budget = n
		t.reply = true
		t.quietAt = time.Now()
	}
}

func (t *frontTune) takeReply() (wire.Frame, bool) {
	if t == nil {
		return wire.Frame{}, false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if !t.reply {
		return wire.Frame{}, false
	}
	t.reply = false
	return wire.TuneFrame(wire.TuneSet, t.inflight, t.budget), true
}

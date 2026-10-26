package transport

// Witnesses and automatic promotion of a standby controller.
//
// A standby may become the controller by itself only when the controller is
// really gone, not just when the standby's own link to it broke (then two
// controllers would run at once). Relays are the witnesses: each one tells
// every standby, every few seconds, whether it reaches the controller. A
// standby set to auto_promote takes over when it has not reached the
// controller for promote_after and more than half of the relays (at least
// two) say they do not reach it either.

import (
	"context"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/a3app/loft/internal/config"
	"github.com/a3app/loft/internal/identity"
	"github.com/a3app/loft/internal/wire"
)

const (
	witnessEvery = 10 * time.Second
	witnessFresh = 30 * time.Second // reports older than this do not count
)

// PromoteHook is called once when a standby decides to take over; the node
// sets it (it records the decision and restarts in the controller role).
var PromoteHook func()

// witnessLoop sends this relay's report to every standby address.
func witnessLoop(ctx context.Context, rt *relayRuntime, id string, key []byte, addrs []string) {
	t := time.NewTicker(witnessEvery)
	defer t.Stop()
	for {
		rep := wire.EncodeWitness(wire.Witness{ID: id, At: time.Now().Unix(), Sees: rt.ctlUp.Load()}, key)
		for _, a := range addrs {
			c, err := net.DialTimeout("tcp", a, 3*time.Second)
			if err != nil {
				continue
			}
			_ = c.SetWriteDeadline(time.Now().Add(3 * time.Second))
			_, _ = c.Write(rep)
			_ = c.Close()
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

// witnessBook holds the latest report of each relay.
type witnessBook struct {
	mu   sync.Mutex
	last map[string]wire.Witness
}

func (b *witnessBook) put(w wire.Witness) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.last == nil {
		b.last = map[string]wire.Witness{}
	}
	if old, ok := b.last[w.ID]; !ok || w.At >= old.At {
		b.last[w.ID] = w
	}
}

// lost counts relays among voters whose fresh report says they do not
// reach the controller.
func (b *witnessBook) lost(voters []string, now time.Time) int {
	b.mu.Lock()
	defer b.mu.Unlock()
	n := 0
	for _, id := range voters {
		w, ok := b.last[id]
		if ok && !w.Sees && now.Sub(time.Unix(w.At, 0)) <= witnessFresh {
			n++
		}
	}
	return n
}

// serveWitness takes relays' reports on ln, checked against the relay keys
// in the standby copy of the controller's state.
func serveWitness(ln net.Listener, standby string, book *witnessBook) {
	for {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		go func() {
			defer c.Close()
			_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
			b, err := io.ReadAll(io.LimitReader(c, 512))
			if err != nil {
				return
			}
			id, err := wire.WitnessID(b)
			if err != nil {
				return
			}
			ring, err := identity.Load(filepath.Join(standby, "relays.keys"))
			if err != nil {
				return
			}
			key := ring.TokenFor(id)
			if len(key) == 0 {
				return
			}
			if w, err := wire.DecodeWitness(b, key, time.Now(), witnessFresh); err == nil {
				book.put(w)
			}
		}()
	}
}

// promoteDecision reports whether a standby should take over now. ctl is the
// controller's node.conf from the standby copy; self is this standby's id.
func promoteDecision(ctl *config.ServerConfig, self string, down, after time.Duration, book *witnessBook, now time.Time) bool {
	var auto bool
	var voters []string
	for _, e := range ctl.Relays {
		if e.ID == self {
			auto = e.AutoPromote
			continue
		}
		voters = append(voters, e.ID)
	}
	if !auto || down < after || len(voters) < 2 {
		return false
	}
	return book.lost(voters, now)*2 > len(voters)
}

// runStandby starts the witness listener and, every few seconds, decides
// whether to take over.
func runStandby(ctx context.Context, cfg *config.ServerConfig, rt *relayRuntime, standby string) {
	book := &witnessBook{}
	if cfg.WitnessListen != "" {
		ln, err := net.Listen("tcp", cfg.WitnessListen)
		if err != nil {
			log.Printf("loft-standby: witness %v", err)
		} else {
			go func() { <-ctx.Done(); ln.Close() }()
			go serveWitness(ln, standby, book)
		}
	}
	after := time.Duration(cfg.PromoteAfter) * time.Second
	if after <= 0 {
		after = 5 * time.Minute
	}
	t := time.NewTicker(witnessEvery)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		b, err := os.ReadFile(filepath.Join(standby, "node.conf"))
		if err != nil {
			continue
		}
		ctl, err := config.ParseCommands(string(b))
		if err != nil {
			continue
		}
		if promoteDecision(ctl, cfg.ID, rt.downFor(), after, book, time.Now()) {
			log.Printf("loft-standby: the controller is gone for %s and most relays agree; taking over", rt.downFor().Round(time.Second))
			if PromoteHook != nil {
				PromoteHook()
			}
			return
		}
	}
}

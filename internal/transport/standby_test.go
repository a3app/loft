package transport

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/a3app/loft/internal/config"
	"github.com/a3app/loft/internal/wire"
)

func ctlWith(auto bool, relays ...string) *config.ServerConfig {
	c := &config.ServerConfig{Relays: []config.RelayEntry{{ID: "r2", Replica: true, AutoPromote: auto}}}
	for _, id := range relays {
		c.Relays = append(c.Relays, config.RelayEntry{ID: id})
	}
	return c
}

func TestPromoteDecision(t *testing.T) {
	now := time.Now()
	after := 5 * time.Minute
	book := &witnessBook{}
	report := func(id string, sees bool, at time.Time) { book.put(wire.Witness{ID: id, At: at.Unix(), Sees: sees}) }
	report("ru1", false, now)
	report("ru2", false, now)
	report("ru3", true, now)

	if !promoteDecision(ctlWith(true, "ru1", "ru2", "ru3"), "r2", 6*time.Minute, after, book, now) {
		t.Fatal("2 of 3 relays lost the controller: should promote")
	}
	if promoteDecision(ctlWith(false, "ru1", "ru2", "ru3"), "r2", 6*time.Minute, after, book, now) {
		t.Fatal("promoted without auto_promote")
	}
	if promoteDecision(ctlWith(true, "ru1", "ru2", "ru3"), "r2", time.Minute, after, book, now) {
		t.Fatal("promoted before promote_after")
	}
	if promoteDecision(ctlWith(true, "ru1", "ru2", "ru3", "ru4"), "r2", 6*time.Minute, after, book, now) {
		t.Fatal("2 of 4 is not a majority")
	}
	if promoteDecision(ctlWith(true, "ru1"), "r2", 6*time.Minute, after, book, now) {
		t.Fatal("one relay is not enough to promote")
	}
	stale := &witnessBook{}
	stale.put(wire.Witness{ID: "ru1", At: now.Add(-time.Minute).Unix()})
	stale.put(wire.Witness{ID: "ru2", At: now.Add(-time.Minute).Unix()})
	if promoteDecision(ctlWith(true, "ru1", "ru2"), "r2", 6*time.Minute, after, stale, now) {
		t.Fatal("stale reports counted")
	}
}

// TestWitnessReportsReachStandby: relays' reports arrive over TCP and are
// counted only under the relay's own key from the standby copy.
func TestWitnessReportsReachStandby(t *testing.T) {
	sb := t.TempDir()
	os.WriteFile(filepath.Join(sb, "relays.keys"), []byte("ru1: lt_ru1\nru2: lt_ru2\n"), 0o600)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	book := &witnessBook{}
	go serveWitness(ln, sb, book)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	lost := &relayRuntime{started: time.Now()} // never reached the controller
	go witnessLoop(ctx, lost, "ru1", []byte("lt_ru1"), []string{ln.Addr().String()})
	go witnessLoop(ctx, lost, "ru2", []byte("lt_wrong"), []string{ln.Addr().String()})
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) && book.lost([]string{"ru1"}, time.Now()) == 0 {
		time.Sleep(20 * time.Millisecond)
	}
	if book.lost([]string{"ru1"}, time.Now()) != 1 {
		t.Fatal("ru1's report did not arrive")
	}
	if book.lost([]string{"ru2"}, time.Now()) != 0 {
		t.Fatal("a report signed with a wrong key was counted")
	}
}

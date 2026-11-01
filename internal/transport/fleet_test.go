package transport

// Tests for fleet admit, YAML allow-list, and silent heartbeat eviction.

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"
	"time"

	"github.com/a3app/loft/internal/config"
	"github.com/a3app/loft/internal/testcfg"
	"github.com/a3app/loft/internal/wire"
)

// TestFleetAdmitAndEvict signs a slice, lists self+relay, then drop removes the relay.
func TestFleetAdmitAndEvict(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.ServerConfig{
		Advertise: "https://hub.example",
		Relays:    []config.RelayEntry{{ID: "ru1"}},
		Decoy:     []string{testcfg.DecoyAddr()},
	}
	f := NewFleet(priv, cfg)
	sl, _ := f.admit(wire.EnrolReq{ID: "ru1", URL: "https://203.0.113.10", Decoy: testcfg.DecoyAddr(), Fronts: []string{"get", "ws"}}, func() {})
	if err := sl.Verify(); err != nil {
		t.Fatal(err)
	}
	if sl.ID != "ru1" || sl.BeatMS == 0 {
		t.Fatalf("%+v", sl)
	}
	r, err := wire.DecodeRoster(f.RosterBytes())
	if err != nil {
		t.Fatal(err)
	}
	if err := r.Verify(); err != nil {
		t.Fatal(err)
	}
	if len(r.Relays) != 2 { // self + ru1
		t.Fatalf("%+v", r.Relays)
	}
	f.drop("ru1")
	r, _ = wire.DecodeRoster(f.RosterBytes())
	if len(r.Relays) != 1 || r.Relays[0].ID != "self" {
		t.Fatalf("after drop %+v", r.Relays)
	}
}

// TestFleetAllowList admits only listed relays; node.conf URL/weight win.
func TestFleetAllowList(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	cfg := &config.ServerConfig{
		Relays: []config.RelayEntry{{ID: "ru1", URL: "https://fixed.example", Weight: 50}},
	}
	f := NewFleet(priv, cfg)
	if f.allowed("ru2") {
		t.Fatal("ru2")
	}
	if !f.allowed("ru1") {
		t.Fatal("ru1")
	}
	sl, _ := f.admit(wire.EnrolReq{ID: "ru1", URL: "https://ignored.example"}, func() {})
	if sl.URL != "https://fixed.example" || sl.Weight != 50 {
		t.Fatalf("yaml wins %+v", sl)
	}
}

// TestFleetSilentEvict drops a relay whose last beat is older than the limit.
func TestFleetSilentEvict(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	f := NewFleet(priv, &config.ServerConfig{Relays: []config.RelayEntry{{ID: "x"}}})
	f.admit(wire.EnrolReq{ID: "x", URL: "https://x"}, func() {})
	f.mu.Lock()
	f.live["x"].last = time.Now().Add(-time.Minute)
	f.live["x"].beatMS = 1000
	f.mu.Unlock()
	f.evictSilent()
	r, _ := wire.DecodeRoster(f.RosterBytes())
	for _, e := range r.Relays {
		if e.ID == "x" {
			t.Fatal("still listed")
		}
	}
}

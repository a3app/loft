package transport

// CapRelay is ExtraCaps on a relay trunk, not a member Hello default.

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"

	"github.com/a3app/loft/internal/config"
	"github.com/a3app/loft/internal/identity"
	"github.com/a3app/loft/internal/mux"
	"github.com/a3app/loft/internal/wire"
)

// TestHelloCapsOmitsRelay: loft-front Hello has hold, not relay.
func TestHelloCapsOmitsRelay(t *testing.T) {
	c := helloCaps(&config.ClientConfig{})
	if c.Has(wire.CapRelay) {
		t.Fatalf("member Hello has relay: %v", c.Names())
	}
	if !c.Has(wire.CapHold) {
		t.Fatal("hold stripped from member Hello")
	}
	c = helloCaps(&config.ClientConfig{ExtraCaps: uint64(wire.CapRelay)})
	if !c.Has(wire.CapRelay) || !c.Has(wire.CapHold) {
		t.Fatalf("relay ExtraCaps: %v", c.Names())
	}
}

// TestAttachHelloDoesNotSetRelay: Park/adopt only copies hold + instance_id.
func TestAttachHelloDoesNotSetRelay(t *testing.T) {
	h := wire.Hello{Version: 1, InstanceID: "a", Min: wire.ProtoMin, Max: wire.Proto, Caps: wire.CapRelay | wire.CapHold}
	ms, reused := adoptServerMux(h.Encode(), nil)
	if reused || ms.Relay() {
		t.Fatalf("attachHello set relay reused=%v relay=%v", reused, ms.Relay())
	}
	if !ms.Holding() || ms.Instance() != "a" {
		t.Fatalf("hold/instance lost hold=%v inst=%q", ms.Holding(), ms.Instance())
	}
	ms.Close()
}

// TestApplyRelayRoleAllowlist: enrol:list members cannot self-assert a trunk.
func TestApplyRelayRoleAllowlist(t *testing.T) {
	t.Cleanup(func() {
		FleetCtl = nil
		AuthKeys = nil
	})
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	ring := identity.NewRing()
	ring.Put("alice", "lt_alice")
	ring.Put("ru1", "lt_ru1")
	AuthKeys = ring
	FleetCtl = NewFleet(priv, &config.ServerConfig{
		Enrol:  "list",
		Relays: []config.RelayEntry{{ID: "ru1"}},
	})

	member := wire.Hello{Version: 1, MemberID: "alice", Min: wire.ProtoMin, Max: wire.Proto, Caps: wire.CapRelay | wire.CapHold}
	ms := mux.NewServer()
	applyRelayRole(ms, member.Encode(), []byte("lt_alice"))
	if ms.Relay() {
		t.Fatal("member alice became trunk")
	}

	listed := member
	listed.MemberID = "ru1"
	ms = mux.NewServer()
	applyRelayRole(ms, listed.Encode(), []byte("lt_ru1"))
	if !ms.Relay() {
		t.Fatal("listed relay denied")
	}

	FleetCtl = NewFleet(priv, &config.ServerConfig{Enrol: "open"})
	ms = mux.NewServer()
	applyRelayRole(ms, member.Encode(), []byte("lt_alice"))
	if !ms.Relay() {
		t.Fatal("enrol:open + CapRelay should SetRelay")
	}

	noBit := member
	noBit.Caps = wire.CapHold
	ms = mux.NewServer()
	applyRelayRole(ms, noBit.Encode(), []byte("lt_alice"))
	if ms.Relay() {
		t.Fatal("Hello without CapRelay")
	}
}

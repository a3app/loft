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

// TestApplyRelayRoleNeedsRelayKey: only a key issued for a listed relay,
// with CapRelay in Hello, opens a trunk, and the trunk carries that id. A
// member key never does, even with CapRelay; a drained relay is refused.
func TestApplyRelayRoleNeedsRelayKey(t *testing.T) {
	t.Cleanup(func() {
		FleetCtl = nil
		AuthKeys = nil
		RelayKeys = nil
	})
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	members := identity.NewRing()
	members.Put("alice", "lt_alice")
	AuthKeys = members
	relays := identity.NewRing()
	relays.Put("ru1", "lt_ru1")
	relays.Put("de1", "lt_de1")
	RelayKeys = relays
	FleetCtl = NewFleet(priv, &config.ServerConfig{
		Relays: []config.RelayEntry{{ID: "ru1"}, {ID: "de1", Drain: true}},
	})
	trunk := wire.Hello{Version: 1, Min: wire.ProtoMin, Max: wire.Proto, Caps: wire.CapRelay | wire.CapHold}

	ms := mux.NewServer()
	applyRelayRole(ms, trunk.Encode(), []byte("lt_alice"))
	if ms.Relay() {
		t.Fatal("member key opened a trunk")
	}
	ms = mux.NewServer()
	applyRelayRole(ms, trunk.Encode(), []byte("lt_ru1"))
	if !ms.Relay() || ms.RelayID() != "ru1" {
		t.Fatalf("relay key refused: relay=%v id=%q", ms.Relay(), ms.RelayID())
	}
	ms = mux.NewServer()
	applyRelayRole(ms, trunk.Encode(), []byte("lt_de1"))
	if ms.Relay() {
		t.Fatal("drained relay opened a trunk")
	}
	noBit := trunk
	noBit.Caps = wire.CapHold
	ms = mux.NewServer()
	applyRelayRole(ms, noBit.Encode(), []byte("lt_ru1"))
	if ms.Relay() {
		t.Fatal("Hello without CapRelay")
	}
}

// TestRelayKeyIsNotAMember: a relay key on an ordinary session is refused
// as a member, with or without member keys on the node.
func TestRelayKeyIsNotAMember(t *testing.T) {
	t.Cleanup(func() {
		AuthKeys = nil
		RelayKeys = nil
	})
	withOverlay(t)
	relays := identity.NewRing()
	relays.Put("ru1", "lt_ru1")
	RelayKeys = relays
	alice := identity.NewRing()
	alice.Put("alice", "lt_alice")
	for _, members := range []*identity.Ring{nil, alice} {
		AuthKeys = members
		st := wire.Status{Code: wire.OK}
		stampOverlayAuth(&st, wire.Hello{Version: 1}.Encode(), mux.NewServer(), nil, []byte("lt_ru1"))
		if st.Code != wire.AuthBadKey {
			t.Fatalf("relay key as member: %v", st.Code)
		}
	}
}

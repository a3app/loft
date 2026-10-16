package transport

// Tests for roster TOFU: first valid pub is pinned, a different signer is ignored.

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"

	"github.com/a3app/loft/internal/wire"
)

// TestRememberRosterTOFU keeps the first signed roster when a second pub tries to replace it.
func TestRememberRosterTOFU(t *testing.T) {
	rosterMu.Lock()
	heldRoster = wire.Roster{}
	rosterMu.Unlock()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	r := wire.Roster{Serial: 2, Relays: []wire.RelayInfo{{ID: "ru1", URL: "https://a.example", Decoy: "www.gosuslugi.ru:443"}}}
	r.Pub = priv.Public().(ed25519.PublicKey)
	r.Sig = ed25519.Sign(priv, r.Body())
	RememberRoster(r.Encode())
	got := CachedRoster()
	if len(got) != 1 || got[0].ID != "ru1" {
		t.Fatalf("%+v", got)
	}
	_, priv2, _ := ed25519.GenerateKey(rand.Reader)
	r2 := wire.Roster{Serial: 9, Relays: []wire.RelayInfo{{ID: "evil", URL: "https://evil.example"}}}
	r2.Pub = priv2.Public().(ed25519.PublicKey)
	r2.Sig = ed25519.Sign(priv2, r2.Body())
	RememberRoster(r2.Encode())
	got = CachedRoster()
	if len(got) != 1 || got[0].ID != "ru1" {
		t.Fatal("tofu")
	}
}

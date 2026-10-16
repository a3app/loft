package crypto

// Controller seed load and roster signature round-trip.

import (
	"path/filepath"
	"testing"

	"github.com/a3app/loft/internal/wire"
)

// TestSignRosterRoundtrip loads a seed, signs a roster, and verifies it.
func TestSignRosterRoundtrip(t *testing.T) {
	dir := t.TempDir()
	priv, err := LoadControllerKey(filepath.Join(dir, "seed"), "token")
	if err != nil {
		t.Fatal(err)
	}
	r := wire.Roster{Serial: 3, Relays: []wire.RelayInfo{{ID: "r", URL: "https://a.example"}}}
	r = SignRoster(priv, r)
	got, err := wire.DecodeRoster(r.Encode())
	if err != nil {
		t.Fatal(err)
	}
	if err := got.Verify(); err != nil {
		t.Fatal(err)
	}
}

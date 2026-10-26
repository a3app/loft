package crypto

// Controller seed load and roster signature round-trip.

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"

	"github.com/a3app/loft/internal/wire"
)

// TestSignRosterRoundtrip loads a seed, signs a roster, and verifies it.
func TestSignRosterRoundtrip(t *testing.T) {
	dir := t.TempDir()
	priv, err := LoadControllerKey(filepath.Join(dir, "seed"))
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

// TestControllerKeyRandomAndKept: the first start writes a random seed and
// later starts read the same key back; two nodes never share a key.
func TestControllerKeyRandomAndKept(t *testing.T) {
	a := filepath.Join(t.TempDir(), "controller.seed")
	b := filepath.Join(t.TempDir(), "controller.seed")
	ka, err := LoadControllerKey(a)
	if err != nil {
		t.Fatal(err)
	}
	kb, err := LoadControllerKey(b)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(ka, kb) {
		t.Fatal("two nodes got the same controller key")
	}
	again, err := LoadControllerKey(a)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(ka, again) {
		t.Fatal("controller key changed across restarts")
	}
	if st, err := os.Stat(a); err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("seed file mode %v %v", st.Mode(), err)
	}
}

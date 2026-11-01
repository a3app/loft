package crypto

// Controller seed load and roster signature round-trip.

import (
	"bytes"
	"crypto/ed25519"
	"os"
	"path/filepath"
	"testing"
	"time"

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

// TestRollControllerKey: the seed is replaced, the roll is signed by the old
// key and points at the new one, serials grow, and LoadKeyRoll gives the roll
// only for the new key.
func TestRollControllerKey(t *testing.T) {
	dir := t.TempDir()
	seed, roll := filepath.Join(dir, "controller.seed"), filepath.Join(dir, "controller.roll")
	old, err := LoadControllerKey(seed)
	if err != nil {
		t.Fatal(err)
	}
	k, err := RollControllerKey(seed, roll, time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	next, _ := LoadControllerKey(seed)
	if bytes.Equal(old, next) || !bytes.Equal(k.Old, old.Public().(ed25519.PublicKey)) || !bytes.Equal(k.New, next.Public().(ed25519.PublicKey)) {
		t.Fatal("roll does not hand old to new")
	}
	if err := k.Verify(time.Now()); err != nil {
		t.Fatal(err)
	}
	if LoadKeyRoll(roll, next.Public().(ed25519.PublicKey)) == nil || LoadKeyRoll(roll, old.Public().(ed25519.PublicKey)) != nil {
		t.Fatal("LoadKeyRoll is not tied to the current key")
	}
	k2, err := RollControllerKey(seed, roll, time.Hour)
	if err != nil || k2.Serial != k.Serial+1 || !bytes.Equal(k2.Old, k.New) {
		t.Fatalf("second roll: %+v %v", k2, err)
	}
}

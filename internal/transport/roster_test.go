package transport

// Tests for roster pin: empty controller_pub refuses; tofu pins first valid pub.

import (
	"crypto/ed25519"
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"

	"github.com/a3app/loft/internal/wire"
)

func testRoster(t *testing.T, id, url string, serial uint64) wire.Roster {
	t.Helper()
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	r := wire.Roster{Serial: serial, Relays: []wire.RelayInfo{{ID: id, URL: url}}}
	r.Pub = priv.Public().(ed25519.PublicKey)
	r.Sig = ed25519.Sign(priv, r.Body())
	return r
}

// TestRememberRosterRequiresPin drops a verified roster when neither
// controller_pub nor tofu is set (default).
func TestRememberRosterRequiresPin(t *testing.T) {
	t.Cleanup(resetRosterForTest)
	resetRosterForTest()
	r := testRoster(t, "ru1", "https://a.example", 2)
	if RememberRoster(r.Encode()) {
		t.Fatal("accepted roster without pin")
	}
	if len(CachedRoster()) != 0 {
		t.Fatal("held unsigned-to-us roster")
	}
	PinControllerPub(r.Pub)
	if !RememberRoster(r.Encode()) {
		t.Fatal("pinned roster refused")
	}
	got := CachedRoster()
	if len(got) != 1 || got[0].ID != "ru1" {
		t.Fatalf("%+v", got)
	}
}

// TestRememberRosterTOFU keeps the first signed roster when a second pub tries to replace it.
func TestRememberRosterTOFU(t *testing.T) {
	t.Cleanup(resetRosterForTest)
	resetRosterForTest()
	AllowTOFU(true)
	r := testRoster(t, "ru1", "https://a.example", 2)
	if !RememberRoster(r.Encode()) {
		t.Fatal("tofu first")
	}
	got := CachedRoster()
	if len(got) != 1 || got[0].ID != "ru1" {
		t.Fatalf("%+v", got)
	}
	r2 := testRoster(t, "evil", "https://evil.example", 9)
	if RememberRoster(r2.Encode()) {
		t.Fatal("tofu replaced signer")
	}
	got = CachedRoster()
	if len(got) != 1 || got[0].ID != "ru1" {
		t.Fatal("tofu")
	}
}

// TestRememberRosterTOFUPersists writes the pin next to client.instance.
func TestRememberRosterTOFUPersists(t *testing.T) {
	t.Cleanup(func() {
		resetRosterForTest()
		SetInstanceFile("")
	})
	resetRosterForTest()
	dir := t.TempDir()
	SetInstanceFile(filepath.Join(dir, "client.instance"))
	AllowTOFU(true)
	r := testRoster(t, "ru1", "https://a.example", 2)
	if !RememberRoster(r.Encode()) {
		t.Fatal("tofu first")
	}
	b, err := os.ReadFile(filepath.Join(dir, "controller.pub"))
	if err != nil {
		t.Fatal(err)
	}
	if len(b) < 64 {
		t.Fatalf("pin %q", b)
	}
}

package wire

// Roster signature, JSON manifest, and tamper rejection.

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"

	"github.com/a3app/loft/internal/testcfg"
)

// TestRosterSignVerify signs, encodes, decodes, and verifies a roster.
func TestRosterSignVerify(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	r := Roster{
		Serial: 7,
		Relays: []RelayInfo{
			{ID: "ru1", URL: "https://203.0.113.10", Decoy: testcfg.DecoyAddr(), Fronts: []string{"get", "ws"}},
			{ID: "ru2", URL: "https://203.0.113.11", Decoy: testcfg.Decoy2Addr(), Drain: true},
		},
	}
	pub := priv.Public().(ed25519.PublicKey)
	r.Pub = append([]byte(nil), pub...)
	r.Sig = ed25519.Sign(priv, r.Body())
	got, err := DecodeRoster(r.Encode())
	if err != nil {
		t.Fatal(err)
	}
	if err := got.Verify(); err != nil {
		t.Fatal(err)
	}
	if got.Serial != 7 || len(got.Relays) != 2 || got.Relays[0].URL != "https://203.0.113.10" || !got.Relays[1].Drain {
		t.Fatalf("%+v", got)
	}
}

// TestManifestJSON round-trips path and mask_pub through the cover document.
func TestManifestJSON(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	r := Roster{Serial: 2, Relays: []RelayInfo{{ID: "a", URL: "https://x", Path: "/api/v1/sync", MaskPub: make([]byte, 32)}}}
	r.Pub = priv.Public().(ed25519.PublicKey)
	r.Sig = ed25519.Sign(priv, r.Body())
	j, err := r.ManifestJSON()
	if err != nil {
		t.Fatal(err)
	}
	got, err := ParseManifest(j)
	if err != nil {
		t.Fatal(err)
	}
	if err := got.Verify(); err != nil {
		t.Fatal(err)
	}
	if got.Relays[0].Path != "/api/v1/sync" || len(got.Relays[0].MaskPub) != 32 {
		t.Fatalf("%+v", got.Relays[0])
	}
}

// TestRosterTamper rejects a flipped serial after a valid signature.
func TestRosterTamper(t *testing.T) {
	_, priv, _ := ed25519.GenerateKey(rand.Reader)
	r := Roster{Serial: 1, Relays: []RelayInfo{{ID: "a", URL: "https://x"}}}
	r.Pub = priv.Public().(ed25519.PublicKey)
	r.Sig = ed25519.Sign(priv, r.Body())
	b := r.Encode()
	b[0] ^= 1
	got, err := DecodeRoster(b)
	if err != nil {
		t.Fatal(err)
	}
	if got.Verify() == nil {
		t.Fatal("tamper")
	}
}

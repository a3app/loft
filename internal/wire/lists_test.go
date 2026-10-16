package wire

// Signed address-list encode/verify and ParseAddr kinds.

import (
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"testing"
)

// TestListsRoundtrip signs lists, verifies, and parses the JSON manifest.
func TestListsRoundtrip(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	l := Lists{
		Serial: 3,
		Sets: []AddrList{
			{Name: "sber", Entries: []Addr{ParseAddr("sberbank.ru"), ParseAddr(".sber.ru"), ParseAddr("10.0.0.0/8")}},
			{Name: "drop", Entries: []Addr{ParseAddr("ads.example")}},
		},
	}
	l.Pub = priv.Public().(ed25519.PublicKey)
	l.Sig = ed25519.Sign(priv, l.Body())
	got, err := DecodeLists(l.Encode())
	if err != nil {
		t.Fatal(err)
	}
	if err := got.Verify(); err != nil {
		t.Fatal(err)
	}
	if got.Sets[0].Name != "sber" || got.Sets[0].Entries[1].Kind != KindSuffix {
		t.Fatalf("%+v", got.Sets[0])
	}
	if !got.Sets[0].Entries[2].MatchIP(net.ParseIP("10.1.2.3")) {
		t.Fatal("cidr")
	}
	j, err := got.ManifestJSON()
	if err != nil {
		t.Fatal(err)
	}
	again, err := ParseListsManifest(j)
	if err != nil || again.Verify() != nil {
		t.Fatal(err, again.Verify())
	}
}

// TestParseAddrKinds classifies exact, suffix (. and *.), and CIDR strings.
func TestParseAddrKinds(t *testing.T) {
	if ParseAddr("YouTube.com").Kind != KindExact {
		t.Fatal("exact")
	}
	if ParseAddr(".ru").Kind != KindSuffix || ParseAddr("*.google.com").Kind != KindSuffix {
		t.Fatal("suffix")
	}
	if ParseAddr("192.168.0.0/16").Kind != KindCIDR {
		t.Fatal("cidr")
	}
}

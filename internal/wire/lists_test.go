package wire

// Signed address-list encode/verify and ParseAddr kinds.

import (
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"testing"

	"github.com/a3app/loft/internal/testcfg"
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
			{Name: "yandex", Entries: []Addr{ParseAddr(testcfg.Decoy2()), ParseAddr("." + testcfg.Parent()), ParseAddr("10.0.0.0/8")}},
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
	if got.Sets[0].Name != "yandex" || got.Sets[0].Entries[1].Kind != KindSuffix {
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

func TestParseListEntryMeta(t *testing.T) {
	if a := ParseListEntry("@dir direct"); a.Kind != KindDir || a.Val != DirDirect {
		t.Fatalf("%+v", a)
	}
	if a := ParseListEntry("@label Russia"); a.Kind != KindLabel || a.Val != "Russia" {
		t.Fatalf("%+v", a)
	}
	if a := ParseListEntry("@name RU direct"); a.Kind != KindLabel || a.Val != "RU direct" {
		t.Fatalf("%+v", a)
	}
	if a := ParseListEntry("@dns 77.88.8.8"); a.Kind != KindDNS || a.Val != "77.88.8.8" {
		t.Fatalf("%+v", a)
	}
	if a := ParseListEntry("@dir sideways"); a.Kind != 0 {
		t.Fatalf("bad dir: %+v", a)
	}
	if a := ParseListEntry(".yandex.ru"); a.Kind != KindSuffix || a.Val != "yandex.ru" {
		t.Fatalf("%+v", a)
	}
}

package mobile

import (
	"crypto/ed25519"
	"encoding/json"
	"testing"

	"github.com/a3app/loft/internal/wire"
)

func TestProfileJSON(t *testing.T) {
	priv := ed25519.NewKeyFromSeed([]byte("Liberty Of Free Transmission 1.0"))
	l := wire.Lists{Serial: 20261027, Sets: []wire.AddrList{
		{Name: "", Entries: []wire.Addr{{Kind: wire.KindLabel, Val: "RU direct"}, {Kind: wire.KindDNS, Val: "77.88.8.8"}}},
		{Name: "ru", Entries: []wire.Addr{{Kind: wire.KindDir, Val: wire.DirDirect}, {Kind: wire.KindLabel, Val: "Russia"}, {Kind: wire.KindCIDR, Val: "5.255.255.0/24"}, {Kind: wire.KindSuffix, Val: "yandex.ru"}}},
		{Name: "ads", Entries: []wire.Addr{{Kind: wire.KindDir, Val: wire.DirDrop}, {Kind: wire.KindSuffix, Val: "doubleclick.net"}}},
	}}
	l.Pub = priv.Public().(ed25519.PublicKey)
	l.Sig = ed25519.Sign(priv, l.Body())
	raw, err := ProfileJSON(l.Encode())
	if err != nil {
		t.Fatal(err)
	}
	var d map[string]any
	if err := json.Unmarshal([]byte(raw), &d); err != nil {
		t.Fatal(err)
	}
	if d["src"] != "op" || d["name"] != "RU direct" {
		t.Fatalf("%v", d)
	}
	svcs := d["services"].([]any)
	if len(svcs) != 2 {
		t.Fatalf("%v", svcs)
	}
}

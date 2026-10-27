package wire

import (
	"crypto/ed25519"
	"encoding/json"
	"testing"
)

func TestRouteProfileRUDirect(t *testing.T) {
	priv := ed25519.NewKeyFromSeed([]byte("Liberty Of Free Transmission 1.0"))
	l := Lists{Serial: 20261027, Sets: []AddrList{
		{Name: "", Entries: []Addr{{Kind: KindLabel, Val: "RU direct"}, {Kind: KindDNS, Val: "77.88.8.8"}}},
		{Name: "ru", Entries: []Addr{{Kind: KindDir, Val: DirDirect}, {Kind: KindLabel, Val: "Russia"}, {Kind: KindCIDR, Val: "5.255.255.0/24"}, {Kind: KindSuffix, Val: "yandex.ru"}}},
		{Name: "ads", Entries: []Addr{{Kind: KindDir, Val: DirDrop}, {Kind: KindSuffix, Val: "doubleclick.net"}}},
		{Name: "via", Entries: []Addr{ParseAddr("10.0.0.0/8")}},
	}}
	l.Pub = priv.Public().(ed25519.PublicKey)
	l.Sig = ed25519.Sign(priv, l.Body())
	p := l.RouteProfile()
	if p.Name != "RU direct" || p.DNS != "77.88.8.8" || p.Serial != 20261027 {
		t.Fatalf("%+v", p)
	}
	if len(p.Sets) != 2 {
		t.Fatalf("unmapped via leaked: %+v", p.Sets)
	}
	if p.Sets[0].ID != "ru" || p.Sets[0].Label != "Russia" || p.Sets[0].Dir != DirDirect {
		t.Fatalf("%+v", p.Sets[0])
	}
	if SplitZone(p.Sets[0].Dir) != ZoneLocal {
		t.Fatal(SplitZone(p.Sets[0].Dir))
	}
	if len(p.Sets[0].CIDRs) != 1 || p.Sets[0].CIDRs[0] != "5.255.255.0/24" {
		t.Fatalf("%v", p.Sets[0].CIDRs)
	}
	if len(p.Sets[0].Domains) != 1 || p.Sets[0].Domains[0] != "yandex.ru" {
		t.Fatalf("%v", p.Sets[0].Domains)
	}
	if p.Sets[1].ID != "ads" || SplitZone(p.Sets[1].Dir) != ZoneBlock {
		t.Fatalf("%+v", p.Sets[1])
	}
}

func TestSplitZone(t *testing.T) {
	if SplitZone(DirVia) != ZoneGlobal || SplitZone("") != ZoneGlobal {
		t.Fatal("via")
	}
	if SplitZone(DirDirect) != ZoneLocal || SplitZone(DirDrop) != ZoneBlock {
		t.Fatal("direct/drop")
	}
}

func TestRouteProfileSplitJSON(t *testing.T) {
	priv := ed25519.NewKeyFromSeed([]byte("Liberty Of Free Transmission 1.0"))
	l := Lists{Serial: 20261027, Sets: []AddrList{
		{Name: "", Entries: []Addr{{Kind: KindLabel, Val: "RU direct"}, {Kind: KindDNS, Val: "77.88.8.8"}}},
		{Name: "ru", Entries: []Addr{{Kind: KindDir, Val: DirDirect}, {Kind: KindLabel, Val: "Russia"}, {Kind: KindCIDR, Val: "5.255.255.0/24"}, {Kind: KindSuffix, Val: "yandex.ru"}}},
		{Name: "ads", Entries: []Addr{{Kind: KindDir, Val: DirDrop}, {Kind: KindSuffix, Val: "doubleclick.net"}}},
		{Name: "via", Entries: []Addr{ParseAddr("10.0.0.0/8")}},
	}}
	l.Pub = priv.Public().(ed25519.PublicKey)
	l.Sig = ed25519.Sign(priv, l.Body())
	raw, err := l.RouteProfile().SplitJSON()
	if err != nil {
		t.Fatal(err)
	}
	var d struct {
		Name     string                      `json:"name"`
		DNS      string                      `json:"dns"`
		Serial   uint64                      `json:"serial"`
		Src      string                      `json:"src"`
		Zones    []struct{ ID, Name string } `json:"zones"`
		Services []struct {
			ID, Name, Zone, Src string
			CIDRs, Domains      []string
		} `json:"services"`
	}
	if err := json.Unmarshal(raw, &d); err != nil {
		t.Fatal(err)
	}
	if d.Name != "RU direct" || d.DNS != "77.88.8.8" || d.Serial != 20261027 || d.Src != "op" {
		t.Fatalf("%+v", d)
	}
	if len(d.Zones) != 3 || d.Zones[0].ID != ZoneLocal || d.Zones[1].ID != ZoneGlobal || d.Zones[2].ID != ZoneBlock {
		t.Fatalf("%+v", d.Zones)
	}
	if len(d.Services) != 2 {
		t.Fatalf("unmapped via leaked: %+v", d.Services)
	}
	if d.Services[0].ID != "ru" || d.Services[0].Zone != ZoneLocal || d.Services[0].Src != "op" {
		t.Fatalf("%+v", d.Services[0])
	}
	if d.Services[1].ID != "ads" || d.Services[1].Zone != ZoneBlock || d.Services[1].Src != "op" {
		t.Fatalf("%+v", d.Services[1])
	}
}

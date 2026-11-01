package netconf

// NormalizeFront protocol names; vendor CDN names are not fronts.

import "testing"

// TestNormalizeFront accepts get/ws/quic/h2/h3 and rejects yandex and wss.
func TestNormalizeFront(t *testing.T) {
	got, err := NormalizeFront("get")
	if err != nil || got != "get" {
		t.Fatalf("%s %v", got, err)
	}
	got, err = NormalizeFront("ws")
	if err != nil || got != "ws" {
		t.Fatalf("%s %v", got, err)
	}
	got, err = NormalizeFront("quic")
	if err != nil || got != "quic" {
		t.Fatalf("%s %v", got, err)
	}
	got, err = NormalizeFront("h2")
	if err != nil || got != "h2" {
		t.Fatalf("%s %v", got, err)
	}
	got, err = NormalizeFront("h3")
	if err != nil || got != "h3" {
		t.Fatalf("%s %v", got, err)
	}
	got, err = NormalizeFront("l3")
	if err != nil || got != "l3" {
		t.Fatalf("%s %v", got, err)
	}
	if _, err := NormalizeFront("yandex"); err == nil {
		t.Fatal("vendor names are not fronts")
	}
	if _, err := NormalizeFront("wss"); err == nil {
		t.Fatal("wss is not a front")
	}
}

// TestListenNormalizesFront has Validate canonicalize listen front get.
func TestListenNormalizesFront(t *testing.T) {
	n := &Network{ID: "n", CIDR: "10.8.0.0/24", Listen: []Listen{{Port: 443, Front: "get"}}}
	if err := n.Validate(); err != nil {
		t.Fatal(err)
	}
	if n.Listen[0].Front != "get" {
		t.Fatalf("got %q", n.Listen[0].Front)
	}
}

package transport

// Tests for named-list lookup priority: drop, then direct, then via, then default.

import (
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"testing"

	"github.com/a3app/loft/internal/config"
	"github.com/a3app/loft/internal/wire"
)

// TestLookupPriority checks drop wins over direct on the same host, then default via.
func TestLookupPriority(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	l := wire.Lists{
		Serial: 1,
		Sets: []wire.AddrList{
			{Name: "sber", Entries: []wire.Addr{wire.ParseAddr("online.sberbank.ru")}},
			{Name: "drop", Entries: []wire.Addr{wire.ParseAddr("online.sberbank.ru")}},
			{Name: "via", Entries: []wire.Addr{wire.ParseAddr("youtube.com")}},
		},
	}
	l.Pub = priv.Public().(ed25519.PublicKey)
	l.Sig = ed25519.Sign(priv, l.Body())
	SetUse(&config.ClientConfig{Use: map[string]string{"sber": "direct", "drop": "drop", "via": "via"}, Default: "via"})
	RememberLists(l)
	if LookupAddr("online.sberbank.ru", nil) != ActDrop {
		t.Fatal("drop wins")
	}
	if LookupAddr("youtube.com", nil) != ActVia {
		t.Fatal("youtube")
	}
	if LookupAddr("example.com", nil) != ActVia {
		t.Fatal("default")
	}
	if LookupAddr("", net.ParseIP("8.8.8.8")) != ActVia {
		t.Fatal("ip default")
	}
}

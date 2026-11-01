package origin

import (
	"crypto/ed25519"
	"crypto/rand"
	"testing"

	"github.com/a3app/loft/internal/config"
	"github.com/a3app/loft/internal/wire"
)

func TestSignListsMeta(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	cfg := &config.ServerConfig{Lists: map[string][]string{
		"@":   {"@label RU direct", "@dns 77.88.8.8"},
		"ru":  {"@dir direct", "@label Russia", "5.255.255.0/24", ".yandex.ru"},
		"ads": {"@dir drop", ".doubleclick.net"},
		"via": {"10.0.0.0/8"},
	}}
	blob, ser, _ := signLists(cfg, priv)
	if len(blob) == 0 || ser == 0 {
		t.Fatal("empty")
	}
	l, err := wire.DecodeLists(blob)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.Verify(); err != nil {
		t.Fatal(err)
	}
	if len(l.Sets) != 4 {
		t.Fatalf("sets %d", len(l.Sets))
	}
	if l.Sets[0].Name != "" {
		t.Fatalf("header first, got %q", l.Sets[0].Name)
	}
	p := l.RouteProfile()
	if p.Name != "RU direct" || p.DNS != "77.88.8.8" {
		t.Fatalf("%+v", p)
	}
	if len(p.Sets) != 2 {
		t.Fatalf("%+v", p.Sets)
	}
	dir := map[string]string{}
	for _, s := range p.Sets {
		dir[s.ID] = s.Dir
	}
	if dir["ru"] != wire.DirDirect || dir["ads"] != wire.DirDrop {
		t.Fatalf("%+v", p.Sets)
	}
}

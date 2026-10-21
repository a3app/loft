package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRelayAndCDNCommands(t *testing.T) {
	c, err := ParseCommands("role controller\nlisten :443\nrelay de-1 url https://de1.example.net fronts get,ws weight 5 drain\ncdn_peer 173.245.48.0/20\n")
	if err != nil {
		t.Fatal(err)
	}
	if len(c.Relays) != 1 || c.Relays[0].URL != "https://de1.example.net" || !c.Relays[0].Drain || c.Relays[0].Weight != 5 || len(c.Relays[0].Fronts) != 2 {
		t.Fatalf("relay %+v", c.Relays)
	}
	if len(c.CDNPeers) != 1 {
		t.Fatalf("cdn %v", c.CDNPeers)
	}
	out := RenderCommands(*c, false)
	c2, err := ParseCommands(out)
	if err != nil || len(c2.Relays) != 1 || c2.Relays[0].Weight != 5 || !strings.Contains(out, "cdn_peer 173.245.48.0/20") {
		t.Fatalf("round trip: %v\n%s", err, out)
	}
	for _, bad := range []string{"relay", "relay x weight 300", "relay x frob 1", "cdn_peer nope"} {
		if _, err := ParseCommands("role controller\n" + bad + "\n"); err == nil {
			t.Fatalf("%q parsed", bad)
		}
	}
}

func TestListsFile(t *testing.T) {
	p := filepath.Join(t.TempDir(), "lists.conf")
	m := map[string][]string{"blocked": {".example.ru", "198.51.100.0/24"}, "local": {"router.lan"}}
	os.WriteFile(p, []byte(RenderLists(m)), 0o644)
	got, err := LoadLists(p)
	if err != nil || len(got["blocked"]) != 2 || got["local"][0] != "router.lan" {
		t.Fatalf("%v %v", got, err)
	}
	if m, err := LoadLists(filepath.Join(t.TempDir(), "none")); err != nil || m != nil {
		t.Fatal("missing file")
	}
	os.WriteFile(p, []byte("only-one-word\n"), 0o644)
	if _, err := LoadLists(p); err == nil {
		t.Fatal("bad line parsed")
	}
}

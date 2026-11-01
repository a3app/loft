package adm

// Relay keys: issued into relays.keys with the hash on the relay line, kept
// across a node.conf save, gone with the relay.

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/a3app/loft/internal/config"
	"github.com/a3app/loft/internal/identity"
	"github.com/a3app/loft/server/panel/admproto"
)

func TestRelayKeyIssueKeepPrune(t *testing.T) {
	o, fn, _, _ := setup(t)
	o.Etc = t.TempDir()
	fn.reload = true
	os.WriteFile(filepath.Join(o.Etc, "node.conf"), []byte(nodeConf0+"relay ru1 url https://ru1.example.net\n"), 0o640)
	os.WriteFile(filepath.Join(o.Etc, "loft.env"), []byte("LOFT_TOKEN=test-token-please-change\n"), 0o640)
	ctx := context.Background()

	if _, err := o.RelayKey(ctx, admproto.RelayKeyReq{ID: "nope"}); err == nil {
		t.Fatal("key for a relay not in node.conf")
	}
	k, err := o.RelayKey(ctx, admproto.RelayKeyReq{ID: "ru1"})
	if err != nil || !strings.HasPrefix(k.Key, "lt_") {
		t.Fatalf("issue: %+v %v", k, err)
	}
	ring, _ := identity.Load(filepath.Join(o.Dir, "relays.keys"))
	if string(ring.TokenFor("ru1")) != k.Key {
		t.Fatal("key not in relays.keys")
	}
	conf := func() *config.ServerConfig {
		b, _ := os.ReadFile(filepath.Join(o.Etc, "node.conf"))
		c, err := config.ParseCommands(string(b))
		if err != nil {
			t.Fatal(err)
		}
		return c
	}
	if c := conf(); len(c.Relays) != 1 || c.Relays[0].KeySHA256 != k.KeySHA256 {
		t.Fatalf("hash not on the relay line: %+v", c.Relays)
	}
	if got, err := o.RelayGet(admproto.RelayKeyReq{ID: "ru1"}); err != nil || got.Key != k.Key {
		t.Fatalf("get: %+v %v", got, err)
	}

	// A save from the panel (which never sends keys) keeps the hash.
	v, err := o.NodeConfig(ctx)
	if err != nil || len(v.Relays) != 1 || !v.Relays[0].Key {
		t.Fatalf("view: %+v %v", v.Relays, err)
	}
	v.Relays[0].Weight = 20
	if _, err := o.NodeSave(ctx, v); err != nil {
		t.Fatal(err)
	}
	if c := conf(); c.Relays[0].KeySHA256 != k.KeySHA256 || c.Relays[0].Weight != 20 {
		t.Fatalf("save lost the key hash: %+v", c.Relays)
	}

	// Removing the relay removes its key.
	v.Relays = nil
	if _, err := o.NodeSave(ctx, v); err != nil {
		t.Fatal(err)
	}
	ring, _ = identity.Load(filepath.Join(o.Dir, "relays.keys"))
	if ring.Len() != 0 {
		t.Fatal("removed relay kept its key")
	}
}

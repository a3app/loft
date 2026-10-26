package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/a3app/loft/internal/config"
)

func TestPromoteStandby(t *testing.T) {
	dir, etc := t.TempDir(), t.TempDir()
	sb := filepath.Join(dir, "standby")
	os.MkdirAll(sb, 0o700)
	ctlConf := "role controller\nlisten :443\nadvertise https://ctl.example\ncert /c/full.pem\nkey /c/key.pem\nfronts get ws\ndecoy ctl-site.example:443\n" +
		"relay ru1 url https://ru1.example key_sha256 aa\nrelay r2 url https://r2.example key_sha256 bb replica\n"
	for n, b := range map[string]string{"node.conf": ctlConf, "members.keys": "alice: lt_a\n", "relays.keys": "ru1: lt_r\n",
		"controller.seed": "0123456789abcdef0123456789abcdef", "network.conf": "network loft\n", "loft.token": "ctl-token", "received": "now"} {
		os.WriteFile(filepath.Join(sb, n), []byte(b), 0o600)
	}
	cfgPath, envPath := filepath.Join(etc, "node.conf"), filepath.Join(etc, "loft.env")
	mine := "role replica\nid r2\nlisten :8443\nadvertise https://r2.example\ncontroller wss://ctl.example/api/v1/sync\ncontroller_pub " + strings.Repeat("ab", 32) +
		"\ncert /r/full.pem\nkey /r/key.pem\ndecoy r2-site.example:443\nfronts get\n"
	os.WriteFile(cfgPath, []byte(mine), 0o640)
	os.WriteFile(envPath, []byte("LOFT_TOKEN=replica-own\nOTHER=1\n"), 0o640)

	if err := promote(dir, cfgPath, envPath); err != nil {
		t.Fatal(err)
	}
	c, err := config.LoadServer(cfgPath)
	if err != nil {
		t.Fatal(err)
	}
	if c.Role != "controller" || c.Advertise != "https://r2.example" || c.Listen != ":8443" || c.CertFile != "/r/full.pem" ||
		len(c.Decoy) != 1 || c.Decoy[0] != "r2-site.example:443" || c.Controller != "" {
		t.Fatalf("promoted node.conf: %+v", c)
	}
	if len(c.Relays) != 1 || c.Relays[0].ID != "ru1" || c.Relays[0].KeySHA256 != "aa" {
		t.Fatalf("relays after promote: %+v", c.Relays)
	}
	for n, want := range map[string]string{"members.keys": "alice: lt_a\n", "relays.keys": "ru1: lt_r\n", "controller.seed": "0123456789abcdef0123456789abcdef"} {
		if b, _ := os.ReadFile(filepath.Join(dir, n)); string(b) != want {
			t.Fatalf("%s not taken over: %q", n, b)
		}
	}
	env, _ := os.ReadFile(envPath)
	if !strings.Contains(string(env), "LOFT_TOKEN=ctl-token") || !strings.Contains(string(env), "OTHER=1") || strings.Contains(string(env), "replica-own") {
		t.Fatalf("env: %s", env)
	}
	// Once promoted it is no longer a standby.
	if err := promote(dir, cfgPath, envPath); err == nil {
		t.Fatal("promoted twice")
	}
}

func TestPromoteNeedsState(t *testing.T) {
	dir, etc := t.TempDir(), t.TempDir()
	cfgPath := filepath.Join(etc, "node.conf")
	os.WriteFile(cfgPath, []byte("role replica\nid r2\n"), 0o640)
	if err := promote(dir, cfgPath, filepath.Join(etc, "loft.env")); err == nil {
		t.Fatal("promoted without the controller's state")
	}
}

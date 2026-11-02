package adm

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/a3app/loft/server/panel/admproto"
	"github.com/a3app/loft/server/panel/catalog"
)

func vlessTree(t *testing.T, o *Ops) {
	t.Helper()
	p, ok := catalog.Find("vless")
	if !ok {
		t.Fatal("no vless package")
	}
	os.MkdirAll(filepath.Join(o.Root, p.Release.Dir), 0o755)
	os.WriteFile(filepath.Join(o.Root, p.Release.Dir, p.Release.Bin), []byte("x"), 0o755)
	os.MkdirAll(filepath.Join(o.Root, "etc/systemd/system"), 0o755)
	os.WriteFile(filepath.Join(o.Root, "etc/systemd/system", p.Unit), []byte("u"), 0o644)
}

func TestVLESSInbound(t *testing.T) {
	o, _ := pkgOps(t)
	ctx := context.Background()
	if g, err := o.VLESSGet(); err != nil || g.Installed {
		t.Fatalf("not installed %+v %v", g, err)
	}
	vlessTree(t, o)
	if err := o.ensureVLESS(); err != nil {
		t.Fatal(err)
	}
	g, err := o.VLESSGet()
	if err != nil || !g.Installed || g.Port != 4433 || g.Dest != vlessDest || g.PublicKey == "" || g.ShortID == "" {
		t.Fatalf("get %+v %v", g, err)
	}
	conf, err := os.ReadFile(o.vlessConfFile())
	if err != nil || !strings.Contains(string(conf), `"security": "reality"`) || !strings.Contains(string(conf), "realitySettings") {
		t.Fatalf("json %v %s", err, conf)
	}
	if _, err := o.VLESSSet(ctx, admproto.VLESSSet{Port: 443}); err == nil || !strings.Contains(err.Error(), "loft-node") {
		t.Fatalf("port 443: %v", err)
	}
	got, err := o.VLESSSet(ctx, admproto.VLESSSet{Port: 8444, Dest: "www.cloudflare.com"})
	if err != nil || got.Port != 8444 || got.Dest != "www.cloudflare.com:443" || got.SNI != "www.cloudflare.com" {
		t.Fatalf("set %+v %v", got, err)
	}
	oldPBK := got.PublicKey
	rot, err := o.VLESSSet(ctx, admproto.VLESSSet{RotateKeys: true})
	if err != nil || rot.PublicKey == "" || rot.PublicKey == oldPBK {
		t.Fatalf("rotate %+v %v", rot, err)
	}
	sh, err := o.VLESSClient(ctx, admproto.VLESSClient{ID: "alice", Mint: true})
	if err != nil || !vlessUUID.MatchString(sh.UUID) {
		t.Fatalf("mint %+v %v", sh, err)
	}
	again, err := o.VLESSClient(ctx, admproto.VLESSClient{ID: "alice", Mint: true})
	if err != nil || again.UUID != sh.UUID {
		t.Fatalf("keep uuid %+v %v", again, err)
	}
	s, err := o.readVLESS()
	if err != nil {
		t.Fatal(err)
	}
	raw := vlessShare(s, "vpn.example.net", sh.UUID, "alice")
	link, err := ParseXrayLink(raw)
	if err != nil || link.Protocol != "vless" || link.Port != 8444 || link.Security != "reality" || link.Flow != vlessFlow {
		t.Fatalf("share %v %+v %s", err, link, raw)
	}
	conf, _ = os.ReadFile(o.vlessConfFile())
	if !strings.Contains(string(conf), sh.UUID) || !strings.Contains(string(conf), vlessFlow) {
		t.Fatalf("uuid missing in inbound %s", conf)
	}
	drop, err := o.VLESSClient(ctx, admproto.VLESSClient{ID: "alice", Drop: true})
	if err != nil || drop.UUID != "" {
		t.Fatalf("drop %+v %v", drop, err)
	}
	conf, _ = os.ReadFile(o.vlessConfFile())
	if strings.Contains(string(conf), sh.UUID) {
		t.Fatal("uuid left after drop")
	}
}

func TestVLESSDestParse(t *testing.T) {
	got, err := parseVLESSDest("www.Microsoft.com")
	if err != nil || got != "www.microsoft.com:443" {
		t.Fatalf("%q %v", got, err)
	}
	got, err = parseVLESSDest("1.2.3.4:8443")
	if err != nil || got != "1.2.3.4:8443" {
		t.Fatalf("%q %v", got, err)
	}
	if _, err := parseVLESSDest("nope"); err == nil {
		t.Fatal("want error")
	}
	if err := checkVLESSPort(80); err == nil {
		t.Fatal("port 80")
	}
}

func TestVLESSConfigStable(t *testing.T) {
	s := vlessFile{Port: 4433, Dest: vlessDest, PrivateKey: "priv", PublicKey: "pub", ShortID: "abcd"}
	clients := map[string]string{"bob": "bbbbbbbb-bbbb-bbbb-bbbb-bbbbbbbbbbbb", "alice": "aaaaaaaa-aaaa-aaaa-aaaa-aaaaaaaaaaaa"}
	a, err := vlessConfig(s, clients)
	if err != nil {
		t.Fatal(err)
	}
	b, err := vlessConfig(s, clients)
	if err != nil {
		t.Fatal(err)
	}
	if string(a) != string(b) {
		t.Fatal("unstable json")
	}
	var raw map[string]any
	if err := json.Unmarshal(a, &raw); err != nil {
		t.Fatal(err)
	}
}

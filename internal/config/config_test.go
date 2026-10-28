package config

// Node.conf command parse, YAML fallback, and RenderCommands round-trip.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/a3app/loft/internal/wire"
)

// TestLoadServerTokenFromEnv leaves token empty (filled later from LOFT_TOKEN).
func TestLoadServerTokenFromEnv(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "node.conf")
	if err := os.WriteFile(p, []byte("listen :443\nrole controller\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := LoadServer(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Token != "" {
		t.Fatalf("token %q, want empty (filled later from LOFT_TOKEN)", c.Token)
	}
	if c.Listen != ":443" {
		t.Fatalf("listen %q", c.Listen)
	}
	if c.Role != "controller" {
		t.Fatalf("role %q", c.Role)
	}
	if got := c.EnabledFronts(); len(got) != 1 || got[0] != "get" {
		t.Fatalf("default fronts %v", got)
	}
}

// TestLoadServerYAMLFallback still loads a legacy node.yaml mapping.
func TestLoadServerYAMLFallback(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "node.yaml")
	if err := os.WriteFile(p, []byte("listen: \":443\"\nrole: controller\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := LoadServer(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen != ":443" || c.Role != "controller" {
		t.Fatalf("%+v", c)
	}
}

// TestRenderCommandsRoundTrip parses what RenderCommands writes.
func TestRenderCommandsRoundTrip(t *testing.T) {
	in := ServerConfig{
		Role: "controller", Listen: ":444", Advertise: "https://de1.example.com",
		Decoy: []string{"origin.example:443"}, Mask: true,
		Fronts: []string{"get", "ws", "h2"},
		Relays: []RelayEntry{{ID: "ru1", URL: "https://ru1.example", KeySHA256: "ab12"}},
		GetH2:  2,
		Canary: CanaryConfig{SplitHello: false, Pad: 32},
	}
	on := true
	in.Canary.FatHello = &on
	got, err := ParseCommands(RenderCommands(in))
	if err != nil {
		t.Fatal(err)
	}
	if got.Listen != ":444" || got.Advertise != in.Advertise || !got.Mask {
		t.Fatalf("%+v", got)
	}
	if strings.Join(got.Fronts, ",") != "get,ws,h2" {
		t.Fatalf("fronts %v", got.Fronts)
	}
	if got.GetH2 != 2 {
		t.Fatalf("get_h2 %d", got.GetH2)
	}
	if got.AuthShared {
		t.Fatal("default auth shared")
	}
	in.AuthShared = true
	got, err = ParseCommands(RenderCommands(in))
	if err != nil {
		t.Fatal(err)
	}
	if !got.AuthShared {
		t.Fatal("auth shared round-trip")
	}
}

func TestAuthSharedCommand(t *testing.T) {
	c, err := ParseCommands("listen :443\nrole controller\nauth shared enable\n")
	if err != nil {
		t.Fatal(err)
	}
	if !c.AuthShared {
		t.Fatal("auth shared enable")
	}
	out := RenderCommands(*c)
	if !strings.Contains(out, "auth shared enable\n") {
		t.Fatal(out)
	}
	off, err := ParseCommands("listen :443\nrole controller\nauth shared disable\n")
	if err != nil {
		t.Fatal(err)
	}
	if off.AuthShared {
		t.Fatal("auth shared disable")
	}
	if _, err := ParseCommands("auth enable\n"); err == nil {
		t.Fatal("want error")
	}
}

func TestInnerCommand(t *testing.T) {
	c, err := ParseCommands("listen :443\nrole controller\ninner clear aes\n")
	if err != nil {
		t.Fatal(err)
	}
	ids, err := c.InnerIDs()
	if err != nil {
		t.Fatal(err)
	}
	if len(ids) != 2 || ids[0] != wire.InnerClear || ids[1] != wire.InnerAES {
		t.Fatalf("ids %v", ids)
	}
	out := RenderCommands(*c)
	if !strings.Contains(out, "inner clear aes\n") {
		t.Fatal(out)
	}
	if _, err := ParseCommands("listen :443\ninner nope\n"); err == nil {
		t.Fatal("bad name")
	}
	if _, err := ParseCommands("inner\n"); err == nil {
		t.Fatal("empty inner")
	}
	only, err := ParseCommands("listen :443\ninner chacha\n")
	if err != nil {
		t.Fatal(err)
	}
	ids, err = only.InnerIDs()
	if err != nil || len(ids) != 0 {
		t.Fatalf("chacha %v %v", ids, err)
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "node.yaml")
	if err := os.WriteFile(p, []byte("listen: \":443\"\nrole: controller\ninner: [clear, aes]\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	loaded, err := LoadServer(p)
	if err != nil {
		t.Fatal(err)
	}
	ids, err = loaded.InnerIDs()
	if err != nil || len(ids) != 2 || ids[0] != wire.InnerClear || ids[1] != wire.InnerAES {
		t.Fatalf("yaml %v %v", ids, err)
	}
	ask := &ClientConfig{Inner: "clear"}
	set, id, err := ask.InnerAsk()
	if err != nil || !set || id != wire.InnerClear {
		t.Fatalf("ask %v %d %v", set, id, err)
	}
	ask.Inner = "chacha"
	set, _, err = ask.InnerAsk()
	if err != nil || set {
		t.Fatalf("chacha ask %v %v", set, err)
	}
	ask.Inner = ""
	set, _, err = ask.InnerAsk()
	if err != nil || set {
		t.Fatalf("empty ask %v %v", set, err)
	}
	ask.Inner = "nope"
	if _, _, err := ask.InnerAsk(); err == nil {
		t.Fatal("bad ask")
	}
}

func TestLoadServerAuthSharedYAML(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "node.yaml")
	if err := os.WriteFile(p, []byte("listen: \":443\"\nrole: controller\nauth_shared: true\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := LoadServer(p)
	if err != nil {
		t.Fatal(err)
	}
	if !c.AuthShared {
		t.Fatal("yaml auth_shared")
	}
}

func TestTraceCommand(t *testing.T) {
	c, err := ParseCommands("listen :443\nrole controller\ntrace /var/log/loft-sess.log\n")
	if err != nil {
		t.Fatal(err)
	}
	if c.Trace != "/var/log/loft-sess.log" {
		t.Fatalf("trace %q", c.Trace)
	}
	out := RenderCommands(*c)
	if !strings.Contains(out, "trace /var/log/loft-sess.log\n") {
		t.Fatal(out)
	}
}

func TestLoadClientH2MaxAge(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "client.yaml")
	body := "token: t\noutbounds:\n  - name: x\n    type: get\n    url: https://example.com\n    h2_max_age_ms: 60000\n"
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := LoadClient(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Outbounds[0].H2MaxAgeMS != 60000 {
		t.Fatalf("h2_max_age_ms %d", c.Outbounds[0].H2MaxAgeMS)
	}
	if got := c.EnabledFronts(); len(got) != 1 || got[0] != "get" {
		t.Fatalf("client fronts %v", got)
	}
}

func TestLoadClientTofu(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "client.yaml")
	body := "token: t\noutbounds:\n  - name: x\n    type: get\n    url: https://example.com\n"
	if err := os.WriteFile(p, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := LoadClient(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Tofu {
		t.Fatal("default tofu")
	}
	if err := os.WriteFile(p, []byte(body+"tofu: true\ncontroller_pub: aabb\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err = LoadClient(p)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Tofu || c.ControllerPub != "aabb" {
		t.Fatalf("tofu=%v pub=%q", c.Tofu, c.ControllerPub)
	}
}

func TestLoadServerTraceYAML(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "node.yaml")
	if err := os.WriteFile(p, []byte("listen: \":443\"\nrole: controller\ntrace: /tmp/loft-sess.log\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := LoadServer(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Trace != "/tmp/loft-sess.log" {
		t.Fatalf("trace %q", c.Trace)
	}
}

func TestDecoyProxyCommand(t *testing.T) {
	c, err := ParseCommands("listen :443\ndecoy 127.0.0.1:8443\ndecoy_proxy enable\n")
	if err != nil {
		t.Fatal(err)
	}
	if !c.DecoyProxy {
		t.Fatal("decoy_proxy enable")
	}
	if out := RenderCommands(*c); !strings.Contains(out, "decoy 127.0.0.1:8443\ndecoy_proxy enable\n") {
		t.Fatal(out)
	}
	off, _ := ParseCommands("listen :443\ndecoy 127.0.0.1:8443\n")
	if off.DecoyProxy || strings.Contains(RenderCommands(*off), "decoy_proxy") {
		t.Fatal("decoy_proxy must default off and not render")
	}
	if _, err := ParseCommands("decoy_proxy maybe\n"); err == nil {
		t.Fatal("want error")
	}
}

func TestLoadClientListenHTTPUplink(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "client.yaml")
	body := []byte(`listen:
  socks5: 0.0.0.0:1080
  tun: auto
  http: 0.0.0.0:8090
  bind: eth0
  uplink: ether1
token: "lt_test"
outbounds:
  - name: node
    type: get
    url: https://vpn.example.net
`)
	if err := os.WriteFile(p, body, 0o600); err != nil {
		t.Fatal(err)
	}
	c, err := LoadClient(p)
	if err != nil {
		t.Fatal(err)
	}
	if c.Listen.HTTP != "0.0.0.0:8090" || c.Listen.Bind != "eth0" || c.Listen.Uplink != "ether1" {
		t.Fatalf("%+v", c.Listen)
	}
	c.Listen.Uplink = "lte1"
	if err := WriteClient(p, c); err != nil {
		t.Fatal(err)
	}
	c2, err := LoadClient(p)
	if err != nil {
		t.Fatal(err)
	}
	if c2.Listen.Uplink != "lte1" {
		t.Fatalf("uplink %q", c2.Listen.Uplink)
	}
}

func TestValidIFName(t *testing.T) {
	if err := ValidIFName(""); err != nil {
		t.Fatal(err)
	}
	if err := ValidIFName("ether1"); err != nil {
		t.Fatal(err)
	}
	if err := ValidIFName("pppoe-out1"); err != nil {
		t.Fatal(err)
	}
	if ValidIFName("ether1; /system/reset") == nil {
		t.Fatal("want reject")
	}
	if ValidIFName("ether 1") == nil {
		t.Fatal("want reject")
	}
}

func TestCCCommand(t *testing.T) {
	c, err := ParseCommands("listen :443\ncc bbr fixed\ncc_max 50 200\n")
	if err != nil {
		t.Fatal(err)
	}
	ids, err := c.CCIDs()
	if err != nil || len(ids) != 2 || ids[0] != wire.CCBBR || ids[1] != wire.CCFixed {
		t.Fatalf("ids %v %v", ids, err)
	}
	if c.CCMaxUpMbps != 50 || c.CCMaxDownMbps != 200 {
		t.Fatalf("max %d %d", c.CCMaxUpMbps, c.CCMaxDownMbps)
	}
	out := RenderCommands(*c)
	if !strings.Contains(out, "cc bbr fixed\n") || !strings.Contains(out, "cc_max 50 200\n") {
		t.Fatal(out)
	}
	for _, bad := range []string{"cc nope\n", "cc\n", "cc_max 1\n", "cc_max a b\n"} {
		if _, err := ParseCommands("listen :443\n" + bad); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
	if off, _ := ParseCommands("listen :443\ncc off\n"); off == nil {
		t.Fatal("cc off")
	} else if ids, _ := off.CCIDs(); len(ids) != 0 {
		t.Fatalf("off %v", ids)
	}
	ask := &ClientConfig{CC: "fixed", CCUpMbps: 10, CCDownMbps: 100}
	id, up, down, err := ask.CCAsk()
	if err != nil || id != wire.CCFixed || up != 10000 || down != 100000 {
		t.Fatalf("ask %d %d %d %v", id, up, down, err)
	}
	ask.CC = "bbr"
	if id, up, _, _ := ask.CCAsk(); id != wire.CCBBR || up != 0 {
		t.Fatal("bbr ask carries rates")
	}
	ask.CC = ""
	if id, _, _, _ := ask.CCAsk(); id != wire.CCDefault {
		t.Fatal("empty ask")
	}
}

// TestValidGatewayMode: listen.gateway takes nat, route or nothing; a typo
// is refused instead of silently acting as nat.
func TestValidGatewayMode(t *testing.T) {
	for _, ok := range []string{"", "nat", "route"} {
		if err := ValidGatewayMode(ok); err != nil {
			t.Errorf("%q refused: %v", ok, err)
		}
	}
	for _, bad := range []string{"NAT", "routed", "masquerade"} {
		if ValidGatewayMode(bad) == nil {
			t.Errorf("%q accepted", bad)
		}
	}
}

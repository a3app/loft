package adm

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/a3app/loft/server/panel/admproto"
)

const nodeConf0 = `role controller
listen :443
cert /etc/loft/tls/fullchain.pem
key /etc/loft/tls/privkey.pem
get_path /static/js
ws_path /api/v1/sync
h2_path /api/v1/tunnel
h3_path /api/v1/tunnel
advertise https://api.example.net
roster_path /static/v3/manifest.json
lists_path /static/v3/lists.json
enrol list
auth shared disable
keep_keys enable
mask disable
decoy 127.0.0.1:8443
decoy_proxy enable
`

func TestNodeSave(t *testing.T) {
	o, fn, _, _ := setup(t)
	o.Etc = t.TempDir()
	fn.reload = true
	os.WriteFile(filepath.Join(o.Etc, "node.conf"), []byte(nodeConf0), 0o640)
	ctx := context.Background()
	os.WriteFile(filepath.Join(o.Etc, "loft.env"), []byte("LOFT_TOKEN=test-token-please-change\n"), 0o640)
	v, err := o.NodeConfig(ctx)
	if err != nil || v.Listen != ":443" || len(v.Fronts) != 1 || v.Fronts[0] != "get" || v.Decoy[0] != "127.0.0.1:8443" {
		t.Fatalf("%+v %v", v, err)
	}
	if v.MaskPub == "" {
		t.Fatal("mask_pub empty with token")
	}
	in := v
	in.Decoy = []string{"127.0.0.1:9000"}
	if _, err := o.NodeSave(ctx, in); err == nil {
		t.Fatal("panel decoy replaced")
	}
	in = v
	in.GetPath = in.WSPath
	if _, err := o.NodeSave(ctx, in); err == nil {
		t.Fatal("same path twice")
	}
	// Relays only: a reload, not a restart.
	in = v
	in.Relays = []admproto.Relay{{ID: "de-1", URL: "https://de1.example.net", Weight: 10}}
	fn.calls = nil
	if _, err := o.NodeSave(ctx, in); err != nil {
		t.Fatal(err)
	}
	if got := strings.Join(fn.calls, "\n"); !strings.Contains(got, "reload loft-node.service") || strings.Contains(got, "restart") {
		t.Fatalf("calls %v", fn.calls)
	}
	b, _ := os.ReadFile(filepath.Join(o.Etc, "node.conf"))
	if !strings.Contains(string(b), "relay de-1 url https://de1.example.net weight 10") || !strings.Contains(string(b), "decoy_proxy enable") || strings.Contains(string(b), "keep_keys") {
		t.Fatalf("node.conf:\n%s", b)
	}
	// Fronts: a restart.
	in.Fronts = []string{"get", "ws"}
	fn.calls = nil
	if _, err := o.NodeSave(ctx, in); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(fn.calls, "\n"), "restart loft-node.service") {
		t.Fatalf("calls %v", fn.calls)
	}
	// A node that does not come back gets the old file.
	fn.active = func() string { return "failed" }
	in.Mask = true
	if _, err := o.NodeSave(ctx, in); err == nil {
		t.Fatal("failed restart accepted")
	}
	b2, _ := os.ReadFile(filepath.Join(o.Etc, "node.conf"))
	if strings.Contains(string(b2), "mask enable") {
		t.Fatal("not rolled back")
	}
	// Lists.
	fn.active = func() string { return "active" }
	l, err := o.NodeLists(ctx, admproto.NodeLists{Lists: map[string][]string{"blocked": {".example.ru", "198.51.100.0/24"}}})
	if err != nil || len(l.Lists["blocked"]) != 2 {
		t.Fatalf("%v %v", l.Lists, err)
	}
	if _, err := o.NodeLists(ctx, admproto.NodeLists{Lists: map[string][]string{"Bad Name": {"x"}}}); err == nil {
		t.Fatal("bad list name")
	}
}

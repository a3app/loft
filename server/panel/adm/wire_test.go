package adm

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/a3app/loft/internal/config"
)

const nodeConf = `role controller
listen :443
# operator note kept across the edit
cert /etc/loft/tls/fullchain.pem
key /etc/loft/tls/privkey.pem
mask disable
`

func wire(t *testing.T, body string) (string, bool, string, error) {
	t.Helper()
	p := filepath.Join(t.TempDir(), "node.conf")
	os.WriteFile(p, []byte(body), 0o640)
	ch, note, err := WireNode(p, "127.0.0.1:8443")
	b, _ := os.ReadFile(p)
	return string(b), ch, note, err
}

func TestWireNodeAdds(t *testing.T) {
	out, ch, _, err := wire(t, nodeConf)
	if err != nil || !ch {
		t.Fatalf("changed=%v err=%v", ch, err)
	}
	c, err := config.ParseCommands(out)
	if err != nil || len(c.Decoy) != 1 || c.Decoy[0] != "127.0.0.1:8443" || !c.DecoyProxy {
		t.Fatalf("result %+v %v\n%s", c, err, out)
	}
	if !strings.Contains(out, "# operator note kept") || !strings.HasPrefix(out, nodeConf) {
		t.Fatalf("original text not kept:\n%s", out)
	}
	p := filepath.Join(t.TempDir(), "node.conf")
	os.WriteFile(p, []byte(out), 0o640)
	if ch, note, err := WireNode(p, "127.0.0.1:8443"); ch || err != nil || note != "already wired" {
		t.Fatalf("second run: %v %q %v", ch, note, err)
	}
}

func TestWireNodeProxyOnly(t *testing.T) {
	out, ch, note, err := wire(t, nodeConf+"decoy 127.0.0.1:8443\n")
	if err != nil || !ch || note != "added: decoy_proxy enable" || strings.Count(out, "decoy 127.0.0.1:8443") != 1 {
		t.Fatalf("%v %q %v\n%s", ch, note, err, out)
	}
}

func TestWireNodeForeignDecoy(t *testing.T) {
	in := nodeConf + "decoy www.bank.example:443\n"
	out, ch, note, err := wire(t, in)
	if err != nil || ch || out != in || !strings.Contains(note, "left as is") {
		t.Fatalf("foreign decoy touched: %v %q %v", ch, note, err)
	}
}

func TestWireNodeYAMLAndBroken(t *testing.T) {
	in := "role: controller\nlisten: \":443\"\n"
	out, ch, note, err := wire(t, in)
	if err != nil || ch || out != in || !strings.Contains(note, "YAML") {
		t.Fatalf("yaml: %v %q %v", ch, note, err)
	}
	if _, _, _, err := wire(t, "frobnicate\n"); err == nil {
		t.Fatal("broken node.conf accepted")
	}
}

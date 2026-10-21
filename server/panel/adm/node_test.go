package adm

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/a3app/loft/server/panel/admproto"
)

const testNodeConf = `role controller
listen :443
cert /etc/loft/tls/fullchain.pem
key /etc/loft/tls/privkey.pem
get_path /static/js
ws_path /api/v1/sync
advertise https://old.example.net
# kept comment
decoy 127.0.0.1:8443
decoy_proxy enable
`

const testSite = `server {
	listen 80;
	server_name old.example.net;
}
server {
	listen 127.0.0.1:8443 ssl http2 proxy_protocol;
	server_name old.example.net;
}
`

func nodeSetup(t *testing.T) (*Ops, *fakeNode) {
	t.Helper()
	o, fn, _, _ := setup(t)
	etc := t.TempDir()
	o.Etc = etc
	o.NginxSite = filepath.Join(etc, "site.conf")
	o.PanelEnv = filepath.Join(etc, "panel.env")
	o.NginxTest = func() error { return nil }
	o.PanelTLS = filepath.Join(etc, "panel-tls")
	os.MkdirAll(filepath.Join(etc, "tls"), 0o750)
	os.WriteFile(filepath.Join(etc, "node.conf"), []byte(testNodeConf), 0o640)
	os.WriteFile(o.NginxSite, []byte(testSite), 0o644)
	os.WriteFile(o.PanelEnv, []byte("PANEL_LISTEN=127.0.0.1:8091\nPANEL_HOSTS=old.example.net\n"), 0o640)
	os.WriteFile(filepath.Join(o.Dir, "controller.seed"), make([]byte, 32), 0o600)
	return o, fn
}

func TestNodeInfo(t *testing.T) {
	o, _ := nodeSetup(t)
	in, err := o.NodeInfo()
	if err != nil {
		t.Fatal(err)
	}
	if in.Host != "old.example.net" || in.GetPath != "/static/js" || in.WSPath != "/api/v1/sync" || in.Panel != "old.example.net" || in.RosterPath == "" {
		t.Fatalf("%+v", in)
	}
	// ed25519 public key of the all-zero seed
	if in.ControllerPub != "3b6a27bcceb6a42d62a3a8d02a6f0d73653215771de243a63ac048a18b59da29" {
		t.Fatalf("pub %s", in.ControllerPub)
	}
}

func TestDomainSet(t *testing.T) {
	o, fn := nodeSetup(t)
	in, err := o.DomainSet(context.Background(), admproto.DomainSet{Name: "New.Example.NET."})
	if err != nil {
		t.Fatal(err)
	}
	if in.Host != "new.example.net" || in.Panel != "new.example.net" {
		t.Fatalf("%+v", in)
	}
	nc, _ := os.ReadFile(filepath.Join(o.Etc, "node.conf"))
	site, _ := os.ReadFile(o.NginxSite)
	if !strings.Contains(string(nc), "advertise https://new.example.net\n# kept comment") || strings.Count(string(site), "server_name new.example.net;") != 2 {
		t.Fatalf("files:\n%s\n%s", nc, site)
	}
	if !contains(fn.calls, "reload nginx") || !contains(fn.calls, "restart loft-node.service") {
		t.Fatalf("calls %v", fn.calls)
	}
	for _, bad := range []string{"", "localhost", "203.0.113.1", "a b.example", "x.example.net:443", "-a.example.net"} {
		if _, err := o.DomainSet(context.Background(), admproto.DomainSet{Name: bad}); err == nil {
			t.Fatalf("%q accepted", bad)
		}
	}
}

func TestDomainSetRollback(t *testing.T) {
	o, fn := nodeSetup(t)
	n := 0
	fn.active = func() string {
		n++
		if n == 1 {
			return "failed"
		}
		return "active"
	}
	if _, err := o.DomainSet(context.Background(), admproto.DomainSet{Name: "new.example.net"}); err == nil || !strings.Contains(err.Error(), "old name is back") {
		t.Fatalf("want rollback, got %v", err)
	}
	nc, _ := os.ReadFile(filepath.Join(o.Etc, "node.conf"))
	site, _ := os.ReadFile(o.NginxSite)
	env, _ := os.ReadFile(o.PanelEnv)
	if string(nc) != testNodeConf || string(site) != testSite || !strings.Contains(string(env), "PANEL_HOSTS=old.example.net") {
		t.Fatal("files not rolled back")
	}
	o2, _ := nodeSetup(t)
	o2.NginxTest = func() error { return os.ErrInvalid }
	if _, err := o2.DomainSet(context.Background(), admproto.DomainSet{Name: "new.example.net"}); err == nil {
		t.Fatal("nginx -t failure ignored")
	}
	if site, _ := os.ReadFile(o2.NginxSite); string(site) != testSite {
		t.Fatal("site not rolled back after nginx -t")
	}
}

func selfSigned(t *testing.T, name string, until time.Time) (string, string) {
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(1), Subject: pkix.Name{CommonName: name}, DNSNames: []string{name},
		NotBefore: time.Now().Add(-time.Hour), NotAfter: until}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &k.PublicKey, k)
	if err != nil {
		t.Fatal(err)
	}
	kd, _ := x509.MarshalECPrivateKey(k)
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kd}))
}

func TestCertInstall(t *testing.T) {
	o, fn := nodeSetup(t)
	oc, ok := selfSigned(t, "old.example.net", time.Now().Add(24*time.Hour))
	os.WriteFile(filepath.Join(o.Etc, "tls", "fullchain.pem"), []byte(oc), 0o640)
	os.WriteFile(filepath.Join(o.Etc, "tls", "privkey.pem"), []byte(ok), 0o640)
	c, k := selfSigned(t, "new.example.net", time.Now().Add(90*24*time.Hour))
	if _, err := o.CertInstall(context.Background(), admproto.CertInstall{Fullchain: c, Key: k}); err != nil {
		t.Fatal(err)
	}
	got, _ := os.ReadFile(filepath.Join(o.Etc, "tls", "fullchain.pem"))
	prev, _ := os.ReadFile(filepath.Join(o.Etc, "tls", "fullchain.pem.prev"))
	if string(got) != c || string(prev) != oc || !contains(fn.calls, "reload nginx") {
		t.Fatal("cert not installed or no .prev")
	}
	_, k2 := selfSigned(t, "x.example.net", time.Now().Add(time.Hour))
	if _, err := o.CertInstall(context.Background(), admproto.CertInstall{Fullchain: c, Key: k2}); err == nil {
		t.Fatal("mismatched key accepted")
	}
	ec, ek := selfSigned(t, "x.example.net", time.Now().Add(-time.Minute))
	if _, err := o.CertInstall(context.Background(), admproto.CertInstall{Fullchain: ec, Key: ek}); err == nil {
		t.Fatal("expired cert accepted")
	}
	n := 0
	fn.active = func() string {
		n++
		if n == 1 {
			return "failed"
		}
		return "active"
	}
	c3, k3 := selfSigned(t, "z.example.net", time.Now().Add(time.Hour))
	if _, err := o.CertInstall(context.Background(), admproto.CertInstall{Fullchain: c3, Key: k3}); err == nil {
		t.Fatal("failed node accepted")
	}
	if got, _ := os.ReadFile(filepath.Join(o.Etc, "tls", "fullchain.pem")); string(got) != c {
		t.Fatal("cert not rolled back")
	}
}

func TestSetLine(t *testing.T) {
	if got := setLine("a 1\nadvertise x\nb 2\n", "advertise", "advertise y"); got != "a 1\nadvertise y\nb 2\n" {
		t.Fatalf("%q", got)
	}
	if got := setLine("a 1", "advertise", "advertise y"); got != "a 1\nadvertise y\n" {
		t.Fatalf("%q", got)
	}
	if got := setLine("X=1\nPANEL_HOSTS=a\n", "PANEL_HOSTS=", "PANEL_HOSTS=b"); got != "X=1\nPANEL_HOSTS=b\n" {
		t.Fatalf("%q", got)
	}
	if got := setLine("advertise_x 1\n", "advertise", "advertise y"); got != "advertise_x 1\nadvertise y\n" {
		t.Fatalf("prefix matched: %q", got)
	}
}

func TestDomainSetSeparatePanel(t *testing.T) {
	o, _ := nodeSetup(t)
	in, err := o.DomainSet(context.Background(), admproto.DomainSet{Name: "api.example.net", Panel: "us.example.net"})
	if err != nil {
		t.Fatal(err)
	}
	if in.Host != "api.example.net" || in.Panel != "us.example.net" {
		t.Fatalf("%+v", in)
	}
	site, _ := os.ReadFile(o.NginxSite)
	st := string(site)
	for _, want := range []string{
		"server_name api.example.net us.example.net;",                                      // :80 for both, Let's Encrypt
		"server_name us.example.net;",                                                      // the panel only on its own name
		"listen 127.0.0.1:8443 ssl http2 proxy_protocol default_server;\n\tserver_name _;", // probes of the node
		"root /var/www/html;",
	} {
		if !strings.Contains(st, want) {
			t.Fatalf("site lacks %q:\n%s", want, st)
		}
	}
	// Until the panel has its own certificate it uses the node's.
	if strings.Contains(st, "panel-tls") {
		t.Fatal("panel cert used before it exists")
	}
	c, k := selfSigned(t, "us.example.net", time.Now().Add(90*24*time.Hour))
	if _, err := o.CertInstall(context.Background(), admproto.CertInstall{Fullchain: c, Key: k, Target: "panel"}); err != nil {
		t.Fatal(err)
	}
	site, _ = os.ReadFile(o.NginxSite)
	pi := strings.Index(string(site), "server_name us.example.net;")
	di := strings.Index(string(site), "server_name _;")
	if !strings.Contains(string(site)[pi:di], filepath.Join(o.PanelTLS, "fullchain.pem")) || strings.Contains(string(site)[di:], "panel-tls") {
		t.Fatalf("panel cert must serve only the panel:\n%s", site)
	}
	if st, _ := os.Stat(filepath.Join(o.PanelTLS, "privkey.pem")); st.Mode().Perm() != 0o600 {
		t.Fatalf("panel key mode %v", st.Mode())
	}
	if _, err := o.CertInstall(context.Background(), admproto.CertInstall{Fullchain: c, Key: k, Target: "both"}); err == nil {
		t.Fatal("unknown target accepted")
	}
	// Back to one name: one panel server, default, on the node cert.
	if _, err := o.DomainSet(context.Background(), admproto.DomainSet{Name: "api.example.net"}); err != nil {
		t.Fatal(err)
	}
	site, _ = os.ReadFile(o.NginxSite)
	if strings.Contains(string(site), "server_name _;") || !strings.Contains(string(site), "proxy_protocol default_server;\n\tserver_name api.example.net;") {
		t.Fatalf("single name site:\n%s", site)
	}
}

func TestRenderSiteSingle(t *testing.T) {
	s := RenderSite(Site{Node: "vpn.example.net", Listen: "127.0.0.1:8091", NodeCert: "/c", NodeKey: "/k", PanelCert: "/c", PanelKey: "/k"})
	if strings.Count(s, "server {") != 2 || !strings.Contains(s, "server_name vpn.example.net;") || !strings.Contains(s, "proxy_pass http://127.0.0.1:8091;") {
		t.Fatal(s)
	}
}

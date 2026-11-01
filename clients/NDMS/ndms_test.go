package ndms

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/a3app/loft/internal/config"
)

// TestUpLinesOrder locks the ndmc order: host pins before "up", no default
// route, a quoted description, down before "no ip route", and "no interface"
// immediately before the final save.
func TestUpLinesOrder(t *testing.T) {
	lines, err := UpLines(Up{
		Index: 0, Desc: "Work", IP: "10.7.0.2", MTU: 1280,
		ISP: "GigabitEthernet0/0", Hosts: []string{"1.2.3.4"},
	})
	if err != nil {
		t.Fatal(err)
	}
	joined := strings.Join(lines, "\n")
	if strings.Contains(strings.ToLower(joined), "default") {
		t.Fatalf("default route in %q", joined)
	}
	pin := strings.Index(joined, "ip route 1.2.3.4/32 GigabitEthernet0/0")
	up := strings.Index(joined, "interface OpkgTun0 up")
	if pin < 0 || up < 0 || pin > up {
		t.Fatalf("pin before up:\n%s", joined)
	}
	if !strings.Contains(joined, `description "Work"`) {
		t.Fatalf("quote: %s", joined)
	}
	if _, err := UpLines(Up{Index: 0, Desc: `bad"name`, IP: "10.7.0.2"}); err == nil {
		t.Fatal("quote accepted")
	}
	down, err := DownLines(Up{Index: 0, ISP: "GigabitEthernet0/0", Hosts: []string{"1.2.3.4"}})
	if err != nil {
		t.Fatal(err)
	}
	if down[0] != "interface OpkgTun0 down" || !strings.HasPrefix(down[1], "no ip route ") {
		t.Fatalf("down %v", down)
	}
	del, err := DeleteLines(Up{Index: 0, ISP: "GigabitEthernet0/0", Hosts: []string{"1.2.3.4"}})
	if err != nil {
		t.Fatal(err)
	}
	if del[len(del)-2] != "no interface OpkgTun0" || del[len(del)-1] != "system configuration save" {
		t.Fatalf("delete %v", del)
	}
}

// TestApplySkipsPin checks that a failed "ip route" does not stop "interface up",
// and that a failed "up" does stop the script.
func TestApplySkipsPin(t *testing.T) {
	r := &script{fail: "ip route "}
	lines, err := UpLines(Up{Index: 1, Desc: "A", IP: "10.1.0.2", ISP: "ISP", Hosts: []string{"9.9.9.9"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := Apply(context.Background(), r, lines); err != nil {
		t.Fatal(err)
	}
	if !containsLine(r.lines, "interface OpkgTun1 up") {
		t.Fatalf("up skipped: %v", r.lines)
	}
	r2 := &script{fail: "interface OpkgTun1 up"}
	if err := Apply(context.Background(), r2, lines); err == nil {
		t.Fatal("up failure ignored")
	}
}

// script is a Runner that records lines and fails those with a given prefix.
type script struct {
	lines []string
	fail  string
}

// Run appends line and returns an error when it has the fail prefix.
func (s *script) Run(_ context.Context, line string) error {
	s.lines = append(s.lines, line)
	if s.fail != "" && strings.HasPrefix(line, s.fail) {
		return os.ErrInvalid
	}
	return nil
}

// containsLine reports an exact line match.
func containsLine(lines []string, want string) bool {
	for _, l := range lines {
		if l == want {
			return true
		}
	}
	return false
}

// TestTooOld refuses a parsed major below 5 and accepts 5.x and unparsed text.
func TestTooOld(t *testing.T) {
	if !TooOld("NDMS release: 4.3") || !TooOld("version: 3") {
		t.Fatal("old firmware accepted")
	}
	if TooOld("release: 5.1") || TooOld("KeeneticOS") || TooOld("") {
		t.Fatal("unparsed or 5.x refused")
	}
}

// TestStoreRoundTrip checks kind rejection, the next free OpkgTun, secret
// retention on an empty update, auth defaulting, and a missing enabled
// flag staying false.
func TestStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "client.yaml")
	if err := os.WriteFile(path, []byte("listen:\n  http: \":8090\"\nconnections: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	st := OpenStore(path)
	if _, err := st.Put(Conn{Name: "bad", Kind: "sstp"}, true); err == nil {
		t.Fatal("sstp kind")
	}
	a, err := st.Put(Conn{Name: "Work", Kind: "loft", Enabled: true, Opkg: -1, Token: "lt_secret"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if a.Opkg != 0 || a.ID == "" {
		t.Fatalf("%+v", a)
	}
	b, err := st.Put(Conn{Name: "NL", Kind: "vless", Enabled: true, Opkg: -1}, true)
	if err != nil {
		t.Fatal(err)
	}
	if b.Opkg != 1 {
		t.Fatalf("opkg %d", b.Opkg)
	}
	kept, err := st.Put(Conn{ID: a.ID, Name: "Work", Kind: "loft", Enabled: true, Opkg: a.Opkg}, true)
	if err != nil {
		t.Fatal(err)
	}
	if kept.Token != "lt_secret" {
		t.Fatalf("secret dropped: %+v", kept)
	}
	f, err := st.Load()
	if err != nil || len(f.Connections) != 2 || !f.Keenetic.CheckUpdates && f.Keenetic.Auth == "" {
		t.Fatal(err, f.Keenetic)
	}
	if f.Keenetic.Auth != "http://127.0.0.1/auth" {
		t.Fatal(f.Keenetic.Auth)
	}
	raw := "connections:\n  - id: ab\n    name: Off\n    kind: loft\n    opkg: 3\n"
	if err := os.WriteFile(path, []byte(raw), 0o600); err != nil {
		t.Fatal(err)
	}
	f, err = st.Load()
	if err != nil || len(f.Connections) != 1 || f.Connections[0].Enabled {
		t.Fatalf("missing enabled must be false: %+v %v", f.Connections, err)
	}
}

// TestAuth accepts an admin role and an empty body, rejects guest and a
// user named guest, forwards the ndw4 token, and refuses a non-loopback URL.
func TestAuth(t *testing.T) {
	var got string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Get("x-ndw4-authenticate")
		switch r.URL.Path {
		case "/admin":
			w.Write([]byte(`{"role":"admin","user":"bob"}`))
		case "/guest":
			w.Write([]byte(`{"role":"guest"}`))
		case "/named":
			w.Write([]byte(`{"user":"guest"}`))
		case "/empty":
			w.Write(nil)
		default:
			w.WriteHeader(http.StatusUnauthorized)
		}
	}))
	defer srv.Close()
	ok, err := Check(context.Background(), srv.URL+"/admin", "session=1", "tok")
	if err != nil || !ok || got != "tok" {
		t.Fatalf("admin ok=%v err=%v ndw4=%q", ok, err, got)
	}
	ok, err = Check(context.Background(), srv.URL+"/guest", "session=1", "")
	if err != nil || ok {
		t.Fatalf("guest ok=%v err=%v", ok, err)
	}
	ok, err = Check(context.Background(), srv.URL+"/named", "", "")
	if err != nil || ok {
		t.Fatalf("user guest ok=%v", ok)
	}
	ok, err = Check(context.Background(), srv.URL+"/empty", "", "")
	if err != nil || !ok {
		t.Fatalf("empty body ok=%v err=%v", ok, err)
	}
	ok, err = Check(context.Background(), srv.URL+"/no", "", "")
	if err != nil || ok {
		t.Fatalf("401 ok=%v err=%v", ok, err)
	}
	if err := Loopback("http://example.com/auth"); err == nil {
		t.Fatal("remote auth accepted")
	}
	if err := Loopback("http://127.0.0.1/auth"); err != nil {
		t.Fatal(err)
	}
}

// TestISP reads the default-route interface from RCI JSON and from ndmc text.
func TestISP(t *testing.T) {
	body := []byte(`{"show":{"ip":{"route":[{"destination":"0.0.0.0/0","interface":"GigabitEthernet0/0"}]}}}`)
	s, ok := ParseRoutes(body)
	if !ok || s != "GigabitEthernet0/0" {
		t.Fatalf("%q %v", s, ok)
	}
	s, ok = ParseRouteText("S>* 0.0.0.0/0 [0/0] via 1.2.3.4 GigabitEthernet0/0\n")
	if !ok || s != "GigabitEthernet0/0" {
		t.Fatalf("text %q %v", s, ok)
	}
}

// TestUpdatePick selects the versioned ipk that has a sha256 sidecar,
// checks a "hex  name" digest line, and compares dotted versions.
func TestUpdatePick(t *testing.T) {
	assets := []ghAsset{
		{Name: "loft-ndms-1.0.32-aarch64.ipk", URL: "http://example/b"},
		{Name: "loft-ndms-1.0.32-aarch64.ipk.sha256", URL: "http://example/c"},
		{Name: "loft-ndms-armv7.ipk", URL: "http://example/a"},
	}
	ipk, sum, err := PickAssets(assets, "aarch64")
	if err != nil {
		t.Fatal(err)
	}
	if ipk.Name != "loft-ndms-1.0.32-aarch64.ipk" || sum.Name != ipk.Name+".sha256" {
		t.Fatalf("%s %s", ipk.Name, sum.Name)
	}
	body := []byte("pkg")
	sum256 := sha256.Sum256(body)
	line := hex.EncodeToString(sum256[:]) + "  loft.ipk\n"
	if err := VerifySHA256(body, line); err != nil {
		t.Fatal(err)
	}
	if err := VerifySHA256(body, "00"); err == nil {
		t.Fatal("bad sum accepted")
	}
	if !Newer("1.0.33", "1.0.32") || Newer("1.0.32", "1.0.32") || Newer("dev", "1.0.32") {
		t.Fatal("Newer")
	}
	if !Newer("1.0.32", "") || !Newer("1.0.32", "dev") {
		t.Fatal("empty current")
	}
}

// TestListenAddrs binds loopback plus private LAN for an empty host,
// adds loopback beside a specific host, and does not duplicate loopback.
func TestListenAddrs(t *testing.T) {
	got := ListenAddrs("", []string{"192.168.1.1"})
	if strings.Join(got, ",") != "127.0.0.1:8090,192.168.1.1:8090" {
		t.Fatal(got)
	}
	got = ListenAddrs("10.0.0.1:8090", nil)
	if strings.Join(got, ",") != "10.0.0.1:8090,127.0.0.1:8090" {
		t.Fatal(got)
	}
	got = ListenAddrs("127.0.0.1:8090", []string{"192.168.1.1"})
	if strings.Join(got, ",") != "127.0.0.1:8090" {
		t.Fatal(got)
	}
}

// TestImportAndRuntime covers a loft:// link, an SSTP skip, manual front
// order (get before ws, wss for ws), VLESS vision flow, and distinct
// 198.18.<index>.1 addresses for two proxy tuns.
func TestImportAndRuntime(t *testing.T) {
	res, err := ImportText("loft://lt_abc@edge.example.com:443/static/js?type=get&cdn=cloudflare&fp=chrome#Work")
	if err != nil || len(res.Added) != 1 {
		t.Fatal(err, res)
	}
	c := res.Added[0]
	c.Opkg = 0
	rt, err := c.Runtime()
	if err != nil {
		t.Fatal(err)
	}
	if rt.Kind != "loft" || rt.Loft == nil || rt.Tun != "opkgtun0" || rt.NDM != "OpkgTun0" {
		t.Fatalf("%+v", rt)
	}
	if rt.Loft.Listen.KillSwitch || rt.Loft.ListsURL != "" || rt.Loft.P2P == nil || *rt.Loft.P2P {
		t.Fatalf("loft prepared %+v", rt.Loft.Listen)
	}
	if _, err := ImportText("sstp://alice:secret@vpn.example.com:8443#Office"); err == nil || !strings.Contains(err.Error(), "SSTP") {
		t.Fatal(err)
	}
	manual := Conn{
		Name: "Home", Kind: "loft", Mode: "manual", Opkg: 2, Enabled: true,
		Token: "lt_abc", Host: "edge.example.com", Port: 443, Fronts: []string{"ws", "get"},
		CC: "bbr",
	}
	rt, err = manual.Runtime()
	if err != nil {
		t.Fatal(err)
	}
	if len(rt.Loft.Outbounds) != 2 || rt.Loft.Outbounds[0].Type != "get" || rt.Loft.Outbounds[1].Type != "ws" {
		t.Fatalf("front order %+v", rt.Loft.Outbounds)
	}
	if rt.Loft.Outbounds[1].URL != "wss://edge.example.com" {
		t.Fatal(rt.Loft.Outbounds[1].URL)
	}
	vx := Conn{
		Name: "NL", Kind: "vless", Mode: "manual", Opkg: 1,
		UUID: "550e8400-e29b-41d4-a716-446655440000", Host: "de1.example.com", Port: 443,
		Security: "tls", Network: "tcp", SNI: "de1.example.com", Vision: true,
	}
	rt, err = vx.Runtime()
	if err != nil {
		t.Fatal(err)
	}
	if rt.IP != "198.18.1.1" || !strings.Contains(rt.Proxy, "flow=xtls-rprx-vision") {
		t.Fatalf("%s %s", rt.IP, rt.Proxy)
	}
	tr := Conn{
		Name: "T", Kind: "trojan", Mode: "manual", Opkg: 4,
		Password: "secret", Host: "de1.example.com", Port: 443, Security: "tls", Network: "tcp",
	}
	rt, err = tr.Runtime()
	if err != nil {
		t.Fatal(err)
	}
	if rt.IP != "198.18.4.1" || !strings.HasPrefix(rt.Proxy, "trojan://") {
		t.Fatalf("%s %s", rt.IP, rt.Proxy)
	}
	_ = config.DefaultGETPath
}

// TestPanel checks the embedded page, the login redirect, the X-Loft
// requirement, and an import that shows up as OpkgTun0. Sup.Exe is
// /usr/bin/true so a reconcile cannot re-exec the test binary.
func TestPanel(t *testing.T) {
	b, err := page.ReadFile("web/index.html")
	if err != nil || !strings.Contains(string(b), "OpkgTun") {
		t.Fatal(err)
	}
	dir := t.TempDir()
	path := filepath.Join(dir, "client.yaml")
	if err := os.WriteFile(path, []byte("connections: []\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	st := OpenStore(path)
	p := &Panel{Store: st, Sup: &Sup{Store: st, Ring: NewRing(8), State: dir, Exe: "/usr/bin/true"}, Allow: func(r *http.Request) (bool, string, error) {
		if r.Header.Get("Cookie") == "" && r.URL.Query().Get("ok") == "" {
			return false, "http://192.168.1.1/?return=http%3A%2F%2F192.168.1.1%3A8090%2F", nil
		}
		return true, "", nil
	}}
	h := p.Handler()
	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.Host = "192.168.1.1:8090"
	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusFound || !strings.Contains(rec.Header().Get("Location"), "return=") {
		t.Fatalf("redirect %d %s", rec.Code, rec.Header().Get("Location"))
	}
	req = httptest.NewRequest(http.MethodPost, "/api/conn/toggle", strings.NewReader(`{"id":"x","enabled":true}`))
	req.Host = "192.168.1.1:8090"
	req.Header.Set("Cookie", "a=b")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != http.StatusForbidden {
		t.Fatalf("csrf %d", rec.Code)
	}
	req = httptest.NewRequest(http.MethodPost, "/api/import", strings.NewReader(`{"text":"loft://lt_abc@edge.example.com:443/static/js?type=get&fp=chrome#Work"}`))
	req.Host = "192.168.1.1:8090"
	req.Header.Set("Cookie", "a=b")
	req.Header.Set("X-Loft", "1")
	req.Header.Set("Content-Type", "application/json")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "Work") {
		t.Fatalf("import %d %s", rec.Code, rec.Body.String())
	}
	req = httptest.NewRequest(http.MethodGet, "/api/status", nil)
	req.Header.Set("Cookie", "a=b")
	rec = httptest.NewRecorder()
	h.ServeHTTP(rec, req)
	if rec.Code != 200 || !strings.Contains(rec.Body.String(), "OpkgTun0") {
		t.Fatalf("status %d %s", rec.Code, rec.Body.String())
	}
}

// TestIPK writes a package whose binary is an odd number of bytes, so the
// ar pad newline is outside the member size, and reads the three member names.
func TestIPK(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "t.ipk")
	err := WriteIPK(path, IPKMeta{
		Version: "1.0.32", Arch: "aarch64-3.10",
		Bin: []byte("abc"), Init: []byte("#!/bin/sh\n"), Sample: []byte("connections: []\n"),
		Prerm: []byte("#!/bin/sh\n"), Postinst: []byte("#!/bin/sh\n"),
	})
	if err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(string(b), "!<arch>\n") {
		t.Fatal("magic")
	}
	names, err := ReadARNames(b)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(names, ",") != "debian-binary,control.tar.gz,data.tar.gz" {
		t.Fatal(names)
	}
}

// TestIPKConffiles checks that the package marks client.yaml as a conffile
// (so opkg preserves operator edits on upgrade) and actually ships that
// file, seeded from the sample.
func TestIPKConffiles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "t.ipk")
	sample := []byte("connections: []\n")
	if err := WriteIPK(path, IPKMeta{
		Version: "1.0.35", Arch: "aarch64-3.10",
		Bin: []byte("abc"), Init: []byte("#!/bin/sh\n"), Sample: sample,
		Prerm: []byte("#!/bin/sh\n"), Postinst: []byte("#!/bin/sh\n"),
	}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	// control.tar.gz lists /opt/etc/loft/client.yaml in conffiles.
	ctrl := tarMembers(t, arMember(t, b, "control.tar.gz"))
	if got := strings.TrimSpace(string(ctrl["conffiles"])); got != "/opt/etc/loft/client.yaml" {
		t.Fatalf("conffiles = %q", got)
	}
	// data.tar.gz ships the tracked config, with the sample's content.
	data := tarMembers(t, arMember(t, b, "data.tar.gz"))
	if _, ok := data["opt/etc/loft/client.yaml"]; !ok {
		t.Fatal("client.yaml not shipped")
	}
	if !bytes.Equal(data["opt/etc/loft/client.yaml"], sample) {
		t.Fatal("client.yaml not seeded from sample")
	}
}

// arMember returns one System V ar member's body by name.
func arMember(t *testing.T, ar []byte, want string) []byte {
	t.Helper()
	rest := ar[len("!<arch>\n"):]
	for len(rest) >= 60 {
		hdr := rest[:60]
		rest = rest[60:]
		name := strings.TrimRight(strings.TrimSpace(string(hdr[0:16])), "/")
		var size int
		_, _ = fmtSscan(string(hdr[48:58]), &size)
		body := rest[:size]
		rest = rest[size:]
		if size%2 == 1 && len(rest) > 0 && rest[0] == '\n' {
			rest = rest[1:]
		}
		if name == want {
			return body
		}
	}
	t.Fatalf("ar member %s not found", want)
	return nil
}

// tarMembers gunzips a .tar.gz and returns every regular file's body by name.
func tarMembers(t *testing.T, gzBody []byte) map[string][]byte {
	t.Helper()
	gz, err := gzip.NewReader(bytes.NewReader(gzBody))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(gz)
	out := map[string][]byte{}
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			t.Fatal(err)
		}
		if h.Typeflag == tar.TypeReg {
			b, _ := io.ReadAll(tr)
			out[h.Name] = b
		}
	}
	return out
}

// fmtSscan is fmt.Sscanf for one decimal, split out so the test needs no
// extra import at the call site.
func fmtSscan(s string, n *int) (int, error) { return fmt.Sscanf(s, "%d", n) }

// TestRedact hides a member key, a UUID, a trojan userinfo secret, and user:password.
func TestRedact(t *testing.T) {
	s := Redact("loft://lt_abcdefgh@edge.example.com 550e8400-e29b-41d4-a716-446655440000")
	if strings.Contains(s, "lt_abcdefgh") || strings.Contains(s, "550e8400") {
		t.Fatal(s)
	}
	s = Redact("trojan://s3cret@edge.example.com sstp://u:p@h")
	if strings.Contains(s, "s3cret") || strings.Contains(s, "u:p") {
		t.Fatal(s)
	}
}

// TestSnapPath rejects a path that leaves the state directory and round-trips a snap.
func TestSnapPath(t *testing.T) {
	dir := t.TempDir()
	if snapPath(dir, "../etc/passwd") != "" || snapPath(dir, "ab/cd") != "" {
		t.Fatal("traversal")
	}
	p := snapPath(dir, "ab")
	if p == "" || filepath.Dir(p) != dir {
		t.Fatal(p)
	}
	if err := writeSnap("", Snap{State: "error"}); err == nil {
		t.Fatal("empty path")
	}
	if err := writeSnap(p, Snap{State: "up"}); err != nil {
		t.Fatal(err)
	}
	if readSnap(p).State != "up" || readSnap("").State != "" {
		t.Fatal(readSnap(p))
	}
}

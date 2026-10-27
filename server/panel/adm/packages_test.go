package adm

import (
	"archive/tar"
	"archive/zip"
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
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/a3app/loft/server/panel/admproto"
	"github.com/a3app/loft/server/panel/catalog"
)

// fakeHost plays dpkg, apt and systemd for package jobs.
type fakeHost struct {
	installed map[string]string // apt name → version
	cand      map[string]string
	active    map[string]string // unit → state
	jobUnit   bool
	ran       []string
	failApt   string // fail apt-get with this verb
}

func (h *fakeHost) exec(_ context.Context, name string, args ...string) (string, error) {
	switch name {
	case "dpkg-query":
		if strings.Contains(args[1], "Depends") { // dependency walk
			return map[string]string{"dovecot-imapd": "dovecot-core (= 1:2.3), libc6 (>= 2.38)", "dovecot-core": "libc6, openssl | libssl3"}[args[2]], nil
		}
		var b strings.Builder
		for _, n := range args[2:] {
			if v, ok := h.installed[n]; ok {
				fmt.Fprintf(&b, "%s\tii \t%s\t2048\n", n, v)
			}
		}
		return b.String(), nil
	case "apt-cache":
		var b strings.Builder
		for _, n := range args[1:] {
			fmt.Fprintf(&b, "%s:\n  Installed: %s\n  Candidate: %s\n", n, h.installed[n], h.cand[n])
		}
		return b.String(), nil
	case "apt-get": // -s autoremove
		return "NOTE: simulation\nRemv dovecot-core [1:2.3]\nRemv zerofree [1.1]\n", nil
	case "dpkg": // --compare-versions a gt b
		if args[1] > args[3] {
			return "", nil
		}
		return "", fmt.Errorf("exit 1")
	}
	return "", nil
}

func (h *fakeHost) systemctl(_ context.Context, args ...string) (string, error) {
	switch args[0] {
	case "is-active":
		if args[1] == JobUnit+".service" {
			if h.jobUnit {
				return "active", nil
			}
			return "inactive", nil
		}
		return firstNonEmpty(h.active[args[1]], "inactive"), nil
	case "show":
		if args[1] == "--property=Id,ActiveState,UnitFileState" {
			var b []string
			for _, u := range args[2:] {
				st := firstNonEmpty(h.active[u], "inactive")
				file := "enabled"
				if st == "inactive" {
					file = ""
				}
				b = append(b, fmt.Sprintf("Id=%s\nActiveState=%s\nUnitFileState=%s", u, st, file))
			}
			return strings.Join(b, "\n\n"), nil
		}
		return "ActiveState=active\nActiveEnterTimestamp=Tue 2026-10-20 11:28:00 UTC", nil
	}
	return "", nil
}

func (h *fakeHost) stream(_ context.Context, w io.Writer, name string, args ...string) error {
	line := name + " " + strings.Join(args, " ")
	h.ran = append(h.ran, line)
	fmt.Fprintln(w, "ok")
	if name == "apt-get" {
		verb := ""
		for _, a := range args {
			if !strings.HasPrefix(a, "-") && !strings.Contains(a, "::") && !strings.Contains(a, "=") {
				verb = a
				break
			}
		}
		if verb == h.failApt {
			return fmt.Errorf("exit status 100")
		}
		pk := args[len(args)-1]
		switch verb {
		case "install":
			for _, a := range args {
				if !strings.HasPrefix(a, "-") && a != "install" && !strings.Contains(a, "=") {
					h.installed[a] = "1.0-1"
				}
			}
		case "remove", "purge":
			delete(h.installed, pk)
		}
	}
	if name == "systemctl" && args[0] == "restart" {
		h.active[args[1]] = "active"
	}
	return nil
}

func pkgOps(t *testing.T) (*Ops, *fakeHost) {
	t.Helper()
	root := t.TempDir()
	os.MkdirAll(filepath.Join(root, "etc"), 0o755)
	os.WriteFile(filepath.Join(root, "etc/os-release"), []byte("ID=ubuntu\nVERSION_CODENAME=noble\n"), 0o644)
	h := &fakeHost{installed: map[string]string{"nginx": "1.24.0-2ubuntu7"}, cand: map[string]string{}, active: map[string]string{"nginx.service": "active", "loft-node.service": "active"}}
	o := &Ops{Dir: t.TempDir(), Unit: "loft-node.service", UID: -1, GID: -1, Settle: time.Millisecond,
		State: t.TempDir(), Root: root, PanelEtc: filepath.Join(root, "etc/loft-panel"), Exe: "/usr/local/bin/loft-panel-adm", NodeBin: "/usr/local/bin/loft-node",
		Systemctl: h.systemctl, Exec: h.exec, Stream: h.stream}
	o.SystemdRun = func(context.Context, ...string) error { h.jobUnit = true; return nil }
	return o, h
}

func TestPackagesState(t *testing.T) {
	o, h := pkgOps(t)
	h.installed["wireguard-tools"] = "1.0.20210914-1ubuntu4"
	h.installed["fail2ban"] = "1.0.2-3"
	h.cand["fail2ban"] = "1.0.2-4"
	h.active["fail2ban.service"] = "active"
	l, err := o.Packages(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]admproto.Package{}
	for _, p := range l {
		by[p.Key] = p
	}
	if p := by["wireguard"]; !p.Installed || p.Version != "1.0.20210914-1ubuntu4" || p.Service || p.DiskMB != 2 {
		t.Fatalf("wireguard %+v", p)
	}
	if p := by["fail2ban"]; !p.Installed || p.Candidate != "1.0.2-4" || p.Active != "active" || !p.Enabled {
		t.Fatalf("fail2ban %+v", p)
	}
	if p := by["zerotier"]; p.Installed || p.DiskMB != 9 || p.Ports != "9993 udp" {
		t.Fatalf("zerotier %+v", p)
	}
	if p := by["nginx"]; !p.Base || !p.Installed || p.Active != "active" {
		t.Fatalf("nginx %+v", p)
	}
}

func TestPackagesJob(t *testing.T) {
	o, h := pkgOps(t)
	ctx := context.Background()
	j, err := o.StartPackages(ctx, "admin", catalog.Plan{Install: []string{"fail2ban", "zerotier"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := o.StartPackages(ctx, "admin", catalog.Plan{Install: []string{"openvpn"}}); err == nil {
		t.Fatal("second job started while the first runs")
	}
	for _, p := range must(o.Packages(ctx)) {
		if p.Key == "fail2ban" && p.Busy != "install" {
			t.Fatalf("busy not shown: %+v", p)
		}
	}
	if err := o.RunJob(ctx); err != nil {
		t.Fatal(err)
	}
	h.jobUnit = false
	got, err := o.JobGet(ctx, true)
	if err != nil || got.ID != j.ID || got.State != "done" || !strings.Contains(got.Log, "✓ done") {
		t.Fatalf("job %+v %v", got, err)
	}
	if h.installed["zerotier-one"] == "" || h.installed["fail2ban"] == "" {
		t.Fatalf("not installed: %v", h.installed)
	}
	list, _ := os.ReadFile(filepath.Join(o.Root, "etc/apt/sources.list.d/loft-zerotier.list"))
	if !strings.Contains(string(list), "deb [signed-by=/etc/apt/keyrings/loft-zerotier.asc] https://download.zerotier.com/debian/noble noble main") {
		t.Fatalf("list: %s", list)
	}
	if key, _ := os.ReadFile(filepath.Join(o.Root, "etc/apt/keyrings/loft-zerotier.asc")); !bytes.Contains(key, []byte("PGP PUBLIC KEY")) {
		t.Fatal("no key")
	}
	if b, _ := os.ReadFile(filepath.Join(o.Root, "etc/fail2ban/jail.d/loft-panel.conf")); !bytes.Contains(b, []byte("backend = systemd")) {
		t.Fatal("no jail")
	}
	all := strings.Join(h.ran, "\n")
	for _, want := range []string{"apt-get -y -q -o DPkg::Lock::Timeout=300", " update", "install --no-install-recommends fail2ban python3-systemd", "apt-mark auto python3-systemd", "systemctl restart fail2ban.service"} {
		if !strings.Contains(all, want) {
			t.Fatalf("missing %q in\n%s", want, all)
		}
	}
	if strings.Index(all, " update") > strings.Index(all, "install --no-install-recommends") {
		t.Fatal("install before apt update")
	}

	// Remove with wipe: the repo, the jail and the data go.
	os.MkdirAll(filepath.Join(o.Root, "var/lib/zerotier-one"), 0o700)
	if _, err := o.StartPackages(ctx, "admin", catalog.Plan{Remove: []string{"zerotier", "fail2ban"}, Purge: true}); err != nil {
		t.Fatal(err)
	}
	o.RunJob(ctx)
	h.jobUnit = false
	if got, _ := o.JobGet(ctx, false); got.State != "done" {
		t.Fatalf("remove: %+v", got)
	}
	for _, p := range []string{"etc/apt/sources.list.d/loft-zerotier.list", "etc/apt/keyrings/loft-zerotier.asc", "etc/fail2ban/jail.d/loft-panel.conf", "var/lib/zerotier-one"} {
		if _, err := os.Stat(filepath.Join(o.Root, p)); err == nil {
			t.Fatalf("%s left", p)
		}
	}
	if !strings.Contains(strings.Join(h.ran, "\n"), "purge zerotier-one") {
		t.Fatal("not purged")
	}
}

func TestPackagesJobFails(t *testing.T) {
	o, h := pkgOps(t)
	ctx := context.Background()
	h.failApt = "install"
	o.StartPackages(ctx, "admin", catalog.Plan{Install: []string{"openvpn"}})
	o.RunJob(ctx)
	h.jobUnit = false
	j, _ := o.JobGet(ctx, true)
	if j.State != "error" || !strings.Contains(j.Error, "install openvpn: apt-get install failed") || !strings.Contains(j.Log, "✗") {
		t.Fatalf("%+v", j)
	}
	// A unit that does not come up is an error with its journal in the log.
	h.failApt = ""
	h.active["fail2ban.service"] = "failed"
	o.Stream = func(ctx context.Context, w io.Writer, name string, args ...string) error {
		if name == "systemctl" && args[0] == "restart" {
			return nil // stays failed
		}
		return h.stream(ctx, w, name, args...)
	}
	o.StartPackages(ctx, "admin", catalog.Plan{Install: []string{"fail2ban"}})
	o.RunJob(ctx)
	h.jobUnit = false
	if j, _ := o.JobGet(ctx, true); j.State != "error" || !strings.Contains(j.Error, "did not start") || !strings.Contains(j.Log, "journalctl -u fail2ban.service") {
		t.Fatalf("%+v", j)
	}
}

func TestJobInterrupted(t *testing.T) {
	o, h := pkgOps(t)
	ctx := context.Background()
	o.StartPackages(ctx, "admin", catalog.Plan{Install: []string{"openvpn"}})
	h.jobUnit = false // the runner died without writing its end
	j, _ := o.JobGet(ctx, false)
	if j.State != "interrupted" {
		t.Fatalf("%+v", j)
	}
	if _, err := o.StartPackages(ctx, "admin", catalog.Plan{Install: []string{"openvpn"}}); err != nil {
		t.Fatal(err)
	}
}

func TestPackageUnit(t *testing.T) {
	o, h := pkgOps(t)
	ctx := context.Background()
	if _, err := o.PackageUnit(ctx, admproto.PackageUnit{Key: "nginx", On: false}); err == nil {
		t.Fatal("base service switched off")
	}
	if _, err := o.PackageUnit(ctx, admproto.PackageUnit{Key: "fail2ban", On: true}); err == nil {
		t.Fatal("switched a package that is not installed")
	}
	h.installed["fail2ban"] = "1.0.2-3"
	if _, err := o.PackageUnit(ctx, admproto.PackageUnit{Key: "wireguard", On: true}); err == nil {
		t.Fatal("wireguard has no service")
	}
	if _, err := o.PackageUnit(ctx, admproto.PackageUnit{Key: "fail2ban", On: false}); err != nil {
		t.Fatal(err)
	}
	if nodeVersion("loft-node 1.0.20 (protocol 1.0)") != "1.0.20" {
		t.Fatal("node version")
	}
	if nodeVersion("loft-node 1.0.22 (LOFT/1.0)") != "1.0.22" {
		t.Fatal("node version LOFT")
	}
}

func tgz(t *testing.T, files map[string]string) []byte {
	var b bytes.Buffer
	zw := gzip.NewWriter(&b)
	tw := tar.NewWriter(zw)
	for n, c := range files {
		tw.WriteHeader(&tar.Header{Name: n, Mode: 0o755, Size: int64(len(c)), Typeflag: tar.TypeReg})
		tw.Write([]byte(c))
	}
	tw.Close()
	zw.Close()
	return b.Bytes()
}

func TestUpdateChecksSum(t *testing.T) {
	o, _ := pkgOps(t)
	body := tgz(t, map[string]string{"loft-panel-9.9.9-linux-amd64/install.sh": "#!/bin/sh\n"})
	h := sha256.Sum256(body)
	good := hex.EncodeToString(h[:])
	sums := good
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch filepath.Base(r.URL.Path) {
		case "loft-panel-9.9.9-SHA256SUMS.txt":
			fmt.Fprintf(w, "%s  ./linux-amd64/loft-panel-9.9.9-linux-amd64.tar.gz\n", sums)
		case "loft-panel-9.9.9-linux-amd64.tar.gz":
			w.Write(body)
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	o.ReleaseBase = srv.URL
	r := &runner{o: o, j: &admproto.Job{}, w: io.Discard}
	dir := t.TempDir()
	p, err := r.fetchChecked(context.Background(), "v9.9.9", "loft-panel-9.9.9-SHA256SUMS.txt", "loft-panel-9.9.9-linux-amd64.tar.gz", dir)
	if err != nil || filepath.Base(p) != "loft-panel-9.9.9-linux-amd64.tar.gz" {
		t.Fatal(p, err)
	}
	sums = strings.Repeat("0", 64)
	if _, err := r.fetchChecked(context.Background(), "v9.9.9", "loft-panel-9.9.9-SHA256SUMS.txt", "loft-panel-9.9.9-linux-amd64.tar.gz", dir); err == nil || !strings.Contains(err.Error(), "does not match") {
		t.Fatalf("bad sum accepted: %v", err)
	}
}

func TestUpdateTagChecked(t *testing.T) {
	o, _ := pkgOps(t)
	for _, tag := range []string{"", "1.0.0", "v1.0", "v1.0.0;rm -rf", "v1.0.0/../x"} {
		if _, err := o.StartUpdate(context.Background(), "admin", admproto.UpdateApply{Tag: tag}); err == nil {
			t.Fatalf("%q accepted", tag)
		}
	}
}

func must[T any](v T, err error) T {
	if err != nil {
		panic(err)
	}
	return v
}

func TestMailPackages(t *testing.T) {
	o, h := pkgOps(t)
	ctx := context.Background()
	if _, err := o.StartPackages(ctx, "admin", catalog.Plan{Install: []string{"webmail"}}); err == nil || !strings.Contains(err.Error(), "needs") {
		t.Fatalf("webmail without the mail server: %v", err)
	}
	if _, err := o.StartPackages(ctx, "admin", catalog.Plan{Install: []string{"webmail", "mail"}}); err != nil {
		t.Fatal(err)
	}
	o.RunJob(ctx)
	h.jobUnit = false
	if j, _ := o.JobGet(ctx, true); j.State != "done" {
		t.Fatalf("%+v", j)
	}
	all := strings.Join(h.ran, "\n")
	if !strings.Contains(all, "debconf-set-selections") || strings.Index(all, "postfix") > strings.Index(all, "roundcube-core") {
		t.Fatalf("preseed or order:\n%s", all)
	}
	if _, err := o.StartPackages(ctx, "admin", catalog.Plan{Remove: []string{"mail"}}); err == nil {
		t.Fatal("mail removed under webmail")
	}
	h.ran = nil
	if _, err := o.StartPackages(ctx, "admin", catalog.Plan{Remove: []string{"mail", "webmail"}}); err != nil {
		t.Fatal(err)
	}
	o.RunJob(ctx)
	all = strings.Join(h.ran, "\n")
	if !strings.Contains(all, "remove dovecot-core") || strings.Contains(all, "zerofree") {
		t.Fatalf("leftovers:\n%s", all)
	}
	if strings.Index(all, "remove php-fpm") > strings.Index(all, "remove postfix") {
		t.Fatalf("mail removed before webmail:\n%s", all)
	}
}

func TestTimezone(t *testing.T) {
	o, _ := pkgOps(t)
	var set string
	o.Exec = func(_ context.Context, name string, args ...string) (string, error) {
		switch {
		case args[0] == "show":
			return firstNonEmpty(set, "Etc/UTC") + "\n", nil
		case args[0] == "list-timezones":
			return "Etc/UTC\nEurope/Moscow\n", nil
		case args[0] == "set-timezone":
			set = args[1]
		}
		return "", nil
	}
	if _, err := o.TimezoneSet(context.Background(), admproto.Timezone{Zone: "../../etc/passwd"}); err == nil {
		t.Fatal("unknown zone set")
	}
	tz, err := o.TimezoneSet(context.Background(), admproto.Timezone{Zone: "Europe/Moscow"})
	if err != nil || tz.Zone != "Europe/Moscow" || len(tz.Zones) != 2 {
		t.Fatalf("%+v %v", tz, err)
	}
}

func TestReleasePackage(t *testing.T) {
	o, h := pkgOps(t)
	var zb bytes.Buffer
	zw := zip.NewWriter(&zb)
	w, _ := zw.Create("xray")
	w.Write([]byte("#!/bin/sh\necho Xray 26.3.27\n"))
	w, _ = zw.Create("geoip.dat")
	w.Write([]byte("x"))
	zw.Close()
	sum := sha256.Sum256(zb.Bytes())
	asset := map[string]string{"amd64": "Xray-linux-64.zip", "arm64": "Xray-linux-arm64-v8a.zip"}[runtime.GOARCH]
	dgst := "MD5= 00\nSHA2-256= " + hex.EncodeToString(sum[:]) + "\n"
	srv := httptest.NewServer(http.HandlerFunc(func(rw http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "/releases/latest"):
			rw.Write([]byte(`{"tag_name":"v26.3.27"}`))
		case strings.HasSuffix(r.URL.Path, asset+".dgst"):
			rw.Write([]byte(dgst))
		case strings.HasSuffix(r.URL.Path, asset):
			rw.Write(zb.Bytes())
		default:
			http.NotFound(rw, r)
		}
	}))
	defer srv.Close()
	o.ReleaseAPI, o.ReleaseDL = srv.URL, srv.URL
	ctx := context.Background()
	o.StartPackages(ctx, "admin", catalog.Plan{Install: []string{"xray"}})
	o.RunJob(ctx)
	h.jobUnit = false
	if j, _ := o.JobGet(ctx, true); j.State != "done" {
		t.Fatalf("%+v", j)
	}
	if _, err := os.Stat(filepath.Join(o.Root, "usr/local/lib/loft-panel/xray/xray")); err != nil {
		t.Fatal("not unpacked")
	}
	if b, _ := os.ReadFile(filepath.Join(o.Root, "etc/systemd/system/loft-panel-xray.service")); !bytes.Contains(b, []byte("LoadCredential=config.json")) {
		t.Fatal("no unit")
	}
	if strings.Contains(strings.Join(h.ran, "\n"), "apt-get") {
		t.Fatal("apt ran for a release package")
	}
	// A digest that does not match is refused and nothing is replaced.
	dgst = "SHA2-256= " + strings.Repeat("0", 64) + "\n"
	o.StartPackages(ctx, "admin", catalog.Plan{Upgrade: []string{"xray"}})
	o.RunJob(ctx)
	h.jobUnit = false
	if j, _ := o.JobGet(ctx, false); j.State != "error" || !strings.Contains(j.Error, "does not match") {
		t.Fatalf("%+v", j)
	}
	o.StartPackages(ctx, "admin", catalog.Plan{Remove: []string{"xray"}})
	o.RunJob(ctx)
	if _, err := os.Stat(filepath.Join(o.Root, "usr/local/lib/loft-panel/xray")); err == nil {
		t.Fatal("left after remove")
	}
}

func pkgBy(l []admproto.Package, key string) admproto.Package {
	for _, p := range l {
		if p.Key == key {
			return p
		}
	}
	return admproto.Package{}
}

func mtgServer(t *testing.T, body []byte) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if filepath.Base(r.URL.Path) != "mtg-2.2.8-linux-amd64.tar.gz" {
			http.NotFound(w, r)
			return
		}
		w.Write(body)
	}))
}

func TestMTProtoJob(t *testing.T) {
	o, h := pkgOps(t)
	ctx := context.Background()
	o.GOARCH = "amd64"
	body := tgz(t, map[string]string{"mtg": "#!/bin/sh\necho mtg\n"})
	sum := sha256.Sum256(body)
	o.BinSHA256 = map[string]string{"mtg-2.2.8-linux-amd64.tar.gz": hex.EncodeToString(sum[:])}
	srv := mtgServer(t, body)
	defer srv.Close()
	o.BinBase, o.HTTP = srv.URL, srv.Client()

	if _, err := o.StartPackages(ctx, "admin", catalog.Plan{Install: []string{"mtproto"}}); err != nil {
		t.Fatal(err)
	}
	if err := o.RunJob(ctx); err != nil {
		t.Fatal(err)
	}
	h.jobUnit = false
	got, _ := o.JobGet(ctx, true)
	if got.State != "done" {
		t.Fatalf("job %+v", got)
	}
	if strings.Contains(strings.Join(h.ran, "\n"), "apt-get") {
		t.Fatalf("apt ran for a binary package:\n%s", h.ran)
	}
	bin := filepath.Join(o.Root, "usr/lib/loft-panel/mtproto/mtg")
	if _, err := os.Stat(bin); err != nil {
		t.Fatal(err)
	}
	unit, _ := os.ReadFile(filepath.Join(o.Root, "etc/systemd/system/loft-mtproto.service"))
	if !bytes.Contains(unit, []byte("mtg run /var/lib/loft-mtproto/mtg.toml")) {
		t.Fatalf("unit: %s", unit)
	}
	st, err := o.MTProtoGet()
	if err != nil || !st.Installed || st.Port != 8448 || st.Domain != "cloudflare.com" || !strings.HasPrefix(st.Secret, "ee") {
		t.Fatalf("settings %+v %v", st, err)
	}
	toml, _ := os.ReadFile(filepath.Join(o.Root, "var/lib/loft-mtproto/mtg.toml"))
	if !bytes.Contains(toml, []byte(st.Secret)) || !bytes.Contains(toml, []byte("0.0.0.0:8448")) {
		t.Fatalf("toml: %s", toml)
	}
	pk := pkgBy(must(o.Packages(ctx)), "mtproto")
	if !pk.Installed || pk.Version != "2.2.8" || pk.Active != "active" {
		t.Fatalf("state %+v", pk)
	}

	secret := st.Secret
	if _, err := o.StartPackages(ctx, "admin", catalog.Plan{Remove: []string{"mtproto"}}); err != nil {
		t.Fatal(err)
	}
	o.RunJob(ctx)
	h.jobUnit = false
	if _, err := os.Stat(bin); err == nil {
		t.Fatal("binary left")
	}
	if _, err := os.Stat(filepath.Join(o.Root, "var/lib/loft-mtproto/settings.json")); err != nil {
		t.Fatal("settings wiped without purge")
	}
	if _, err := o.StartPackages(ctx, "admin", catalog.Plan{Install: []string{"mtproto"}}); err != nil {
		t.Fatal(err)
	}
	o.RunJob(ctx)
	h.jobUnit = false
	st, _ = o.MTProtoGet()
	if st.Secret != secret {
		t.Fatalf("secret rotated on reinstall: %s vs %s", st.Secret, secret)
	}

	st, err = o.MTProtoSet(ctx, admproto.MTProtoSet{Port: 9000, Domain: "www.example.com"})
	if err != nil || st.Port != 9000 || st.Domain != "www.example.com" || st.Secret == secret {
		t.Fatalf("set %+v %v", st, err)
	}
	if _, err := o.MTProtoSet(ctx, admproto.MTProtoSet{Port: 443}); err == nil {
		t.Fatal("port 443 accepted")
	}
	if _, err := o.MTProtoSet(ctx, admproto.MTProtoSet{Domain: "not a domain"}); err == nil {
		t.Fatal("bad domain accepted")
	}

	if _, err := o.StartPackages(ctx, "admin", catalog.Plan{Remove: []string{"mtproto"}, Purge: true}); err != nil {
		t.Fatal(err)
	}
	o.RunJob(ctx)
	if _, err := os.Stat(filepath.Join(o.Root, "var/lib/loft-mtproto")); err == nil {
		t.Fatal("data left after purge")
	}
	if st, _ := o.MTProtoGet(); st.Installed {
		t.Fatal("still installed")
	}
}

func TestMTProtoSecret(t *testing.T) {
	s, err := mtprotoSecret("cloudflare.com")
	if err != nil || !strings.HasPrefix(s, "ee") {
		t.Fatal(s, err)
	}
	want := hex.EncodeToString([]byte("cloudflare.com"))
	if !strings.HasSuffix(s, want) || len(s) != 2+32+len(want) {
		t.Fatalf("shape %s", s)
	}
}

func TestMTProtoBadSum(t *testing.T) {
	o, h := pkgOps(t)
	o.GOARCH = "amd64"
	body := tgz(t, map[string]string{"mtg": "x"})
	o.BinSHA256 = map[string]string{"mtg-2.2.8-linux-amd64.tar.gz": strings.Repeat("0", 64)}
	srv := mtgServer(t, body)
	defer srv.Close()
	o.BinBase, o.HTTP = srv.URL, srv.Client()
	o.StartPackages(context.Background(), "admin", catalog.Plan{Install: []string{"mtproto"}})
	o.RunJob(context.Background())
	h.jobUnit = false
	j, _ := o.JobGet(context.Background(), true)
	if j.State != "error" || !strings.Contains(j.Error, "does not match the catalog") {
		t.Fatalf("%+v", j)
	}
}

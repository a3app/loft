package main

import (
	"context"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"
)

func TestRedact(t *testing.T) {
	in := "Oct 27 12:34:56.000 [warn] Proxy Client: unable to connect to 203.0.113.7:443 " +
		"[2001:db8::1]:9001 fe80:0:0:0:1:2:3:4 obfs4 198.51.100.2:80 ABCDEF0123456789ABCDEF0123456789ABCDEF01 cert=AbC+/x= iat-mode=0 " +
		"url=https://example.org/x local 127.0.0.1:9050"
	out := redact(in)
	for _, leak := range []string{"203.0.113.7", "198.51.100.2", "2001:db8::1", "fe80:0:0:0", "ABCDEF0123456789", "AbC+/x=", "example.org"} {
		if strings.Contains(out, leak) {
			t.Fatalf("%q left in %q", leak, out)
		}
	}
	for _, keep := range []string{"12:34:56.000", "127.0.0.1:9050", "iat-mode=0", "[warn]"} {
		if !strings.Contains(out, keep) {
			t.Fatalf("%q lost from %q", keep, out)
		}
	}
}

func TestBridgeFileAndPlugins(t *testing.T) {
	p := filepath.Join(t.TempDir(), "b.txt")
	os.WriteFile(p, []byte("# mine\n\nBridge obfs4 192.0.2.1:443 FP cert=x iat-mode=0\nwebtunnel [2001:db8::2]:443 FP url=https://a/b\n192.0.2.9:9001\n"), 0o644)
	got, err := readBridgeFile(p)
	if err != nil {
		t.Fatal(err)
	}
	if len(got["obfs4"]) != 1 || len(got["webtunnel"]) != 1 || len(got["vanilla"]) != 1 {
		t.Fatalf("%v", got)
	}
	if !strings.HasPrefix(got["obfs4"][0], "obfs4 ") {
		t.Fatalf("Bridge prefix kept: %q", got["obfs4"][0])
	}

	cfg := &ptConfig{PluggableTransports: map[string]string{
		"lyrebird":  "ClientTransportPlugin meek_lite,obfs2,obfs3,obfs4,scramblesuit,webtunnel exec ${pt_path}lyrebird${pt_suffix}",
		"snowflake": "ClientTransportPlugin snowflake exec ${pt_path}snowflake-client${pt_suffix}",
	}}
	lines := pluginsOf(cfg, "/Tor Browser.app/pt")
	if p, ok := pluginFor(lines, "webtunnel"); !ok || p.Path != "/Tor Browser.app/pt"+string(filepath.Separator)+"lyrebird" {
		t.Fatalf("webtunnel plugin %+v", p)
	}
	if _, ok := pluginFor(lines, "conjure"); ok {
		t.Fatal("conjure has no plugin here")
	}
	if runtime.GOOS != "windows" {
		linked := linkPlugins(lines, t.TempDir())
		for _, p := range linked {
			if strings.Contains(p.Path, " ") {
				t.Fatalf("path with a space reached torrc: %q", p.Path)
			}
		}
	}
	c := bridgeCase("file:conjure", []string{"conjure 192.0.2.3:80 FP"}, lines)
	if c.Skip == "" {
		t.Fatal("conjure without plugin should be skipped")
	}
}

func TestCaseOrder(t *testing.T) {
	env := &torEnv{
		Plugins:   []plugin{{Names: []string{"meek_lite", "obfs4"}, Path: "/x"}, {Names: []string{"snowflake"}, Path: "/y"}},
		Builtin:   map[string][]string{"meek-azure": {"meek_lite 192.0.2.1:80 FP url=u"}, "obfs4": {"obfs4 192.0.2.2:443 FP cert=c"}, "snowflake": {"snowflake 192.0.2.3:80 FP"}},
		Recommend: "snowflake",
	}
	cases := buildCases(env, map[string][]string{"obfs4": {"obfs4 192.0.2.4:443 FP"}}, "127.0.0.1:1")
	var names []string
	for _, c := range cases {
		names = append(names, c.Name)
	}
	want := "direct builtin:snowflake builtin:meek-azure builtin:obfs4 file:obfs4 loft"
	if strings.Join(names, " ") != want {
		t.Fatalf("order %v", names)
	}
	if cases[len(cases)-1].Skip == "" {
		t.Fatal("loft with nothing listening should be skipped")
	}
	if _, err := pickCases(cases, "direct,nope"); err == nil {
		t.Fatal("unknown case accepted")
	}
	rc := torrcFor(cases[1], "/d", 9999)
	for _, s := range []string{"UseBridges 1", "ClientTransportPlugin snowflake exec /y", "Bridge snowflake ", "SocksPort 127.0.0.1:9999"} {
		if !strings.Contains(rc, s) {
			t.Fatalf("torrc lacks %q:\n%s", s, rc)
		}
	}
	if rc := torrcFor(torCase{Name: "loft", Proxy: "127.0.0.1:1080"}, "/d", 1); !strings.Contains(rc, "Socks5Proxy 127.0.0.1:1080") || strings.Contains(rc, "UseBridges") {
		t.Fatalf("loft torrc:\n%s", rc)
	}
}

// fakeTor writes a script that prints tor-like lines, so runCase can be
// tested without a network.
func fakeTor(t *testing.T, body string) *torEnv {
	if runtime.GOOS == "windows" {
		t.Skip("shell script tor")
	}
	p := filepath.Join(t.TempDir(), "tor")
	os.WriteFile(p, []byte("#!/bin/sh\n"+body), 0o755)
	return &torEnv{Tor: p}
}

func TestRunCaseReaches100(t *testing.T) {
	env := fakeTor(t, `echo "Oct 27 10:00:00.000 [notice] Bootstrapped 0% (starting): Starting"
echo "Oct 27 10:00:00.100 [warn] Proxy Client: unable to connect to 203.0.113.7:443"
echo "Oct 27 10:00:00.200 [notice] Bootstrapped 50% (loading_descriptors): Loading relay descriptors"
echo "Oct 27 10:00:00.300 [notice] Bootstrapped 100% (done): Done"
sleep 30
`)
	r := runCase(context.Background(), env, torCase{Name: "direct"}, 10*time.Second, "")
	if r.Outcome != "ok" || r.Max != 100 || len(r.Steps) != 3 {
		t.Fatalf("%+v", r)
	}
	if r.IsTor == "yes" {
		t.Fatal("fake tor has no SOCKS, the check cannot say yes")
	}
	if strings.Contains(r.Log, "203.0.113.7") || len(r.Warnings) != 1 {
		t.Fatalf("log %q warnings %v", r.Log, r.Warnings)
	}
}

func TestRunCaseStuckAndFailed(t *testing.T) {
	env := fakeTor(t, `echo "Oct 27 10:00:00.000 [notice] Bootstrapped 10% (conn_done): Connected to a relay"
sleep 30
`)
	r := runCase(context.Background(), env, torCase{Name: "direct"}, 3*time.Second, "")
	if r.Outcome != "stuck" || r.Max != 10 {
		t.Fatalf("%+v", r)
	}
	env = fakeTor(t, `echo "Oct 27 10:00:00.000 [err] Reading config failed"
exit 1
`)
	r = runCase(context.Background(), env, torCase{Name: "direct"}, 5*time.Second, "")
	if r.Outcome != "failed" || len(r.Warnings) != 1 {
		t.Fatalf("%+v", r)
	}
	md := renderReport(meta{Network: "test | net", Started: time.Now()}, []result{r, {Case: torCase{Name: "loft", Skip: "nothing listens"}, Outcome: "skipped"}})
	for _, s := range []string{"| direct | failed | 0% |", "test \\| net", "| loft | skipped |", "logs/direct.log"} {
		if !strings.Contains(md, s) {
			t.Fatalf("report lacks %q:\n%s", s, md)
		}
	}
}

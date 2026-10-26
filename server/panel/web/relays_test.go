package web

// The Relays dialog: install command and node config from node.conf, and
// the offline bundle built from a checked release archive.

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/a3app/loft/internal/version"
	"github.com/a3app/loft/server/panel/admproto"
)

const testCtlPub = "ab12ab12ab12ab12ab12ab12ab12ab12ab12ab12ab12ab12ab12ab12ab12ab12"

type relayAdm struct{ issued *int }

func (f relayAdm) Call(_ context.Context, op string, body, out any) error {
	set := func(v any) { b, _ := json.Marshal(v); json.Unmarshal(b, out) }
	switch op {
	case admproto.OpNodeConfig:
		set(admproto.NodeConfig{Advertise: "https://api.example.net", Fronts: []string{"get", "ws"}, WSPath: "/api/v1/sync",
			ControllerPub: testCtlPub, Relays: []admproto.Relay{{ID: "ru1", URL: "https://ru1.example.net", Fronts: []string{"get"}, Key: true},
				{ID: "r2", URL: "https://r2.example.net", Key: true, Replica: true}}})
	case admproto.OpRelayKey:
		*f.issued++
		set(admproto.IssuedKey{ID: "ru1", Key: fmt.Sprintf("lt_new%d", *f.issued)})
	case admproto.OpRelayGet:
		set(admproto.IssuedKey{ID: "ru1", Key: "lt_kept"})
	}
	return nil
}

func TestRelayInstall(t *testing.T) {
	e := newEnv(t)
	c, csrf := e.owner()
	issued := 0
	e.srv.Adm = func(string) Adm { return relayAdm{issued: &issued} }

	r := e.do("POST", "/api/relays/key", map[string]string{"id": "ru1"}, c, csrf)
	if r.code != 200 || r.body["key"] != "lt_new1" || issued != 1 {
		t.Fatalf("issue %d %v", r.code, r.body)
	}
	cmd, _ := r.body["command"].(string)
	for _, want := range []string{"--role relay", "--id ru1", "--advertise https://ru1.example.net",
		"--controller 'wss://api.example.net/api/v1/sync wss://r2.example.net/api/v1/sync'", "--controller-pub " + testCtlPub, "--relay-key lt_new1",
		"V=" + version.Version, "--witness 'r2.example.net:4443'"} {
		if !strings.Contains(cmd, want) {
			t.Fatalf("command lacks %q: %s", want, cmd)
		}
	}
	conf, _ := r.body["config"].(string)
	if !strings.Contains(conf, "controller_pub "+testCtlPub) || strings.Contains(conf, "lt_") {
		t.Fatalf("relay node.conf: %s", conf)
	}
	if r := e.do("GET", "/api/relays/key?id=ru1", nil, c, ""); r.code != 200 || r.body["key"] != "lt_kept" || issued != 1 {
		t.Fatalf("show must not reissue: %d %v", r.code, r.body)
	}
	if r := e.do("GET", "/api/relays/key?id=nope", nil, c, ""); r.code == 200 {
		t.Fatal("install for a relay not in node.conf")
	}
	// A standby installs as role replica and lists only the controller.
	r = e.do("GET", "/api/relays/key?id=r2", nil, c, "")
	cmd, _ = r.body["command"].(string)
	if r.code != 200 || !strings.Contains(cmd, "--role replica") || strings.Contains(cmd, "r2.example.net/api") ||
		!strings.Contains(cmd, "--witness-listen :4443") || strings.Contains(cmd, "--witness '") {
		t.Fatalf("standby install: %d %s", r.code, cmd)
	}
}

func tarGz(t *testing.T, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	zw := gzip.NewWriter(&buf)
	tw := tar.NewWriter(zw)
	for name, body := range files {
		tw.WriteHeader(&tar.Header{Name: name, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg})
		tw.Write([]byte(body))
	}
	tw.Close()
	zw.Close()
	return buf.Bytes()
}

func TestRelayBundle(t *testing.T) {
	name := fmt.Sprintf("loft-node-%s-linux-amd64", version.Version)
	archive := tarGz(t, map[string]string{name + "/install.sh": "#!/bin/sh\n", name + "/loft-node": "bin"})
	sum := sha256.Sum256(archive)
	sums := hex.EncodeToString(sum[:]) + "  " + name + ".tar.gz\n"
	rel := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasSuffix(r.URL.Path, "SHA256SUMS.txt"):
			io.WriteString(w, sums)
		case strings.HasSuffix(r.URL.Path, name+".tar.gz"):
			w.Write(archive)
		default:
			http.NotFound(w, r)
		}
	}))
	defer rel.Close()
	old := relayReleases
	relayReleases = rel.URL
	t.Cleanup(func() { relayReleases = old })

	e := newEnv(t)
	c, _ := e.owner()
	issued := 0
	e.srv.Adm = func(string) Adm { return relayAdm{issued: &issued} }
	r := e.do("GET", "/api/relays/bundle?id=ru1&arch=amd64", nil, c, "")
	if r.code != 200 {
		t.Fatalf("bundle %d %s", r.code, r.raw())
	}
	zr, err := gzip.NewReader(strings.NewReader(r.raw()))
	if err != nil {
		t.Fatal(err)
	}
	tr := tar.NewReader(zr)
	found := map[string]string{}
	for {
		h, err := tr.Next()
		if err != nil {
			break
		}
		b, _ := io.ReadAll(tr)
		found[h.Name] = string(b)
	}
	script := found[name+"/install-relay.sh"]
	if found[name+"/loft-node"] != "bin" || !strings.Contains(script, "--relay-key lt_kept") || !strings.Contains(script, "./install.sh") {
		t.Fatalf("bundle: %v", found)
	}

	sums = strings.Repeat("0", 64) + "  " + name + ".tar.gz\n"
	if r := e.do("GET", "/api/relays/bundle?id=ru1&arch=amd64", nil, c, ""); r.code == 200 {
		t.Fatal("bundle from an archive that does not match SHA256SUMS")
	}
}

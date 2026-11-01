package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/a3app/loft/internal/version"
	"github.com/a3app/loft/server/panel/store"
)

func TestCmpVersion(t *testing.T) {
	for _, c := range []struct {
		a, b string
		want int
	}{{"1.0.20", "1.0.20", 0}, {"1.0.21", "1.0.20", 1}, {"1.0.9", "1.0.10", -1}, {"2.0.0", "1.9.99", 1},
		{"1.0.21-rc1", "1.0.21", -1}, {"1.0.21-rc1", "1.0.20", 1}, {"1.0.21-rc2", "1.0.21-rc1", 1}} {
		if got := cmpVersion(c.a, c.b); got != c.want {
			t.Fatalf("%s vs %s: %d", c.a, c.b, got)
		}
	}
}

func rel(tag string, pre bool, assets ...string) ghRelease {
	r := ghRelease{Tag: tag, Prerelease: pre, Published: time.Date(2026, 10, 20, 0, 0, 0, 0, time.UTC)}
	for _, a := range assets {
		r.Assets = append(r.Assets, struct {
			Name string `json:"name"`
		}{a})
	}
	return r
}

func withPanel(tag string, pre bool) ghRelease {
	v := tag[1:]
	return rel(tag, pre, "loft-panel-"+v+"-linux-amd64.tar.gz", "loft-panel-"+v+"-SHA256SUMS.txt")
}

func TestLatestRelease(t *testing.T) {
	rels := []ghRelease{withPanel("v1.0.23-rc1", true), rel("v1.0.22", false, "loft-node-1.0.22-linux-amd64.tar.gz"), withPanel("v1.0.21", false), withPanel("v1.0.19", false)}
	if r, ok := latestRelease(rels, false, "amd64"); !ok || r.Tag != "v1.0.21" {
		t.Fatalf("stable: %v %v", r.Tag, ok)
	}
	if r, _ := latestRelease(rels, true, "amd64"); r.Tag != "v1.0.23-rc1" {
		t.Fatalf("pre: %v", r.Tag)
	}
	if _, ok := latestRelease(rels, false, "arm64"); ok {
		t.Fatal("arm64 has no panel asset")
	}
}

func TestInWindow(t *testing.T) {
	sun4 := time.Date(2026, 10, 25, 4, 30, 0, 0, time.Local) // Sunday
	mon4 := sun4.Add(24 * time.Hour)
	if !inWindow("daily", mon4) || inWindow("daily", mon4.Add(time.Hour)) || !inWindow("sunday", sun4) || inWindow("sunday", mon4) || !inWindow("now", mon4.Add(5*time.Hour)) {
		t.Fatal("window")
	}
}

func TestUpdateAPI(t *testing.T) {
	e := newEnv(t)
	c, csrf := e.owner()
	next := fmt.Sprintf("v%d.0.0", 90)
	gh := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		json.NewEncoder(w).Encode([]ghRelease{withPanel(next, false), withPanel("v"+version.Version, false)})
	}))
	defer gh.Close()
	e.srv.ReleasesAPI = gh.URL
	if r := e.do("GET", "/api/update", nil, c, ""); r.code != 200 || r.body["mode"] != "notify" || r.body["available"] != false {
		t.Fatalf("get %d %v", r.code, r.body)
	}
	if r := e.do("POST", "/api/update/apply", map[string]string{"tag": next}, c, csrf); r.code != 409 {
		t.Fatalf("apply before a check: %d", r.code)
	}
	if r := e.do("POST", "/api/update/check", map[string]string{}, c, csrf); r.code != 200 || r.body["latest"] != next || r.body["available"] != true {
		t.Fatalf("check %d %v", r.code, r.body)
	}
	if r := e.do("POST", "/api/update/apply", map[string]string{"tag": "v0.0.1"}, c, csrf); r.code != 409 {
		t.Fatalf("other tag: %d", r.code)
	}
	if r := e.do("POST", "/api/update/apply", map[string]string{"tag": next}, c, csrf); r.code != 200 || r.body["id"] != "j1" {
		t.Fatalf("apply %d %v", r.code, r.body)
	}
	if r := e.do("POST", "/api/update/settings", map[string]string{"mode": "auto", "channel": "pre", "window": "sunday"}, c, csrf); r.code != 200 || r.body["mode"] != "auto" || r.body["latest"] != nil {
		t.Fatalf("settings %d %v", r.code, r.body)
	}
	if r := e.do("POST", "/api/update/settings", map[string]string{"mode": "yes", "channel": "pre", "window": "sunday"}, c, csrf); r.code != 400 {
		t.Fatalf("bad mode: %d", r.code)
	}
	st, _ := e.srv.Settings.Get()
	if st.Update != (store.Update{Mode: "auto", Channel: "pre", Window: "sunday", CheckError: st.Update.CheckError, LatestDate: st.Update.LatestDate}) {
		t.Fatalf("stored %+v", st.Update)
	}
}

func TestPackagesAPI(t *testing.T) {
	e := newEnv(t)
	c, csrf := e.owner()
	r := e.do("GET", "/api/packages", nil, c, "")
	if r.code != 200 || len(r.body["packages"].([]any)) != 2 || r.body["job"].(map[string]any)["id"] != "j1" {
		t.Fatalf("list %d %v", r.code, r.body)
	}
	if r := e.do("POST", "/api/packages/apply", map[string]any{"install": []string{"node"}}, c, csrf); r.code != 400 {
		t.Fatalf("base package accepted: %d", r.code)
	}
	if r := e.do("POST", "/api/packages/apply", map[string]any{"install": []string{"wireguard"}, "shell": "x"}, c, csrf); r.code != 400 {
		t.Fatalf("unknown field accepted: %d", r.code)
	}
	if r := e.do("POST", "/api/packages/apply", map[string]any{"install": []string{"wireguard"}}, c, csrf); r.code != 200 || r.body["state"] != "running" {
		t.Fatalf("apply %d %v", r.code, r.body)
	}
}

func TestMTProtoAPI(t *testing.T) {
	e := newEnv(t)
	c, csrf := e.owner()
	r := e.do("GET", "/api/packages/mtproto", nil, c, "")
	if r.code != 200 || r.body["port"] != float64(8448) || r.body["domain"] != "cloudflare.com" {
		t.Fatalf("get %d %v", r.code, r.body)
	}
	if r := e.do("POST", "/api/packages/mtproto", map[string]any{"port": 9000, "domain": "www.example.com"}, c, csrf); r.code != 200 || r.body["port"] != float64(9000) {
		t.Fatalf("set %d %v", r.code, r.body)
	}
	if r := e.do("POST", "/api/packages/mtproto", map[string]any{"port": 9000, "shell": "x"}, c, csrf); r.code != 400 {
		t.Fatalf("unknown field: %d", r.code)
	}
}

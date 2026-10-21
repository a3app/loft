package web

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/a3app/loft/server/panel/admproto"
)

func TestKeepMemberKeys(t *testing.T) {
	e := newEnv(t)
	c, csrf := e.owner()
	if r := e.do("GET", "/api/settings", nil, c, ""); r.code != 200 || r.body["keep_member_keys"] != false {
		t.Fatalf("default keep %d %v", r.code, r.body)
	}
	if r := e.do("GET", "/api/state", nil, c, ""); r.body["keep_member_keys"] != false {
		t.Fatalf("state keep %v", r.body)
	}
	if r := e.do("GET", "/api/members/key?id=alice", nil, c, ""); r.code != 404 || r.body["error"] != "member keys are not kept" {
		t.Fatalf("get while off %d %v", r.code, r.body)
	}

	if r := e.do("POST", "/api/settings/panel", map[string]any{"session_idle": "30m", "session_max": "7d", "lang": "ru", "keep_member_keys": true}, c, csrf); r.code != 200 || r.body["keep_member_keys"] != true {
		t.Fatalf("turn on %d %v", r.code, r.body)
	}
	if r := e.do("GET", "/api/state", nil, c, ""); r.body["keep_member_keys"] != true {
		t.Fatalf("state on %v", r.body)
	}

	issued := e.do("POST", "/api/members/key", map[string]string{"id": "alice"}, c, csrf)
	if issued.code != 200 || !strings.HasPrefix(issued.body["key"].(string), "lt_") {
		t.Fatalf("issue %d %v", issued.code, issued.body)
	}
	key := issued.body["key"].(string)
	if strings.Contains(e.do("GET", "/api/settings", nil, c, "").raw(), key) {
		t.Fatal("plaintext key in GET /api/settings")
	}
	view := e.do("GET", "/api/members/key?id=alice", nil, c, "")
	if view.code != 200 || view.body["key"] != key || view.body["link"] == nil {
		t.Fatalf("view stored %d %v", view.code, view.body)
	}

	// Omitted keep_member_keys must not wipe (wizard POSTs idle/max/lang only).
	if r := e.do("POST", "/api/settings/panel", map[string]string{"session_idle": "8h", "session_max": "7d", "lang": "en"}, c, csrf); r.code != 200 || r.body["keep_member_keys"] != true {
		t.Fatalf("omit keep %d %v", r.code, r.body)
	}
	if r := e.do("GET", "/api/members/key?id=alice", nil, c, ""); r.code != 200 || r.body["key"] != key {
		t.Fatalf("still stored %d %v", r.code, r.body)
	}

	if r := e.do("GET", "/api/members/key?id=nobody", nil, c, ""); r.code != 404 || r.body["error"] != "key is not stored" {
		t.Fatalf("missing %d %v", r.code, r.body)
	}
	if r := e.do("GET", "/api/members/key", nil, c, ""); r.code != 400 {
		t.Fatalf("no id %d", r.code)
	}

	if r := e.do("POST", "/api/settings/panel", map[string]any{"session_idle": "30m", "session_max": "7d", "lang": "ru", "keep_member_keys": false}, c, csrf); r.code != 200 || r.body["keep_member_keys"] != false {
		t.Fatalf("turn off %d %v", r.code, r.body)
	}
	if r := e.do("GET", "/api/members/key?id=alice", nil, c, ""); r.code != 404 || r.body["error"] != "member keys are not kept" {
		t.Fatalf("after off %d %v", r.code, r.body)
	}
	if _, ok, _ := e.srv.Keys.Get("alice"); ok {
		t.Fatal("wipe left the panel copy")
	}
}

func TestKeepMemberKeysPeekAndPanelCopy(t *testing.T) {
	e := newEnv(t)
	c, csrf := e.owner()
	issued := e.do("POST", "/api/members/key", map[string]string{"id": "bob"}, c, csrf)
	if issued.code != 200 {
		t.Fatalf("issue off %d %v", issued.code, issued.body)
	}
	key := issued.body["key"].(string)
	if r := e.do("GET", "/api/members/key?id=bob", nil, c, ""); r.code != 404 {
		t.Fatalf("peek while off %d", r.code)
	}
	e.do("POST", "/api/settings/panel", map[string]any{"session_idle": "30m", "session_max": "7d", "lang": "ru", "keep_member_keys": true}, c, csrf)
	view := e.do("GET", "/api/members/key?id=bob", nil, c, "")
	if view.code != 200 || view.body["key"] != key {
		t.Fatalf("peek after on %d %v", view.code, view.body)
	}
	got, ok, err := e.srv.Keys.Get("bob")
	if err != nil || !ok || got.Key != key {
		t.Fatalf("cached %+v ok=%v err=%v", got, ok, err)
	}

	e.srv.Adm = func(string) Adm { return noKeysAdm{} }
	view = e.do("GET", "/api/members/key?id=bob", nil, c, "")
	if view.code != 200 || view.body["key"] != key {
		t.Fatalf("panel copy after empty ring %d %v", view.code, view.body)
	}
}

func TestWizardMemberKeepsKey(t *testing.T) {
	e := newEnv(t)
	c, csrf := e.owner()
	e.do("POST", "/api/settings/panel", map[string]any{"session_idle": "30m", "session_max": "7d", "lang": "ru", "keep_member_keys": true}, c, csrf)
	j := e.job(e.do("POST", "/api/wizard/member", map[string]string{"id": "carol", "name": "Carol"}, c, csrf), c)
	res, _ := j["result"].(map[string]any)
	if j["state"] != "done" || res["key"] == nil {
		t.Fatalf("wizard %v", j)
	}
	key := res["key"].(string)
	view := e.do("GET", "/api/members/key?id=carol", nil, c, "")
	if view.code != 200 || view.body["key"] != key {
		t.Fatalf("wizard stored %d %v", view.code, view.body)
	}
}

type noKeysAdm struct{}

func (noKeysAdm) Call(_ context.Context, op string, _, out any) error {
	if op == admproto.OpMemberGet {
		return errors.New("no stored key for this member")
	}
	if op == admproto.OpNodeInfo {
		b, _ := json.Marshal(admproto.NodeInfo{Host: "panel.example.net", GetPath: "/static/js"})
		return json.Unmarshal(b, out)
	}
	return nil
}

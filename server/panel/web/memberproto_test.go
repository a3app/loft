package web

import (
	"testing"
)

func TestMemberProtoRoundTrip(t *testing.T) {
	e := newEnv(t)
	c, csrf := e.owner()
	got := e.do("GET", "/api/members/proto", nil, c, "")
	if got.code != 200 {
		t.Fatalf("get %d %v", got.code, got.body)
	}
	if m, _ := got.body["members"].(map[string]any); m == nil || len(m) != 0 {
		t.Fatalf("empty map %v", got.body)
	}
	if r := e.do("POST", "/api/members/proto", map[string]any{"id": "alice", "proto": []string{"vless", "loft", "vless"}}, c, csrf); r.code != 200 {
		t.Fatalf("set %d %v", r.code, r.body)
	}
	got = e.do("GET", "/api/members/proto", nil, c, "")
	alice, _ := got.body["members"].(map[string]any)["alice"].([]any)
	if len(alice) != 2 || alice[0] != "loft" || alice[1] != "vless" {
		t.Fatalf("order %v", got.body)
	}
	st, _ := e.srv.Settings.Get()
	if got := st.MemberProto["alice"]; len(got) != 2 || got[0] != "loft" || got[1] != "vless" {
		t.Fatalf("stored %+v", st.MemberProto)
	}
	if r := e.do("GET", "/api/settings", nil, c, ""); r.body["member_proto"] != nil {
		t.Fatalf("settings leaked member_proto: %v", r.body)
	}
	if r := e.do("POST", "/api/members/proto", map[string]any{"id": "alice", "proto": []string{"nope"}}, c, csrf); r.code != 400 {
		t.Fatalf("bad proto %d %v", r.code, r.body)
	}
	if r := e.do("POST", "/api/members/proto", map[string]any{"id": "-alice", "proto": []string{"loft"}}, c, csrf); r.code != 400 {
		t.Fatalf("bad id %d %v", r.code, r.body)
	}
	if r := e.do("POST", "/api/members/proto", map[string]any{"id": "alice", "delete": true}, c, csrf); r.code != 200 {
		t.Fatalf("delete %d %v", r.code, r.body)
	}
	got = e.do("GET", "/api/members/proto", nil, c, "")
	if m, _ := got.body["members"].(map[string]any); len(m) != 0 {
		t.Fatalf("after delete %v", got.body)
	}
	st, _ = e.srv.Settings.Get()
	if len(st.MemberProto) != 0 {
		t.Fatalf("stored after delete %+v", st.MemberProto)
	}
}

func TestMemberProtoEmptyList(t *testing.T) {
	e := newEnv(t)
	c, csrf := e.owner()
	if r := e.do("POST", "/api/members/proto", map[string]any{"id": "bob", "proto": []string{}}, c, csrf); r.code != 200 {
		t.Fatalf("empty %d %v", r.code, r.body)
	}
	got := e.do("GET", "/api/members/proto", nil, c, "")
	bob, ok := got.body["members"].(map[string]any)["bob"].([]any)
	if !ok || len(bob) != 0 {
		t.Fatalf("empty stored as missing: %v", got.body)
	}
}

func TestNormMemberProto(t *testing.T) {
	got, err := normMemberProto([]string{" SS ", "vless", "loft", "trojan", "vless"})
	if err != nil || !slicesEqual(got, []string{"loft", "vless", "trojan", "ss"}) {
		t.Fatalf("%v %v", got, err)
	}
	if _, err := normMemberProto([]string{"wireguard"}); err == nil {
		t.Fatal("want unknown")
	}
}

func slicesEqual(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestMemberProtoCopyIsolatesStore(t *testing.T) {
	in := map[string][]string{"a": {"loft"}}
	out := copyMemberProto(in)
	out["a"][0] = "vless"
	if in["a"][0] != "loft" {
		t.Fatal("alias")
	}
}

package web

import (
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/a3app/loft/server/panel/store"
)

func TestUsersRolesTokensAudit(t *testing.T) {
	e := newEnv(t)
	e.srv.Audit = store.OpenAudit(t.TempDir())
	c, csrf := e.owner()
	// A custom role that may view members only.
	r := e.do("POST", "/api/roles/save", map[string]any{"name": "support", "perms": map[string]int{"members": 1, "main": 1}}, c, csrf)
	if r.code != 200 {
		t.Fatalf("role %d %v", r.code, r.body)
	}
	rid := r.body["id"].(string)
	if r := e.do("POST", "/api/users/save", map[string]any{"new": true, "login": "bob", "name": "Bob", "role": rid, "password": "short"}, c, csrf); r.code != 400 {
		t.Fatalf("short password: %d", r.code)
	}
	if r := e.do("POST", "/api/users/save", map[string]any{"new": true, "login": "bob", "name": "Bob", "role": rid, "password": "correct horse battery"}, c, csrf); r.code != 200 {
		t.Fatalf("bob %d %v", r.code, r.body)
	}
	inv := e.do("POST", "/api/users/save", map[string]any{"new": true, "login": "carol", "role": "viewer", "invite": true}, c, csrf)
	link, _ := inv.body["invite"].(string)
	if inv.code != 200 || !strings.HasPrefix(link, "#invite/") {
		t.Fatalf("invite %d %v", inv.code, inv.body)
	}
	tok := strings.TrimPrefix(link, "#invite/")
	if r := e.do("GET", "/api/invite/"+tok, nil, nil, ""); r.code != 200 || r.body["login"] != "carol" {
		t.Fatalf("invite get %d %v", r.code, r.body)
	}
	if r := e.do("POST", "/api/invite", map[string]string{"token": tok, "password": "correct horse battery"}, nil, ""); r.code != 200 || r.cookie == nil {
		t.Fatalf("invite accept %d %v", r.code, r.body)
	}
	if r := e.do("POST", "/api/invite", map[string]string{"token": tok, "password": "correct horse battery"}, nil, ""); r.code != 404 {
		t.Fatalf("invite reused: %d", r.code)
	}

	// bob signs in: members yes, packages and users no.
	bl := e.do("POST", "/api/login", map[string]string{"login": "bob", "password": "correct horse battery"}, nil, "")
	if bl.code != 200 || bl.body["perms"].(map[string]any)["members"] != float64(1) {
		t.Fatalf("bob login %d %v", bl.code, bl.body)
	}
	bc, bcsrf := bl.cookie, bl.body["csrf"].(string)
	if r := e.do("GET", "/api/network", nil, bc, ""); r.code != 200 {
		t.Fatalf("bob network %d", r.code)
	}
	if r := e.do("GET", "/api/packages", nil, bc, ""); r.code != 403 {
		t.Fatalf("bob packages %d", r.code)
	}
	if r := e.do("POST", "/api/members/key", map[string]string{"id": "x"}, bc, bcsrf); r.code != 403 {
		t.Fatalf("bob changes members: %d", r.code)
	}
	if r := e.do("GET", "/api/users", nil, bc, ""); r.code != 403 {
		t.Fatalf("bob users %d", r.code)
	}

	// An API token stands for its user, without CSRF.
	tr := e.do("POST", "/api/users/token", map[string]string{"login": "bob"}, c, csrf)
	bt, _ := tr.body["token"].(string)
	if !strings.HasPrefix(bt, "lpt_") {
		t.Fatalf("token %v", tr.body)
	}
	req := httptest.NewRequest("GET", "/api/network", nil)
	req.Host = "panel.example.net"
	req.Header.Set("Authorization", "Bearer "+bt)
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, req)
	if w.Code != 200 {
		t.Fatalf("bearer %d", w.Code)
	}
	req.Header.Set("Authorization", "Bearer lpt_nope")
	w = httptest.NewRecorder()
	e.h.ServeHTTP(w, req)
	if w.Code != 401 {
		t.Fatalf("bad bearer %d", w.Code)
	}

	// Sessions: the owner ends bob's.
	sl := e.do("GET", "/api/sessions", nil, c, "")
	var bobKey string
	for _, x := range sl.body["sessions"].([]any) {
		if m := x.(map[string]any); m["user"] == "bob" {
			bobKey = m["key"].(string)
		}
	}
	if r := e.do("POST", "/api/sessions/end", map[string]string{"key": bobKey}, c, csrf); r.code != 200 {
		t.Fatalf("end %d", r.code)
	}
	if r := e.do("GET", "/api/network", nil, bc, ""); r.code != 401 {
		t.Fatalf("bob still in: %d", r.code)
	}
	if r := e.do("POST", "/api/roles/delete", map[string]string{"id": rid}, c, csrf); r.code != 400 {
		t.Fatalf("deleted a role in use: %d", r.code)
	}
	if r := e.do("POST", "/api/users/delete", map[string]string{"login": "admin"}, c, csrf); r.code != 400 {
		t.Fatalf("deleted self: %d", r.code)
	}
	if r := e.do("POST", "/api/users/delete", map[string]string{"login": "bob"}, c, csrf); r.code != 200 {
		t.Fatalf("delete bob %d", r.code)
	}
	ops := []string{}
	for _, x := range e.srv.Audit.List("", 50) {
		ops = append(ops, x.User+" "+x.Op+" "+x.Target)
	}
	all := strings.Join(ops, "\n")
	for _, want := range []string{"admin POST /api/users/save bob", "admin POST /api/users/delete bob", "carol POST /api/invite", "bob POST /api/login", "admin POST /api/sessions/end bob"} {
		if !strings.Contains(all, want) {
			t.Fatalf("audit lacks %q:\n%s", want, all)
		}
	}
}

package web

import (
	"testing"

	"github.com/a3app/loft/server/panel/store"
)

// A viewer, with no right to the users area, manages its own sign-in.
func TestMeSelfService(t *testing.T) {
	e := newEnv(t)
	c, csrf := e.owner()
	if r := e.do("POST", "/api/users/save", map[string]any{"login": "vic", "role": store.RoleViewer, "password": "viewer password 1", "new": true}, c, csrf); r.code != 200 {
		t.Fatalf("add viewer %d %v", r.code, r.body)
	}
	login := func() resp {
		r := e.do("POST", "/api/login", map[string]string{"login": "vic", "password": "viewer password 1"}, nil, "")
		if r.code != 200 {
			t.Fatalf("login %d %v", r.code, r.body)
		}
		return r
	}
	a, b := login(), login()
	ac, acsrf := a.cookie, a.body["csrf"].(string)
	if r := e.do("POST", "/api/users/token", map[string]string{"login": "vic"}, ac, acsrf); r.code != 403 {
		t.Fatalf("viewer reached users: %d", r.code)
	}
	if r := e.do("GET", "/api/me", nil, ac, ""); r.code != 200 || len(r.body["sessions"].([]any)) != 2 {
		t.Fatalf("me %d %v", r.code, r.body)
	}
	tk := e.do("POST", "/api/me/token", map[string]any{}, ac, acsrf)
	if tk.code != 200 || len(tk.body["token"].(string)) < 10 {
		t.Fatalf("token %d %v", tk.code, tk.body)
	}
	if r := e.do("POST", "/api/me/password", map[string]string{"current": "wrong", "password": "viewer password 2"}, ac, acsrf); r.code != 400 {
		t.Fatalf("wrong current accepted: %d", r.code)
	}
	if r := e.do("POST", "/api/me/password", map[string]string{"current": "viewer password 1", "password": "viewer password 2"}, ac, acsrf); r.code != 200 {
		t.Fatalf("password %d %v", r.code, r.body)
	}
	if r := e.do("GET", "/api/me", nil, b.cookie, ""); r.code != 401 {
		t.Fatalf("the other session survived a password change: %d", r.code)
	}
	if r := e.do("GET", "/api/me", nil, ac, ""); r.code != 200 || len(r.body["sessions"].([]any)) != 1 || r.body["token"] == nil {
		t.Fatalf("me after %d %v", r.code, r.body)
	}
	if r := e.do("POST", "/api/me/sessions/end", map[string]any{}, ac, acsrf); r.code != 400 {
		t.Fatalf("empty end %d", r.code)
	}
	if r := e.do("POST", "/api/login", map[string]string{"login": "vic", "password": "viewer password 2"}, nil, ""); r.code != 200 {
		t.Fatalf("new password %d", r.code)
	}
}

func TestOverviewShape(t *testing.T) {
	e := newEnv(t)
	c, _ := e.owner()
	r := e.do("GET", "/api/overview", nil, c, "")
	if r.code != 200 {
		t.Fatalf("overview %d %v", r.code, r.body)
	}
	if _, ok := r.body["addrs"].([]any); !ok {
		t.Fatalf("addrs %T", r.body["addrs"])
	}
	if sv, ok := r.body["server"].(map[string]any); !ok || sv["arch"] == "" {
		t.Fatalf("server %v", r.body["server"])
	}
}

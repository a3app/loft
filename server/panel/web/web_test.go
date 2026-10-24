package web

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/a3app/loft/server/panel/admproto"
	"github.com/a3app/loft/server/panel/auth"
	"github.com/a3app/loft/server/panel/store"
)

type fakeAdm struct {
	by   *[]string
	ops  *[]string
	text *string
	host *string
	keys *map[string]admproto.IssuedKey
	mail *admproto.MailView
}

func (f fakeAdm) Call(_ context.Context, op string, body, out any) error {
	*f.ops = append(*f.ops, op)
	set := func(v any) { b, _ := json.Marshal(v); json.Unmarshal(b, out) }
	switch op {
	case admproto.OpNetworkGet:
		set(admproto.Network{Text: *f.text, SHA256: "s1"})
	case admproto.OpNetworkApply:
		in := body.(admproto.NetworkApply)
		if in.Expect != "s1" {
			return io.ErrUnexpectedEOF
		}
		*f.text = in.Text
		set(admproto.Network{Text: in.Text, SHA256: "s2"})
	case admproto.OpNodeStatus:
		set(admproto.NodeStatus{Active: "active"})
	case admproto.OpNodeInfo:
		set(admproto.NodeInfo{Advertise: "https://" + *f.host, Host: *f.host, GetPath: "/static/js", WSPath: "/api/v1/sync", RosterPath: "/static/v3/manifest.json", ControllerPub: "ab12"})
	case admproto.OpDomainSet:
		d := body.(admproto.DomainSet)
		*f.host = d.Name
		set(admproto.NodeInfo{Host: d.Name, Panel: firstNonEmpty(d.Panel, d.Name)})
	case admproto.OpPackages:
		set([]admproto.Package{{Key: "node", Group: "base", Base: true, Installed: true}, {Key: "wireguard", Group: "vpn"}})
	case admproto.OpMTProtoGet:
		set(admproto.MTProto{Installed: true, Host: *f.host, Port: 8448, Domain: "cloudflare.com", Secret: "ee00", Link: "tg://proxy?server=" + *f.host})
	case admproto.OpMTProtoSet:
		in := body.(admproto.MTProtoSet)
		port := in.Port
		if port == 0 {
			port = 8448
		}
		set(admproto.MTProto{Installed: true, Host: *f.host, Port: port, Domain: firstNonEmpty(in.Domain, "cloudflare.com"), Secret: "eenew"})
	case admproto.OpPackagesApply, admproto.OpUpdateApply:
		set(admproto.Job{ID: "j1", State: "running"})
	case admproto.OpJobGet:
		set(admproto.Job{ID: "j1", State: "running", Log: "▸ install wireguard\n"})
	case admproto.OpMemberKey:
		in := body.(admproto.MemberKey)
		k := admproto.IssuedKey{ID: in.ID, Key: "lt_" + strings.Repeat("a", 48), KeySHA256: strings.Repeat("b", 64)}
		if f.keys != nil {
			(*f.keys)[in.ID] = k
		}
		set(k)
	case admproto.OpMemberGet:
		in := body.(admproto.MemberKey)
		if f.keys != nil {
			if k, ok := (*f.keys)[in.ID]; ok {
				set(k)
				return nil
			}
		}
		return errors.New("no stored key for this member")
	case admproto.OpMail:
		if f.mail != nil {
			set(*f.mail)
		}
	}
	return nil
}

type env struct {
	t    *testing.T
	srv  *Server
	h    http.Handler
	ops  []string
	by   []string
	mail *admproto.MailView
}

func newEnv(t *testing.T) *env {
	st, _ := store.Open(t.TempDir())
	e := &env{t: t}
	text := "network main\nipif overlay 10.77.0.0/16\n"
	host := "panel.example.net"
	dir := t.TempDir()
	keys := map[string]admproto.IssuedKey{}
	mail := &admproto.MailView{Installed: true, Helo: "mail.example.net"}
	e.mail = mail
	e.srv = &Server{Store: st, Sessions: auth.NewSessions(), Limit: auth.NewLimiter(), SetupCode: "123456-abcdef",
		Hosts: []string{"panel.example.net"}, Settings: store.OpenSettings(dir), Keys: store.OpenMemberKeys(dir), ACME: &ACME{Dir: dir}, sleep: func(time.Duration) {}}
	e.srv.Adm = func(by string) Adm {
		e.by = append(e.by, by)
		return fakeAdm{by: &e.by, ops: &e.ops, text: &text, host: &host, keys: &keys, mail: mail}
	}
	h, err := e.srv.Handler()
	if err != nil {
		t.Fatal(err)
	}
	e.h = h
	return e
}

type resp struct {
	text   string
	code   int
	body   map[string]any
	cookie *http.Cookie
}

func (e *env) do(method, path string, body any, cookie *http.Cookie, csrf string, hdr ...string) resp {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	r := httptest.NewRequest(method, path, rd)
	r.Host = "panel.example.net"
	r.RemoteAddr = "198.51.100.20:5555"
	if cookie != nil {
		r.AddCookie(cookie)
	}
	if csrf != "" {
		r.Header.Set("X-CSRF-Token", csrf)
	}
	for i := 0; i+1 < len(hdr); i += 2 {
		if hdr[i] == "Host" {
			r.Host = hdr[i+1]
		} else if hdr[i] == "Remote" {
			r.RemoteAddr = hdr[i+1]
		} else {
			r.Header.Set(hdr[i], hdr[i+1])
		}
	}
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, r)
	out := resp{code: w.Code, body: map[string]any{}, text: w.Body.String()}
	json.Unmarshal(w.Body.Bytes(), &out.body)
	for _, c := range w.Result().Cookies() {
		if c.Name == cookieName {
			out.cookie = c
		}
	}
	return out
}

func (e *env) owner() (*http.Cookie, string) {
	r := e.do("POST", "/api/setup", map[string]string{"login": "admin", "password": "correct horse battery", "code": "123456-abcdef"}, nil, "")
	if r.code != 200 || r.cookie == nil {
		e.t.Fatalf("setup %d %v", r.code, r.body)
	}
	return r.cookie, r.body["csrf"].(string)
}

func TestSetupNeedsCode(t *testing.T) {
	e := newEnv(t)
	if r := e.do("GET", "/api/state", nil, nil, ""); r.body["setup"] != true {
		t.Fatalf("state %v", r.body)
	}
	r := e.do("POST", "/api/setup", map[string]string{"login": "admin", "password": "correct horse battery", "code": "wrong"}, nil, "")
	if r.code != 403 || !e.srv.Store.Empty() {
		t.Fatalf("wrong code: %d", r.code)
	}
	c, csrf := e.owner()
	if !c.HttpOnly || c.SameSite != http.SameSiteStrictMode || c.Path != "/" {
		t.Fatalf("cookie flags %+v", c)
	}
	if e.srv.SetupCode != "" {
		t.Fatal("setup code still valid after the owner exists")
	}
	if r := e.do("POST", "/api/setup", map[string]string{"login": "eve", "password": "correct horse battery", "code": "123456-abcdef"}, nil, ""); r.code != 409 {
		t.Fatalf("second setup %d", r.code)
	}
	if r := e.do("GET", "/api/state", nil, c, ""); r.body["setup"] != false || r.body["me"].(map[string]any)["csrf"] != csrf {
		t.Fatalf("state after %v", r.body)
	}
}

func TestLogin(t *testing.T) {
	e := newEnv(t)
	e.owner()
	if r := e.do("POST", "/api/login", map[string]string{"login": "admin", "password": "wrong password!"}, nil, ""); r.code != 401 || r.body["error"] != "wrong login or password" {
		t.Fatalf("bad pw %d %v", r.code, r.body)
	}
	if r := e.do("POST", "/api/login", map[string]string{"login": "nobody", "password": "x"}, nil, ""); r.code != 401 || r.body["error"] != "wrong login or password" {
		t.Fatal("unknown user must look the same")
	}
	r := e.do("POST", "/api/login", map[string]string{"login": "admin", "password": "correct horse battery"}, nil, "")
	if r.code != 200 || r.cookie == nil || r.body["role"] != "owner" {
		t.Fatalf("login %d %v", r.code, r.body)
	}
	if r.cookie.MaxAge != 0 {
		t.Fatalf("session cookie MaxAge %d", r.cookie.MaxAge)
	}
	rm := e.do("POST", "/api/login", map[string]any{"login": "admin", "password": "correct horse battery", "remember": true}, nil, "")
	if rm.code != 200 || rm.cookie == nil || rm.cookie.MaxAge != 7*24*3600 {
		t.Fatalf("remember cookie %d MaxAge %v", rm.code, rm.cookie)
	}
	if r := e.do("POST", "/api/login", map[string]any{"login": "admin", "password": "x", "extra": 1}, nil, ""); r.code != 400 {
		t.Fatalf("unknown field %d", r.code)
	}
}

func TestLoginBlock(t *testing.T) {
	e := newEnv(t)
	e.owner()
	for i := 0; i < 5; i++ {
		e.do("POST", "/api/login", map[string]string{"login": "admin", "password": "nope nope nope"}, nil, "")
	}
	if r := e.do("POST", "/api/login", map[string]string{"login": "admin", "password": "correct horse battery"}, nil, ""); r.code != 429 {
		t.Fatalf("not blocked: %d", r.code)
	}
	// Behind nginx the real client is X-Forwarded-For; another client is not blocked.
	if r := e.do("POST", "/api/login", map[string]string{"login": "admin", "password": "correct horse battery"}, nil, "", "Remote", "127.0.0.1:4000", "X-Forwarded-For", "203.0.113.9"); r.code != 200 {
		t.Fatalf("other client: %d", r.code)
	}
	// Behind loft-node the proxy only sees 127.0.0.1: no block (it would lock
	// everyone out), the real owner still gets in after others' failures.
	for i := 0; i < 8; i++ {
		e.do("POST", "/api/login", map[string]string{"login": "admin", "password": "nope nope nope"}, nil, "", "Remote", "127.0.0.1:4000", "X-Forwarded-For", "127.0.0.1")
	}
	if r := e.do("POST", "/api/login", map[string]string{"login": "admin", "password": "correct horse battery"}, nil, "", "Remote", "127.0.0.1:4000", "X-Forwarded-For", "127.0.0.1"); r.code != 200 {
		t.Fatalf("unknown-address login locked out: %d", r.code)
	}
	// A direct client cannot spoof X-Forwarded-For.
	if r := e.do("POST", "/api/login", map[string]string{"login": "admin", "password": "correct horse battery"}, nil, "", "X-Forwarded-For", "203.0.113.10"); r.code != 429 {
		t.Fatalf("spoofed XFF: %d", r.code)
	}
}

func TestCSRFAndRoles(t *testing.T) {
	e := newEnv(t)
	c, csrf := e.owner()
	if r := e.do("GET", "/api/network", nil, nil, ""); r.code != 401 {
		t.Fatalf("anon %d", r.code)
	}
	if r := e.do("POST", "/api/network/apply", map[string]string{"text": "x", "expect": "s1"}, c, ""); r.code != 403 {
		t.Fatalf("no csrf %d", r.code)
	}
	if r := e.do("POST", "/api/network/apply", map[string]string{"text": "x", "expect": "s1"}, c, "bad"); r.code != 403 {
		t.Fatalf("bad csrf %d", r.code)
	}
	h, _ := auth.Hash("viewer password!")
	e.srv.Store.Add(store.User{Login: "viewer", Role: store.RoleViewer, Hash: h})
	v := e.do("POST", "/api/login", map[string]string{"login": "viewer", "password": "viewer password!"}, nil, "")
	if r := e.do("GET", "/api/network", nil, v.cookie, ""); r.code != 200 {
		t.Fatalf("viewer read %d", r.code)
	}
	if r := e.do("POST", "/api/network/apply", map[string]string{"text": "x", "expect": "s1"}, v.cookie, v.body["csrf"].(string)); r.code != 403 || r.body["error"] != "your role cannot do this" {
		t.Fatalf("viewer apply %d %v", r.code, r.body)
	}
	if r := e.do("POST", "/api/logout", map[string]string{}, c, csrf); r.code != 200 {
		t.Fatalf("logout %d", r.code)
	}
	if r := e.do("GET", "/api/network", nil, c, ""); r.code != 401 {
		t.Fatalf("after logout %d", r.code)
	}
}

func TestNetworkAPI(t *testing.T) {
	e := newEnv(t)
	c, csrf := e.owner()
	r := e.do("GET", "/api/network", nil, c, "")
	if r.code != 200 || r.body["sha256"] != "s1" {
		t.Fatalf("get %d %v", r.code, r.body)
	}
	r = e.do("POST", "/api/network/check", map[string]string{"text": "network x\nipif overlay 10.0.0.0/24\n  mtu zero\n"}, c, csrf)
	if r.body["ok"] != false || r.body["error"] != "line 3: bad mtu" {
		t.Fatalf("check %v", r.body)
	}
	next := "network main\nipif overlay 10.77.0.0/16\n  peers mesh\n"
	j := e.job(e.do("POST", "/api/network/apply", map[string]string{"text": next, "expect": "s1"}, c, csrf), c)
	if j["state"] != "done" || j["result"].(map[string]any)["sha256"] != "s2" {
		t.Fatalf("apply %v", j)
	}
	if e.by[len(e.by)-1] != "admin" {
		t.Fatalf("daemon not told who: %v", e.by)
	}
	j = e.job(e.do("POST", "/api/network/apply", map[string]string{"text": next, "expect": "old"}, c, csrf), c)
	if j["state"] != "error" || j["error"] == "" {
		t.Fatalf("daemon error must end the job with error: %v", j)
	}
	if r := e.do("GET", "/api/node", nil, c, ""); r.body["active"] != "active" {
		t.Fatalf("node %v", r.body)
	}
}

func TestHostAndHeaders(t *testing.T) {
	e := newEnv(t)
	if r := e.do("GET", "/api/state", nil, nil, "", "Host", "evil.example.com"); r.code != http.StatusMisdirectedRequest {
		t.Fatalf("foreign host %d", r.code)
	}
	if r := e.do("GET", "/api/state", nil, nil, "", "Host", "127.0.0.1:8091"); r.code != 200 {
		t.Fatalf("loopback %d", r.code)
	}
	req := httptest.NewRequest("GET", "/", nil)
	req.Host = "panel.example.net"
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, req)
	if w.Code != 200 || !strings.Contains(w.Body.String(), `<script src="live.js"></script>`) {
		t.Fatalf("index: %d", w.Code)
	}
	for _, h := range []string{"Content-Security-Policy", "X-Frame-Options", "X-Content-Type-Options"} {
		if w.Header().Get(h) == "" {
			t.Fatalf("missing %s", h)
		}
	}
	if r := e.do("GET", "/api/nope", nil, nil, ""); r.code != 404 {
		t.Fatalf("unknown api %d", r.code)
	}
}

func TestSecureCookieBehindProxy(t *testing.T) {
	e := newEnv(t)
	r := e.do("POST", "/api/setup", map[string]string{"login": "admin", "password": "correct horse battery", "code": "123456-abcdef"}, nil, "",
		"Remote", "127.0.0.1:4000", "X-Forwarded-Proto", "https")
	if r.cookie == nil || !r.cookie.Secure {
		t.Fatalf("cookie must be Secure behind https proxy: %+v", r.cookie)
	}
}

// job waits for a 202 job reply to finish and returns the job.
func (e *env) job(r resp, c *http.Cookie) map[string]any {
	e.t.Helper()
	if r.code != 202 || r.body["job"] == nil {
		e.t.Fatalf("want a job, got %d %v", r.code, r.body)
	}
	for i := 0; i < 200; i++ {
		j := e.do("GET", "/api/jobs/"+r.body["job"].(string), nil, c, "")
		if j.code != 200 {
			e.t.Fatalf("job %d %v", j.code, j.body)
		}
		if j.body["state"] != "running" {
			return j.body
		}
		time.Sleep(10 * time.Millisecond)
	}
	e.t.Fatal("job did not finish")
	return nil
}

func TestJobsArePrivate(t *testing.T) {
	e := newEnv(t)
	c, csrf := e.owner()
	r := e.do("POST", "/api/node/restart", map[string]string{}, c, csrf)
	h, _ := auth.Hash("viewer password!")
	e.srv.Store.Add(store.User{Login: "viewer", Role: store.RoleViewer, Hash: h})
	v := e.do("POST", "/api/login", map[string]string{"login": "viewer", "password": "viewer password!"}, nil, "")
	if g := e.do("GET", "/api/jobs/"+r.body["job"].(string), nil, v.cookie, ""); g.code != 404 {
		t.Fatalf("another user sees the job: %d", g.code)
	}
	e.job(r, c)
}

func TestWizardFlow(t *testing.T) {
	e := newEnv(t)
	c, csrf := e.owner()
	if st := e.do("GET", "/api/state", nil, c, ""); st.body["wizard"] != true {
		t.Fatalf("wizard must be pending: %v", st.body)
	}
	w := e.do("GET", "/api/wizard", nil, c, "")
	if w.code != 200 || w.body["domain"] != "panel.example.net" {
		t.Fatalf("wizard %d %v", w.code, w.body)
	}
	if r := e.do("POST", "/api/domain/check", map[string]string{"name": "not a name"}, c, csrf); r.body["error"] == nil {
		t.Fatalf("bad name: %v", r.body)
	}
	j := e.job(e.do("POST", "/api/domain", map[string]string{"name": "vpn.example.org"}, c, csrf), c)
	if j["state"] != "done" {
		t.Fatalf("domain %v", j)
	}
	if st, _ := e.srv.Settings.Get(); st.Domain != "vpn.example.org" || !e.srv.hostOK("vpn.example.org") {
		t.Fatalf("domain not taken: %+v", st)
	}
	j = e.job(e.do("POST", "/api/domain", map[string]string{"name": "api.example.org", "panel": "us.example.org"}, c, csrf), c)
	if st, _ := e.srv.Settings.Get(); j["state"] != "done" || st.Domain != "api.example.org" || st.PanelDomain != "us.example.org" || !e.srv.hostOK("us.example.org") {
		t.Fatalf("separate names not taken: %v %+v", j, st)
	}
	if w := e.do("GET", "/api/wizard", nil, c, ""); w.body["panel_domain"] != "us.example.org" {
		t.Fatalf("wizard panel %v", w.body)
	}
	e.job(e.do("POST", "/api/domain", map[string]string{"name": "vpn.example.org"}, c, csrf), c)
	if st, _ := e.srv.Settings.Get(); st.PanelDomain != "" {
		t.Fatalf("one name again: %+v", st)
	}
	j = e.job(e.do("POST", "/api/wizard/member", map[string]string{"id": "alice-phone", "name": "Alice"}, c, csrf), c)
	res, _ := j["result"].(map[string]any)
	if j["state"] != "done" || res["link"] != "loft://lt_"+strings.Repeat("a", 48)+"@vpn.example.org/?get=/static/js&fp=chrome#Alice" {
		t.Fatalf("member %v", j)
	}
	if !strings.Contains(res["file"].(string), "controller_pub: ab12") || !strings.Contains(res["file"].(string), "type: get") || strings.Contains(res["file"].(string), "type: ws") {
		t.Fatalf("file %s", res["file"])
	}
	j = e.job(e.do("POST", "/api/wizard/member", map[string]string{"id": "alice-phone"}, c, csrf), c)
	if j["state"] != "error" || !strings.Contains(j["error"].(string), "already exists") {
		t.Fatalf("duplicate member %v", j)
	}
	if r := e.do("POST", "/api/wizard/done", map[string]string{}, c, csrf); r.code != 200 {
		t.Fatalf("done %d", r.code)
	}
	if st := e.do("GET", "/api/state", nil, c, ""); st.body["wizard"] != false {
		t.Fatal("wizard still pending")
	}
}

func TestACMEChallenge(t *testing.T) {
	e := newEnv(t)
	e.srv.ACME.put("tok123", "tok123.thumb")
	req := httptest.NewRequest("GET", "/.well-known/acme-challenge/tok123", nil)
	req.Host = "panel.example.net"
	w := httptest.NewRecorder()
	e.h.ServeHTTP(w, req)
	if w.Code != 200 || w.Body.String() != "tok123.thumb" {
		t.Fatalf("challenge %d %q", w.Code, w.Body.String())
	}
	req = httptest.NewRequest("GET", "/.well-known/acme-challenge/other", nil)
	req.Host = "panel.example.net"
	w = httptest.NewRecorder()
	e.h.ServeHTTP(w, req)
	if w.Code != 404 {
		t.Fatalf("unknown token %d", w.Code)
	}
}

func (r resp) raw() string { return r.text }

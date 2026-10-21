package web

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/a3app/loft/server/panel/admproto"
	"github.com/a3app/loft/server/panel/store"
)

func TestSettingsAPI(t *testing.T) {
	e := newEnv(t)
	c, csrf := e.owner()
	r := e.do("GET", "/api/settings", nil, c, "")
	if r.code != 200 || r.body["session_idle"] != "30m" || r.body["lang"] != "ru" {
		t.Fatalf("get %d %v", r.code, r.body)
	}
	if r := e.do("POST", "/api/settings/panel", map[string]string{"session_idle": "1y", "session_max": "7d", "lang": "ru"}, c, csrf); r.code != 400 {
		t.Fatalf("bad idle: %d", r.code)
	}
	if r := e.do("POST", "/api/settings/panel", map[string]string{"session_idle": "off", "session_max": "30d", "lang": "en"}, c, csrf); r.code != 200 {
		t.Fatalf("panel %d %v", r.code, r.body)
	}
	if idle, max := e.srv.Sessions.Limits(); max != 30*24*time.Hour || idle != max {
		t.Fatalf("limits %v %v", idle, max)
	}
	if r := e.do("GET", "/api/state", nil, nil, ""); r.body["lang"] != "en" {
		t.Fatalf("state lang %v", r.body)
	}
	if r := e.do("POST", "/api/settings/panel", map[string]string{"session_idle": "30m", "session_max": "7d", "lang": "zh"}, c, csrf); r.code != 200 || r.body["lang"] != "zh" {
		t.Fatalf("zh %d %v", r.code, r.body)
	}
	if r := e.do("POST", "/api/settings/panel", map[string]string{"session_idle": "30m", "session_max": "7d", "lang": "de"}, c, csrf); r.code != 400 {
		t.Fatalf("bad lang: %d", r.code)
	}

	n := map[string]any{"to": "alice@example.net", "from": "panel@example.net", "host": "smtp.example.net", "port": 587, "security": "starttls", "user": "u", "password": "secret-1", "events": []string{"cert", "node"}}
	if r := e.do("POST", "/api/settings/notify", n, c, csrf); r.code != 200 || r.body["notify"].(map[string]any)["has_password"] != true {
		t.Fatalf("notify %d %v", r.code, r.body)
	}
	if strings.Contains(e.do("GET", "/api/settings", nil, c, "").raw(), "secret-1") {
		t.Fatal("password sent to the page")
	}
	n["password"] = "" // keeps the stored one
	e.do("POST", "/api/settings/notify", n, c, csrf)
	st, _ := e.srv.Settings.Get()
	if st.Notify.Password != "secret-1" || strings.Join(st.Notify.Off, ",") != "update,backup" {
		t.Fatalf("stored %+v", st.Notify)
	}
	n["to"] = "not an address"
	if r := e.do("POST", "/api/settings/notify", n, c, csrf); r.code != 400 {
		t.Fatalf("bad address: %d", r.code)
	}
	var sent []string
	e.srv.SendMail = func(_ store.Notify, subject, body string) error { sent = append(sent, subject); return nil }
	if r := e.do("POST", "/api/settings/notify/test", map[string]string{}, c, csrf); r.code != 200 || len(sent) != 1 {
		t.Fatalf("test %d %v", r.code, sent)
	}
}

func TestWatchCertOnce(t *testing.T) {
	e := newEnv(t)
	var sent []string
	e.srv.SendMail = func(_ store.Notify, subject, body string) error { sent = append(sent, subject); return nil }
	until := time.Now().Add(5 * 24 * time.Hour).UTC()
	e.srv.Settings.Update(func(s *store.Settings) {
		s.Domain, s.CertUntil = "api.example.net", until
		s.Notify = store.Notify{To: "a@example.net", From: "p@example.net", Host: "smtp.example.net", Port: 587}
	})
	for i := 0; i < 2; i++ {
		st, _ := e.srv.Settings.Get()
		e.srv.watchCerts(context.Background(), st)
	}
	if len(sent) != 1 || !strings.Contains(sent[0], "api.example.net") {
		t.Fatalf("sent %v", sent)
	}
	// The node: one letter after two failed checks, none while it stays down.
	st, _ := e.srv.Settings.Get()
	down := 0
	e.srv.Adm = func(string) Adm { return nodeDown{} }
	for i := 0; i < 4; i++ {
		down = e.srv.watchNode(context.Background(), st, down)
		st, _ = e.srv.Settings.Get()
	}
	if len(sent) != 2 || !strings.Contains(sent[1], "failed") {
		t.Fatalf("node letters %v", sent)
	}
}

type nodeDown struct{}

func (nodeDown) Call(_ context.Context, op string, _, out any) error {
	if op == admproto.OpNodeStatus {
		*(out.(*admproto.NodeStatus)) = admproto.NodeStatus{Active: "failed"}
	}
	return nil
}

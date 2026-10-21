package adm

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/a3app/loft/server/panel/admproto"
)

const testHash = "{ARGON2ID}$argon2id$v=19$m=19456,t=2,p=1$c2FsdHNhbHRzYWx0c2FsdA$c2VjcmV0c2VjcmV0c2VjcmV0c2VjcmV0c2VjcmV0MTI"

func sampleMail() *mailStore {
	return &mailStore{
		Domains: []admproto.MailDomain{{Name: "example.net", Enabled: true, DKIM: true}, {Name: "example.org", Enabled: false}},
		Boxes: []admproto.MailBox{
			{Address: "admin@example.net", Hash: testHash, QuotaMB: 3072, Enabled: true},
			{Address: "bob@example.net", Hash: testHash, Forwards: []string{"bob@elsewhere.example"}, Enabled: true},
			{Address: "noreply@example.net", Hash: testHash, SendOnly: true, Enabled: true},
			{Address: "old@example.net", Hash: testHash, Enabled: false},
			{Address: "x@example.org", Hash: testHash, Enabled: true},
		},
		Aliases: []admproto.MailAlias{{From: "team@example.net", To: []string{"admin@example.net", "bob@example.net"}}},
	}
}

func TestMailMaps(t *testing.T) {
	s := sampleMail()
	if got := renderVDomains(s); !strings.Contains(got, "example.net\tOK") || strings.Contains(got, "example.org") {
		t.Fatalf("vdomains: a disabled domain takes no mail:\n%s", got)
	}
	mb := renderVMailbox(s)
	if !strings.Contains(mb, "admin@example.net\texample.net/admin/") || strings.Contains(mb, "old@") || strings.Contains(mb, "x@example.org") {
		t.Fatalf("vmailbox: only enabled boxes of enabled domains:\n%s", mb)
	}
	va := renderVAlias(s)
	if !strings.Contains(va, "bob@example.net\tbob@example.net, bob@elsewhere.example") {
		t.Fatalf("a forward keeps the box among the recipients:\n%s", va)
	}
	if !strings.Contains(va, "team@example.net\tadmin@example.net, bob@example.net") {
		t.Fatalf("alias missing:\n%s", va)
	}
	if strings.Contains(va, "@example.net\t") && strings.Contains(va, "\n@example.net") {
		t.Fatalf("no catch-all asked for:\n%s", va)
	}
	s.Domains[0].CatchAll = []string{"admin@example.net"}
	va = renderVAlias(s)
	if !strings.Contains(va, "\n@example.net\tadmin@example.net") || !strings.Contains(va, "noreply@example.net\tnoreply@example.net") {
		t.Fatalf("with a catch-all every box maps to itself so the exact match wins:\n%s", va)
	}
	vl := renderVLogin(s)
	if !strings.Contains(vl, "team@example.net\tadmin@example.net,bob@example.net") || !strings.Contains(vl, "admin@example.net\tadmin@example.net") {
		t.Fatalf("vlogin:\n%s", vl)
	}
	if nr := renderNoRecv(s); !strings.Contains(nr, "noreply@example.net\tREJECT") || strings.Contains(nr, "admin@") {
		t.Fatalf("norecv:\n%s", nr)
	}
}

func TestMailUsersLine(t *testing.T) {
	s := sampleMail()
	for _, v24 := range []bool{false, true} {
		u := renderUsers(s, mailEnv{Dovecot24: v24})
		var line string
		for _, l := range strings.Split(u, "\n") {
			if strings.HasPrefix(l, "admin@") {
				line = l
			}
		}
		// name:pass:uid:gid:gecos:home:shell:extra — the hash has no colons.
		if strings.Count(line, ":") != 7 && strings.Count(strings.SplitN(line, "::::::", 2)[0], ":") != 1 {
			t.Fatalf("passwd-file line has the wrong fields: %q", line)
		}
		want := "::::::userdb_quota_rule=*:storage=3072M"
		if v24 {
			want = "::::::userdb_quota_storage_size=3072M"
		}
		if !strings.HasSuffix(line, want) {
			t.Fatalf("quota (2.4=%v): %q", v24, line)
		}
		if strings.Contains(u, "old@") || strings.Contains(u, "x@example.org") {
			t.Fatalf("a disabled box cannot log in:\n%s", u)
		}
	}
}

func TestMailDovecotTLS(t *testing.T) {
	s := sampleMail()
	e := mailEnv{UID: 120, GID: 125, Certs: map[string]bool{"mail.example.net": true}}
	c := renderDovecot(s, e)
	for _, w := range []string{"first_valid_uid = 120", "local_name mail.example.net {", "ssl_cert = </etc/loft-panel/mail-tls/mail.example.net.crt", "mail_location = maildir:"} {
		if !strings.Contains(c, w) {
			t.Fatalf("2.3 conf lacks %q:\n%s", w, c)
		}
	}
	e.Dovecot24 = true
	c = renderDovecot(s, e)
	for _, w := range []string{"mail_driver = maildir", "passdb loft_boxes {", "ssl_server_cert_file = /etc/loft-panel/mail-tls/mail.example.net.crt"} {
		if !strings.Contains(c, w) {
			t.Fatalf("2.4 conf lacks %q:\n%s", w, c)
		}
	}
	p := mailParams(s, e)
	got := map[string]string{}
	for _, kv := range p {
		got[kv[0]] = kv[1]
	}
	if got["myhostname"] != "mail.example.net" || !strings.Contains(got["smtpd_tls_chain_files"], "mail.example.net.key") {
		t.Fatalf("params: %v", got)
	}
	if !strings.Contains(renderSNI(s, e), "mail.example.net\t/etc/loft-panel/mail-tls/mail.example.net.key /etc/loft-panel/mail-tls/mail.example.net.crt") {
		t.Fatal("sni map")
	}
}

func TestDisableSystemAuth(t *testing.T) {
	in := "#!include auth-deny.conf.ext\n!include auth-system.conf.ext\n#!include auth-sql.conf.ext\n"
	out, changed := disableSystemAuth(in)
	if !changed || strings.Contains(out, "\n!include auth-system") {
		t.Fatalf("not disabled:\n%s", out)
	}
	if _, again := disableSystemAuth(out); again {
		t.Fatal("second run changed it again")
	}
}

type fakeMailHost struct {
	scripts []string
	failOn  string // a script containing this fails
}

func (f *fakeMailHost) exec(_ context.Context, name string, args ...string) (string, error) {
	switch {
	case name == "dovecot":
		return "2.3.21 (47349e2482)\n", nil
	case name == "sh" && strings.Contains(args[1], "useradd"):
		return "118\n124\n", nil
	case name == "sh":
		f.scripts = append(f.scripts, args[1])
		if f.failOn != "" && strings.Contains(args[1], f.failOn) {
			return "postfix/postfix-script: fatal: bad config", errors.New("exit status 1")
		}
	}
	return "", nil
}

func mailOps(t *testing.T) (*Ops, *fakeMailHost) {
	o, _ := pkgOps(t)
	o.PanelEtc = t.TempDir()
	f := &fakeMailHost{}
	o.Exec = f.exec
	o.NginxTest = func() error { return nil }
	os.MkdirAll(filepath.Join(o.Root, "usr/sbin"), 0o755)
	os.WriteFile(filepath.Join(o.Root, "usr/sbin/postconf"), nil, 0o755)
	os.MkdirAll(filepath.Join(o.Root, "etc/dovecot/conf.d"), 0o755)
	os.WriteFile(filepath.Join(o.Root, mailDovecotAuth), []byte("!include auth-system.conf.ext\n"), 0o644)
	return o, f
}

func TestMailSaveFlow(t *testing.T) {
	o, f := mailOps(t)
	ctx := context.Background()
	if _, err := o.MailBoxSave(ctx, admproto.MailBoxSave{MailBox: admproto.MailBox{Address: "a@example.net", Hash: testHash, Enabled: true}}); err == nil {
		t.Fatal("a box needs its domain first")
	}
	v, err := o.MailDomainSave(ctx, admproto.MailDomainSave{MailDomain: admproto.MailDomain{Name: "Example.NET", Enabled: true, DKIM: true}})
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Domains) != 1 || v.Domains[0].Name != "example.net" || !strings.HasPrefix(v.Domains[0].DKIMRecord, "v=DKIM1; k=rsa; p=") {
		t.Fatalf("domain: %+v", v.Domains)
	}
	rec := v.Domains[0].DKIMRecord
	if _, err := o.MailBoxSave(ctx, admproto.MailBoxSave{MailBox: admproto.MailBox{Address: "a@example.net", Enabled: true}}); err == nil {
		t.Fatal("a new box needs a password")
	}
	if _, err := o.MailBoxSave(ctx, admproto.MailBoxSave{MailBox: admproto.MailBox{Address: "a@example.net", Hash: "{PLAIN}x", Enabled: true}}); err == nil {
		t.Fatal("only argon2id hashes are taken")
	}
	v, err = o.MailBoxSave(ctx, admproto.MailBoxSave{MailBox: admproto.MailBox{Address: "A@example.net", Hash: testHash, QuotaMB: 1024, Enabled: true}})
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Boxes) != 1 || v.Boxes[0].Hash != "" {
		t.Fatalf("the hash never leaves the helper: %+v", v.Boxes)
	}
	// A change without a hash keeps the password.
	if _, err := o.MailBoxSave(ctx, admproto.MailBoxSave{MailBox: admproto.MailBox{Address: "a@example.net", QuotaMB: 2048, Enabled: true}}); err != nil {
		t.Fatal(err)
	}
	users, _ := os.ReadFile(filepath.Join(o.Root, mailDovecotUsers))
	if !strings.Contains(string(users), "a@example.net:"+testHash+"::::::userdb_quota_rule=*:storage=2048M") {
		t.Fatalf("users file:\n%s", users)
	}
	if _, err := o.MailAliasSave(ctx, admproto.MailAliasSave{MailAlias: admproto.MailAlias{From: "a@example.net", To: []string{"b@x.example"}}}); err == nil {
		t.Fatal("an alias cannot shadow a box")
	}
	if _, err := o.MailAliasSave(ctx, admproto.MailAliasSave{MailAlias: admproto.MailAlias{From: "info@example.net", To: []string{"a@example.net"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := o.MailDomainSave(ctx, admproto.MailDomainSave{MailDomain: admproto.MailDomain{Name: "example.net", Enabled: true, DKIM: true, CatchAll: []string{"a@example.net"}}}); err != nil {
		t.Fatal(err)
	}
	v, _ = o.Mail(ctx)
	if v.Domains[0].DKIMRecord != rec {
		t.Fatal("the DKIM key was replaced on a save")
	}
	auth, _ := os.ReadFile(filepath.Join(o.Root, mailDovecotAuth))
	if strings.Contains(string(auth), "\n!include auth-system") || strings.HasPrefix(string(auth), "!include") {
		t.Fatalf("system accounts still log in to mail:\n%s", auth)
	}
	last := f.scripts[len(f.scripts)-1]
	for _, w := range []string{"postfix check", "doveconf -n", "postconf -e 'virtual_transport=lmtp:unix:private/dovecot-lmtp'", "postconf -M -e 'submission/inet=", "postmap hash:" + mailPostfixDir + "/valias"} {
		if !strings.Contains(strings.Join(f.scripts, "\n"), w) {
			t.Fatalf("apply lacks %q:\n%s", w, last)
		}
	}
	// Removing the domain takes its boxes and aliases with it.
	v, err = o.MailDomainSave(ctx, admproto.MailDomainSave{MailDomain: admproto.MailDomain{Name: "example.net"}, Delete: true})
	if err != nil || len(v.Boxes) != 0 || len(v.Aliases) != 0 {
		t.Fatalf("delete: %v %+v", err, v)
	}
}

func TestMailRejectsInjection(t *testing.T) {
	o, _ := mailOps(t)
	ctx := context.Background()
	o.MailDomainSave(ctx, admproto.MailDomainSave{MailDomain: admproto.MailDomain{Name: "example.net", Enabled: true}})
	for _, a := range []string{"a\n@example.net", "a b@example.net", "a:b@example.net", ".a@example.net", "a..b@example.net"} {
		if _, err := o.MailBoxSave(ctx, admproto.MailBoxSave{MailBox: admproto.MailBox{Address: a, Hash: testHash, Enabled: true}}); err == nil {
			t.Fatalf("%q accepted", a)
		}
	}
	if _, err := o.MailDomainSave(ctx, admproto.MailDomainSave{MailDomain: admproto.MailDomain{Name: "example.net\tOK\nevil.example", Enabled: true}}); err == nil {
		t.Fatal("a domain with a newline accepted")
	}
}

func TestMailFailedCheckRestores(t *testing.T) {
	o, f := mailOps(t)
	ctx := context.Background()
	if _, err := o.MailDomainSave(ctx, admproto.MailDomainSave{MailDomain: admproto.MailDomain{Name: "example.net", Enabled: true}}); err != nil {
		t.Fatal(err)
	}
	before, _ := os.ReadFile(filepath.Join(o.Root, mailPostfixDir, "vdomains"))
	f.failOn = "postfix check"
	_, err := o.MailDomainSave(ctx, admproto.MailDomainSave{MailDomain: admproto.MailDomain{Name: "example.org", Enabled: true}})
	if err == nil || !strings.Contains(err.Error(), "did not pass its check") {
		t.Fatalf("want the check failure, got %v", err)
	}
	after, _ := os.ReadFile(filepath.Join(o.Root, mailPostfixDir, "vdomains"))
	if string(after) != string(before) {
		t.Fatalf("the old map was not put back:\n%s", after)
	}
	// The intent is kept with the error, so the screen shows it and the
	// next save retries.
	s, _ := o.readMail()
	if len(s.Domains) != 2 || s.Error == "" {
		t.Fatalf("intent: %+v", s)
	}
	f.failOn = ""
	if _, err := o.MailApply(ctx); err != nil {
		t.Fatal(err)
	}
	if s, _ := o.readMail(); s.Error != "" {
		t.Fatal("the error stays after a good apply")
	}
}

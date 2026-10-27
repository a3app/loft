package web

import (
	"net"
	"regexp"
	"strings"
	"testing"

	"github.com/a3app/loft/server/panel/admproto"
)

// The helper takes only this shape (adm.mailHashRe); libsodium in Dovecot
// verifies only p=1.
var helperHash = regexp.MustCompile(`^\{ARGON2ID\}\$argon2id\$v=19\$m=\d{4,7},t=\d{1,2},p=1\$[A-Za-z0-9+/]{16,}\$[A-Za-z0-9+/]{32,}$`)

func TestMailHash(t *testing.T) {
	h, err := mailHash("correct horse battery")
	if err != nil || !helperHash.MatchString(h) {
		t.Fatalf("%q %v", h, err)
	}
	if _, err := mailHash("short"); err == nil {
		t.Fatal("short password accepted")
	}
	if _, err := mailHash("long enough:password"); err == nil {
		t.Fatal("a colon would break the passwd-file")
	}
}

func TestMailNeeds(t *testing.T) {
	d := admproto.MailDomain{Name: "example.net", DKIM: true, DKIMRecord: "v=DKIM1; k=rsa; p=AAAA"}
	recs := mailNeeds(d, []net.IP{net.ParseIP("203.0.113.10"), net.ParseIP("2001:db8::10")})
	var keys []string
	for _, r := range recs {
		keys = append(keys, r.Key+":"+r.Type+":"+r.Name)
	}
	want := "mx:MX:example.net a:A:mail.example.net a:AAAA:mail.example.net spf:TXT:example.net helo:TXT:mail.example.net dkim:TXT:mail._domainkey.example.net dmarc:TXT:_dmarc.example.net"
	if got := strings.Join(keys, " "); got != want {
		t.Fatalf("got  %s\nwant %s", got, want)
	}
}

func TestMailAuto(t *testing.T) {
	if mailAuto(false, mailRec{State: "missing", Key: "mx"}) {
		t.Fatal("no API")
	}
	if !mailAuto(true, mailRec{State: "missing", Key: "spf"}) {
		t.Fatal("missing SPF")
	}
	if !mailAuto(true, mailRec{State: "differs", Key: "mx"}) {
		t.Fatal("foreign MX")
	}
	if mailAuto(true, mailRec{State: "differs", Key: "spf"}) {
		t.Fatal("own SPF")
	}
	if mailAuto(true, mailRec{State: "ok", Key: "mx"}) {
		t.Fatal("matching MX")
	}
}

func TestMailNeedWrite(t *testing.T) {
	mx := mailRec{Key: "mx", Name: "example.net", Type: "MX", Value: "mail.example.net", Prio: 10}
	spf := mailRec{Key: "spf", Name: "example.net", Type: "TXT", Value: "v=spf1 mx -all"}
	have := []Record{
		{Type: "MX", Name: "@", Value: "eforward.example.com", Priority: 10},
		{Type: "TXT", Name: "@", Value: "v=spf1 include:somewhere -all"},
	}
	if !mailNeedWrite(have, mx, "example.net") {
		t.Fatal("foreign MX")
	}
	if mailNeedWrite(have, spf, "example.net") {
		t.Fatal("own SPF")
	}
	have[0].Value, have[0].Priority = "mail.example.net", 10
	if mailNeedWrite(have, mx, "example.net") {
		t.Fatal("wanted MX already there")
	}
}

func TestMailDNSCreateReplacesForeignMX(t *testing.T) {
	e := newEnv(t)
	e.srv.SetDomainsFile(t.TempDir())
	fd := &fakeDNS{recs: map[string][]Record{"example.net": {
		{ID: "1", Type: "MX", Name: "@", Value: "eforward.example.com", Priority: 10},
		{ID: "2", Type: "MX", Name: "@", Value: "eforward2.example.com", Priority: 20},
		{ID: "3", Type: "TXT", Name: "@", Value: "v=spf1 include:somewhere -all"},
		{ID: "4", Type: "A", Name: "www", Value: "203.0.113.10"},
	}}}
	e.srv.dom.provider = func(dnsProv) DNSProvider { return fd }
	c, csrf := e.owner()
	p := e.do("POST", "/api/domains/provider", map[string]string{"kind": "cloudflare", "token": "tok-1234567890"}, c, csrf)
	if p.code != 200 {
		t.Fatalf("provider %d %v", p.code, p.body)
	}
	if r := e.do("POST", "/api/domains/zone", map[string]any{"name": "example.net", "provider": p.body["id"]}, c, csrf); r.code != 200 {
		t.Fatalf("zone %d %v", r.code, r.body)
	}
	e.mail.Domains = []admproto.MailDomain{{Name: "example.net", Enabled: true, DKIM: true, DKIMRecord: "v=DKIM1; k=rsa; p=AAAA"}}
	r := e.do("POST", "/api/mail/dns", map[string]string{"domain": "example.net"}, c, csrf)
	if r.code != 200 {
		t.Fatalf("create %d %v", r.code, r.body)
	}
	if n, _ := r.body["created"].(float64); n < 1 {
		t.Fatalf("created %v", r.body)
	}
	var mx []Record
	var spf string
	var dkim, dmarc, helo bool
	for _, rec := range fd.recs["example.net"] {
		switch {
		case rec.Type == "MX" && rec.Name == "@":
			mx = append(mx, rec)
		case rec.Type == "TXT" && rec.Name == "@" && strings.HasPrefix(rec.Value, "v=spf1"):
			spf = rec.Value
		case rec.Type == "TXT" && rec.Name == "mail._domainkey":
			dkim = rec.Value == "v=DKIM1; k=rsa; p=AAAA"
		case rec.Type == "TXT" && rec.Name == "_dmarc":
			dmarc = true
		case rec.Type == "TXT" && rec.Name == "mail":
			helo = rec.Value == "v=spf1 a -all"
		}
	}
	if len(mx) != 1 || mx[0].Value != "mail.example.net" || mx[0].Priority != 10 {
		t.Fatalf("MX %v", mx)
	}
	if spf != "v=spf1 include:somewhere -all" {
		t.Fatalf("SPF overwritten: %q", spf)
	}
	if !dkim || !dmarc || !helo {
		t.Fatalf("dkim=%v dmarc=%v helo=%v recs=%v", dkim, dmarc, helo, fd.recs["example.net"])
	}
	g := e.do("GET", "/api/mail/dns?domain=example.net", nil, c, "")
	if g.code != 200 {
		t.Fatalf("dns %d %v", g.code, g.body)
	}
	var mxState string
	var mxAuto bool
	for _, x := range g.body["records"].([]any) {
		m := x.(map[string]any)
		if m["key"] == "mx" {
			mxState, _ = m["state"].(string)
			mxAuto, _ = m["auto"].(bool)
		}
	}
	if mxState != "ok" || mxAuto {
		t.Fatalf("MX after create state=%q auto=%v body=%v", mxState, mxAuto, g.body["records"])
	}
}

// TestMailDomainDeleteDropsOwnDNS: removing a mail domain with drop_dns
// deletes the mail records the panel made, and only those. A hand-made
// record, the panel's CAA (not mail), and a panel record edited by hand
// since all stay; the mail.<domain> certificate is forgotten.
func TestMailDomainDeleteDropsOwnDNS(t *testing.T) {
	e := newEnv(t)
	e.srv.SetDomainsFile(t.TempDir())
	fd := &fakeDNS{n: 100, recs: map[string][]Record{"example.net": {
		{ID: "1", Type: "A", Name: "www", Value: "203.0.113.10"},
		{ID: "2", Type: "TXT", Name: "@", Value: "google-site-verification=abc"},
	}}}
	e.srv.dom.provider = func(dnsProv) DNSProvider { return fd }
	c, csrf := e.owner()
	p := e.do("POST", "/api/domains/provider", map[string]string{"kind": "cloudflare", "token": "tok-1234567890"}, c, csrf)
	if r := e.do("POST", "/api/domains/zone", map[string]any{"name": "example.net", "provider": p.body["id"]}, c, csrf); r.code != 200 {
		t.Fatalf("zone %d %v", r.code, r.body)
	}
	e.mail.Domains = []admproto.MailDomain{{Name: "example.net", Enabled: true, DKIM: true, DKIMRecord: "v=DKIM1; k=rsa; p=AAAA"}}
	if r := e.do("POST", "/api/mail/dns", map[string]string{"domain": "example.net"}, c, csrf); r.code != 200 {
		t.Fatalf("create %d %v", r.code, r.body)
	}
	// The panel's own non-mail record and a mail record changed by hand.
	e.srv.dom.mu.Lock()
	e.srv.dom.load()
	z := e.srv.dom.zoneFor("example.net")
	fd.recs["example.net"] = append(fd.recs["example.net"], Record{ID: "50", Type: "CAA", Name: "@", Value: `0 issue "letsencrypt.org"`})
	z.Ours = append(z.Ours, ourRec{Type: "CAA", Name: "@", ID: "50"})
	for i, rec := range fd.recs["example.net"] {
		if rec.Name == "_dmarc" {
			fd.recs["example.net"][i].Value = "something else"
		}
	}
	e.srv.dom.f.Certs = append(e.srv.dom.f.Certs, certRec{ID: "c1", Names: []string{"mail.example.net"}, Use: "mail"},
		certRec{ID: "c2", Names: []string{"example.net"}, Use: "node"})
	e.srv.dom.save()
	e.srv.dom.mu.Unlock()

	r := e.do("POST", "/api/mail/domain", map[string]any{"name": "example.net", "delete": true, "drop_dns": true}, c, csrf)
	if r.code != 200 {
		t.Fatalf("delete %d %v", r.code, r.body)
	}
	if n, _ := r.body["dns_removed"].(float64); n < 4 {
		t.Fatalf("dns_removed %v note %v", r.body["dns_removed"], r.body["note"])
	}
	if note, _ := r.body["note"].(string); !strings.Contains(note, "changed by hand") {
		t.Fatalf("note %q", note)
	}
	left := map[string]bool{}
	for _, rec := range fd.recs["example.net"] {
		left[rec.Type+" "+rec.Name] = true
	}
	for _, want := range []string{"A www", "TXT @", "CAA @", "TXT _dmarc"} {
		if !left[want] {
			t.Fatalf("%s deleted; left %v", want, fd.recs["example.net"])
		}
	}
	for _, gone := range []string{"MX @", "A mail", "TXT mail", "TXT mail._domainkey"} {
		if left[gone] {
			t.Fatalf("%s still there: %v", gone, fd.recs["example.net"])
		}
	}
	if len(e.mail.Domains) != 0 {
		t.Fatalf("mail domain kept: %v", e.mail.Domains)
	}
	e.srv.dom.mu.Lock()
	defer e.srv.dom.mu.Unlock()
	e.srv.dom.load()
	if len(e.srv.dom.f.Certs) != 1 || e.srv.dom.f.Certs[0].ID != "c2" {
		t.Fatalf("certs %v", e.srv.dom.f.Certs)
	}
}

// TestMailOwnRec pins which records count as the panel's mail records.
func TestMailOwnRec(t *testing.T) {
	d := "example.net"
	for _, c := range []struct {
		typ, name, value string
		want             bool
	}{
		{"MX", "example.net", "mail.example.net", true},
		{"A", "mail.example.net", "203.0.113.1", true},
		{"TXT", "example.net", "v=spf1 mx -all", true},
		{"TXT", "example.net", "google-site-verification=x", false},
		{"TXT", "mail.example.net", `"v=spf1 a -all"`, true},
		{"TXT", "old._domainkey.example.net", "v=DKIM1; p=x", true},
		{"TXT", "_dmarc.example.net", "v=DMARC1; p=none", true},
		{"CAA", "example.net", "0 issue x", false},
		{"A", "www.example.net", "203.0.113.1", false},
		{"MX", "sub.example.net", "mail.example.net", false},
	} {
		if got := mailOwnRec(c.typ, c.name, c.value, d, true); got != c.want {
			t.Fatalf("%s %s %q: %v", c.typ, c.name, c.value, got)
		}
	}
}

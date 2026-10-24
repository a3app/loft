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
}

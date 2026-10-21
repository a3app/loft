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

package adm

import (
	"regexp"
	"strings"
	"testing"

	"github.com/a3app/loft/server/panel/admproto"
)

func TestGuardJail(t *testing.T) {
	c := admproto.GuardConf{Fails: 5, FindMin: 10, BanSec: 86400, Scanners: true, Logins: true, Allow: []string{" 198.51.100.0/24", "203.0.113.7", "203.0.113.7"}}
	if err := checkGuard(&c); err != nil {
		t.Fatal(err)
	}
	if strings.Join(c.Allow, " ") != "198.51.100.0/24 203.0.113.7" {
		t.Fatalf("allow: %v", c.Allow)
	}
	j := renderGuardJail(c, true, []string{"/var/log/nginx/loft-panel.access.log"})
	for _, w := range []string{"ignoreip = 127.0.0.1/8 ::1 198.51.100.0/24 203.0.113.7", "findtime = 600", "[postfix-sasl]\nenabled = true", "[loft-scan]\nenabled = true", "bantime = -1", "banaction = loft-nftables"} {
		if !strings.Contains(j, w) {
			t.Fatalf("jail lacks %q:\n%s", w, j)
		}
	}
	if !strings.Contains(guardAction, "add rule <table_family> <table> <chain> %(rule_stat)s") || !strings.Contains(guardAction, "flags interval") {
		t.Fatal("the action must keep the rule that matches the set")
	}
	if j2 := renderGuardJail(c, false, nil); !strings.Contains(j2, "[dovecot]\nenabled = false") || strings.Contains(j2, "[loft-scan]") {
		t.Fatalf("no mail, no logs:\n%s", j2)
	}
	for _, bad := range []admproto.GuardConf{{Fails: 0, FindMin: 10, BanSec: 60}, {Fails: 5, FindMin: 10, BanSec: 5}, {Fails: 5, FindMin: 10, BanSec: 60, Allow: []string{"evil\nignoreip"}}} {
		if err := checkGuard(&bad); err == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
}

// The scanner filter, as fail2ban will read it (<HOST> is its address group).
func TestGuardScanFilter(t *testing.T) {
	f := renderScanFilter()
	re := strings.TrimSpace(strings.SplitN(f, "failregex = ", 2)[1])
	r := regexp.MustCompile(strings.Replace(re, "<HOST>", `(?P<host>\S+)`, 1))
	hit := `203.0.113.9 - - [20/Oct/2026:10:00:00 +0000] "GET /wp-login.php HTTP/1.1" 404 153 "-" "Mozilla"`
	deep := `203.0.113.9 - - [20/Oct/2026:10:00:00 +0000] "GET /blog/.env HTTP/1.1" 404 153 "-" "x"`
	miss := `203.0.113.9 - - [20/Oct/2026:10:00:00 +0000] "GET /api/state HTTP/1.1" 200 153 "-" "x"`
	// fail2ban matches after cutting the date out of the line.
	cut := strings.Replace(hit, "[20/Oct/2026:10:00:00 +0000]", "", 1)
	if !r.MatchString(hit) || !r.MatchString(cut) || !r.MatchString(deep) || r.MatchString(miss) {
		t.Fatal("scanner filter")
	}
	if m := regexp.MustCompile(strings.Replace("login failed from <HOST>(?: |$)", "<HOST>", `(\S+)`, 1)).FindStringSubmatch("2026/10/20 10:00:00 login failed from 198.51.100.4"); len(m) < 2 || m[1] != "198.51.100.4" {
		t.Fatalf("panel filter: %v", m)
	}
}

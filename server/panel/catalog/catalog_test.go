package catalog

import (
	"strings"
	"testing"
)

func TestPlanCheck(t *testing.T) {
	p := Plan{Install: []string{"fail2ban", "mtproto", "wireguard"}}
	if err := p.Check(); err != nil {
		t.Fatal(err)
	}
	if p.Install[0] != "mtproto" || p.Install[1] != "wireguard" {
		t.Fatalf("not in catalog order: %v", p.Install)
	}
	for _, bad := range []Plan{
		{},
		{Install: []string{"frobnicate"}},
		{Remove: []string{"node"}},
		{Install: []string{"wireguard", "wireguard"}},
		{Install: []string{"openvpn"}, Remove: []string{"openvpn"}},
	} {
		if err := bad.Check(); err == nil {
			t.Fatalf("%+v passed", bad)
		}
	}
}

func TestCatalog(t *testing.T) {
	seen := map[string]bool{}
	for _, p := range All() {
		if seen[p.Key] {
			t.Fatalf("%s twice", p.Key)
		}
		seen[p.Key] = true
		if p.Method == MethodRelease && (p.Release == nil || p.Release.Assets["amd64"] == "" || p.Unit == "") {
			t.Fatalf("%s: release without assets or unit", p.Key)
		}
		if p.Method == MethodApt && len(p.Apt) == 0 {
			t.Fatalf("%s: apt without packages", p.Key)
		}
		if p.Method == MethodBin {
			if p.Bin == nil || p.Bin.Repo == "" || p.Bin.Tag == "" || p.Bin.Path == "" || p.Bin.Name == "" || !strings.Contains(p.Bin.Asset, "%s") {
				t.Fatalf("%s: bin fields", p.Key)
			}
			for arch, sum := range p.Bin.SHA256 {
				if len(sum) != 64 {
					t.Fatalf("%s: SHA-256 for %s", p.Key, arch)
				}
			}
			if p.Bin.SHA256["amd64"] == "" || p.Bin.SHA256["arm64"] == "" {
				t.Fatalf("%s: need amd64 and arm64", p.Key)
			}
		}
		if p.Repo != nil && (!strings.Contains(p.Repo.Key, "BEGIN PGP PUBLIC KEY BLOCK") || !strings.Contains(p.Repo.URL, "%s")) {
			t.Fatalf("%s: repo key or URL", p.Key)
		}
		for _, d := range p.DataDirs {
			if !strings.HasPrefix(d, "/var/lib/") && d != "/var/vmail" {
				t.Fatalf("%s: data dir %s outside /var/lib", p.Key, d)
			}
		}
	}
}

func TestConsistent(t *testing.T) {
	none := map[string]bool{}
	mail := map[string]bool{"mail": true, "antispam": true}
	for _, c := range []struct {
		p    Plan
		have map[string]bool
		ok   bool
	}{
		{Plan{Install: []string{"antispam"}}, none, false},
		{Plan{Install: []string{"mail", "antispam"}}, none, true},
		{Plan{Install: []string{"webmail"}}, mail, true},
		{Plan{Remove: []string{"mail"}}, mail, false},
		{Plan{Remove: []string{"mail", "antispam"}}, mail, true},
		{Plan{Remove: []string{"antispam"}}, mail, true},
	} {
		if err := c.p.Consistent(c.have); (err == nil) != c.ok {
			t.Fatalf("%+v with %v: %v", c.p, c.have, err)
		}
	}
	if d := Dependents("mail"); len(d) != 2 || d[0] != "antispam" {
		t.Fatalf("dependents %v", d)
	}
}

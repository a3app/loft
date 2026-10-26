// Package testcfg loads hostnames for tests.
// testdata/domains.yaml is the committed example.com set.
// testdata/domains.local.yaml (gitignored) overlays a live stand.
//
// Tests never hard-code real hostnames. They ask this package, which
// returns example.com names by default; a developer who tests against a
// real node ("stand") puts the real names in the local file instead.
package testcfg

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"

	"gopkg.in/yaml.v3"
)

// Set is the hostnames tests dial and compare. Empty overlay fields keep the base.
type Set struct {
	Front     string `yaml:"front"`
	FrontPort string `yaml:"front_port"`
	CDN       string `yaml:"cdn"`
	Decoy     string `yaml:"decoy"`
	Decoy2    string `yaml:"decoy2"`
}

var (
	once sync.Once
	cur  Set
)

// defaults are the hostnames used when no testdata file is found.
func defaults() Set {
	return Set{
		Front:     "loft.example.com",
		FrontPort: "444",
		CDN:       "cdn.example.com",
		Decoy:     "www.example.net",
		Decoy2:    "origin.example.org",
	}
}

// overlay returns base with every non-empty field of over applied.
func overlay(base, over Set) Set {
	if over.Front != "" {
		base.Front = over.Front
	}
	if over.FrontPort != "" {
		base.FrontPort = over.FrontPort
	}
	if over.CDN != "" {
		base.CDN = over.CDN
	}
	if over.Decoy != "" {
		base.Decoy = over.Decoy
	}
	if over.Decoy2 != "" {
		base.Decoy2 = over.Decoy2
	}
	return base
}

func loadFile(path string) (Set, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return Set{}, err
	}
	var s Set
	if err := yaml.Unmarshal(raw, &s); err != nil {
		return Set{}, err
	}
	return s, nil
}

// root is the repository root, found from this source file's path so tests
// in any package directory read the same testdata.
func root() string {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "."
	}
	return filepath.Clean(filepath.Join(filepath.Dir(file), "../.."))
}

// load applies defaults, then domains.yaml, then domains.local.yaml.
func load() Set {
	s := defaults()
	dir := filepath.Join(root(), "testdata")
	if got, err := loadFile(filepath.Join(dir, "domains.yaml")); err == nil {
		s = overlay(s, got)
	}
	if got, err := loadFile(filepath.Join(dir, "domains.local.yaml")); err == nil {
		s = overlay(s, got)
	}
	return s
}

func hosts() Set {
	once.Do(func() { cur = load() })
	return cur
}

// parent drops the first label (loft.example.com -> example.com).
func parent(host string) string {
	i := strings.Index(host, ".")
	if i < 0 {
		return host
	}
	return host[i+1:]
}

// Front is the node advertise hostname.
func Front() string { return hosts().Front }

// FrontPort is the node listen port in URLs (default 444).
func FrontPort() string { return hosts().FrontPort }

// CDN is the CDN edge hostname.
func CDN() string { return hosts().CDN }

// Decoy is the primary dest hostname.
func Decoy() string { return hosts().Decoy }

// Decoy2 is the second dest hostname.
func Decoy2() string { return hosts().Decoy2 }

// Parent is the registrable suffix of Front (example.com from loft.example.com).
func Parent() string { return parent(Front()) }

// DecoyParent is the registrable suffix of Decoy.
func DecoyParent() string { return parent(Decoy()) }

// Wildcard is *.Parent for Front (one extra label).
func Wildcard() string { return "*." + Parent() }

// DecoyWildcard is *.DecoyParent.
func DecoyWildcard() string { return "*." + DecoyParent() }

// Deep is two extra labels under Parent (must not match Wildcard).
func Deep() string { return "a.b." + Parent() }

// Leaf is one extra label under DecoyParent (matches DecoyWildcard).
func Leaf() string { return "lk." + DecoyParent() }

// FrontAddr is host:port for the node.
func FrontAddr() string { return Front() + ":" + FrontPort() }

// FrontHTTPS is https://Front:Port.
func FrontHTTPS() string { return "https://" + FrontAddr() }

// FrontWSS is wss://Front:Port/api/v1/sync.
func FrontWSS() string { return "wss://" + FrontAddr() + "/api/v1/sync" }

// CDNHTTPS is https://CDN (port 443 implied).
func CDNHTTPS() string { return "https://" + CDN() }

// DecoyAddr is Decoy:443.
func DecoyAddr() string { return Decoy() + ":443" }

// Decoy2Addr is Decoy2:443.
func Decoy2Addr() string { return Decoy2() + ":443" }

// FoldedFront is Front in upper case with a trailing dot (SNI case/FQDN).
func FoldedFront() string { return strings.ToUpper(Front()) + "." }

// FoldedDecoy is Decoy in upper case.
func FoldedDecoy() string { return strings.ToUpper(Decoy()) }

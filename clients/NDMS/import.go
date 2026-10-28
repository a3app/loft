package ndms

import (
	"fmt"
	"net"
	"net/url"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/a3app/loft/clients/mobile"
	"github.com/a3app/loft/internal/config"
)

// ImportResult is what a paste or a file added, and what was skipped.
type ImportResult struct {
	Added   []Conn   `json:"added"`
	Skipped []string `json:"skipped,omitempty"`
}

// ImportText turns a loft:// / vless:// / trojan:// link, a .loft YAML,
// a client.yaml, or several lines of links into new connections.
// SSTP is skipped: Keenetic already has that client.
func ImportText(raw string) (ImportResult, error) {
	raw = strings.TrimSpace(strings.TrimPrefix(raw, "\ufeff"))
	if raw == "" {
		return ImportResult{}, fmt.Errorf("empty")
	}
	if conns, ok := importBundle(raw); ok {
		return ImportResult{Added: conns}, nil
	}
	y, err := mobile.ParseShareAll(raw)
	if err != nil {
		return ImportResult{}, err
	}
	var res ImportResult
	for _, doc := range strings.Split(y, "\n---\n") {
		doc = strings.TrimSpace(doc)
		if doc == "" {
			continue
		}
		c, skip, err := connFromDoc(doc)
		if skip != "" {
			res.Skipped = append(res.Skipped, skip)
			continue
		}
		if err != nil {
			return ImportResult{}, err
		}
		res.Added = append(res.Added, c)
	}
	if len(res.Added) == 0 {
		if len(res.Skipped) > 0 {
			return res, fmt.Errorf("%s", res.Skipped[0])
		}
		return res, fmt.Errorf("empty")
	}
	return res, nil
}

// importBundle recognises this package's own YAML (a keenetic: block, or
// connections without a top-level token) and copies each record.
func importBundle(raw string) ([]Conn, bool) {
	var probe struct {
		Keenetic    yaml.Node         `yaml:"keenetic"`
		Connections []Conn            `yaml:"connections"`
		Token       string            `yaml:"token"`
		Outbounds   []config.Outbound `yaml:"outbounds"`
	}
	if err := yaml.Unmarshal([]byte(raw), &probe); err != nil {
		return nil, false
	}
	// A desktop client.yaml has connections and a token. Accept this
	// package's file (a keenetic: key), or connections that have neither
	// a token nor outbounds. Anything else falls through to share parsing,
	// which must run second: a bundle would otherwise be read as one profile.
	if probe.Keenetic.IsZero() && !(len(probe.Connections) > 0 && probe.Token == "" && len(probe.Outbounds) == 0) {
		return nil, false
	}
	if len(probe.Connections) == 0 {
		return nil, false
	}
	var out []Conn
	for _, c := range probe.Connections {
		c.ID = ""
		c.Opkg = -1
		c.Enabled = true
		if c.Kind == "sstp" {
			continue
		}
		out = append(out, c)
	}
	return out, true
}

// connFromDoc maps one share document to a Conn. SSTP and a routes
// document are skipped: SSTP is the stock Keenetic client, and a route
// profile is not a tunnel. A vless or trojan outbound is stored as a link.
// Any other document is mode file, so the profile YAML is what the child runs
// until the editor saves over it.
func connFromDoc(doc string) (Conn, string, error) {
	var meta struct {
		Name      string            `yaml:"name"`
		Kind      string            `yaml:"kind"`
		Token     string            `yaml:"token"`
		UUID      string            `yaml:"uuid"`
		Password  string            `yaml:"password"`
		Select    string            `yaml:"select"`
		Fronts    []string          `yaml:"fronts"`
		CC        string            `yaml:"cc"`
		CCUp      int               `yaml:"cc_up_mbps"`
		CCDown    int               `yaml:"cc_down_mbps"`
		Outbounds []config.Outbound `yaml:"outbounds"`
	}
	if err := yaml.Unmarshal([]byte(doc), &meta); err != nil {
		return Conn{}, "", err
	}
	kind := strings.ToLower(strings.TrimSpace(meta.Kind))
	if kind == "" && len(meta.Outbounds) > 0 {
		kind = strings.ToLower(meta.Outbounds[0].Type)
	}
	if kind == "sstp" || kind == "routes" {
		return Conn{}, "SSTP is the stock Keenetic client", nil
	}
	if kind != "vless" && kind != "trojan" {
		kind = "loft"
	}
	c := Conn{
		Name: meta.Name, Kind: kind, Enabled: true, Opkg: -1,
		Mode: "file", Profile: doc, Token: meta.Token, UUID: meta.UUID,
		Password: meta.Password, Fronts: meta.Fronts, CC: meta.CC,
		CCUp: meta.CCUp, CCDown: meta.CCDown, Connect: meta.Select,
	}
	if len(meta.Outbounds) > 0 {
		o := meta.Outbounds[0]
		if kind == "vless" || kind == "trojan" {
			c.Link = o.URL
			c.Mode = "link"
		}
		if u, err := url.Parse(o.URL); err == nil {
			c.Host = u.Hostname()
			if p := u.Port(); p != "" {
				fmtPort(&c.Port, p)
			}
		}
		if c.Path == "" {
			c.Path = o.Path
		}
		if c.Connect == "" || c.Connect == "auto" {
			c.Connect = o.Type
		}
		var fronts []string
		seen := map[string]bool{}
		for _, ob := range meta.Outbounds {
			t := strings.ToLower(ob.Type)
			if loftFront(t) && !seen[t] {
				seen[t] = true
				fronts = append(fronts, t)
			}
		}
		if len(fronts) > 0 {
			c.Fronts = fronts
		}
	}
	if c.Name == "" {
		c.Name = c.Host
	}
	if c.Name == "" {
		c.Name = kind
	}
	return c, "", nil
}

// fmtPort writes a numeric port. LookupPort also accepts a service name.
// A value it rejects leaves the destination unchanged.
func fmtPort(dst *int, p string) {
	n, err := net.LookupPort("tcp", p)
	if err != nil {
		return
	}
	*dst = n
}

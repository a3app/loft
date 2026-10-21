// Package config loads client.yaml and node config (YAML or switch-style
// commands). CDN vendor names are deployment profiles, not protocol caps.
package config

import (
	"fmt"
	"os"

	"gopkg.in/yaml.v3"

	"github.com/a3app/loft/internal/startup"
)

// ClientConfig is clients/go/configs/client.yaml.
type ClientConfig struct {
	Listen        ListenConfig      `yaml:"listen"`
	Token         string            `yaml:"token"`
	MemberID      string            `yaml:"member_id,omitempty"`
	InstanceID    string            `yaml:"instance_id,omitempty"`
	Select        string            `yaml:"select"` // get | ws | h2 | quic | h3 | auto
	Fronts        []string          `yaml:"fronts"` // enabled fronts; empty = all outbound types
	Outbounds     []Outbound        `yaml:"outbounds"`
	ExtraCaps     uint64            `yaml:"-"`
	Bootstrap     string            `yaml:"bootstrap"` // HTTPS URL of signed roster manifest
	ControllerPub string            `yaml:"controller_pub"`
	Tofu          bool              `yaml:"tofu"` // empty controller_pub: pin first valid roster (off = refuse)
	Use           map[string]string `yaml:"use"`
	Default       string            `yaml:"default"`
	ListsURL      string            `yaml:"lists_url"`
	P2P           *bool             `yaml:"p2p"`             // nil/true: try direct; false: only hub
	Pause         string            `yaml:"pause,omitempty"` // empty | starlink — client draft pause, not on the wire
}

// Published SOCKS5 (GUI / client.yaml). Debug CLI binds DebugSOCKS5 so the
// two can run at once.
const (
	DefaultSOCKS5 = "127.0.0.1:1080"
	DebugSOCKS5   = "127.0.0.1:18080"
)

// ListenConfig is local SOCKS5 and optional TUN.
type ListenConfig struct {
	SOCKS5     string `yaml:"socks5"`
	TUN        string `yaml:"tun"` // empty = off; "auto" creates utun
	TunAddr    string `yaml:"tun_addr"`
	KillSwitch bool   `yaml:"kill_switch"`
}

// Outbound is one TLS edge the client will try.
type Outbound struct {
	Name         string `yaml:"name"`
	Type         string `yaml:"type"` // get | ws | h2 | quic | h3
	CDN          string `yaml:"cdn"`  // optional edge profile: yandex | cloudflare | empty=generic
	URL          string `yaml:"url"`
	Path         string `yaml:"path"`
	SNI          string `yaml:"sni"`
	Fingerprint  string `yaml:"fingerprint"` // chrome | firefox | go
	Decoy        string `yaml:"decoy"`       // optional last-known node identity (host or host:port); empty = discover
	HeaderBudget int    `yaml:"header_budget"`
	Inflight     int    `yaml:"inflight"`
	Insecure     bool   `yaml:"insecure_skip_verify"`
	Mux          *bool  `yaml:"mux,omitempty"`      // nil/true: inner mux; false: HTTP/2 CONNECT splice
	MaskPub      string `yaml:"mask_pub,omitempty"` // 32-byte node X25519, hex or base64url
}

// UseMux is inner mux unless mux: false (HTTP/2 CONNECT splice).
func (o Outbound) UseMux() bool {
	return o.Mux == nil || *o.Mux
}

// ServerConfig is node listen, fronts, decoy, and controller/relay role.
type ServerConfig struct {
	Listen        string              `yaml:"listen"`
	CertFile      string              `yaml:"cert"`
	KeyFile       string              `yaml:"key"`
	Token         string              `yaml:"token"`
	CoverDir      string              `yaml:"cover_dir"`
	GetPath       string              `yaml:"get_path"`
	GetH2         int                 `yaml:"get_h2"` // GET HTTP/2 connections, 1–4; 0 = one
	WSPath        string              `yaml:"ws_path"`
	H2Path        string              `yaml:"h2_path"`
	H3Path        string              `yaml:"h3_path"`
	Fronts        []string            `yaml:"fronts"`      // enabled fronts; empty = all
	Decoy         []string            `yaml:"decoy"`       // this node's splice origins; first = identity; empty = off
	DecoyProxy    bool                `yaml:"decoy_proxy"` // send a PROXY v1 header on TCP splice (own nginx with proxy_protocol only)
	CDNPeers      []string            `yaml:"cdn_peers"`   // CIDRs allowed to use own SNI without sid (CDN origin pull)
	Mask          bool                `yaml:"mask"`        // borrowed origin cert on loft TLS path
	QuicALPN      string              `yaml:"quic_alpn"`   // default h3; "loft" only for old clients
	Replicas      []string            `yaml:"replicas"`
	Canary        CanaryConfig        `yaml:"canary"`
	Role          string              `yaml:"role"`           // controller | relay; empty = controller
	Controller    string              `yaml:"controller"`     // relay: hub URL
	RelayToken    string              `yaml:"relay_token"`    // empty = Token
	ControllerKey string              `yaml:"controller_key"` // path to ed25519 seed
	Advertise     string              `yaml:"advertise"`      // public URL of this node, for roster
	Relays        []RelayEntry        `yaml:"relays"`         // allowlist + defaults (controller)
	Enrol         string              `yaml:"enrol"`          // list (default) | open
	AuthShared    bool                `yaml:"auth_shared"`    // accept LOFT_TOKEN as client secret when members.keys is empty
	ID            string              `yaml:"id"`             // relay identity
	RosterPath    string              `yaml:"roster_path"`
	ListsPath     string              `yaml:"lists_path"`
	Lists         map[string][]string `yaml:"lists"`
	Trace         string              `yaml:"trace"` // session event log path; empty = off
}

// RelayEntry is one controller allowlist row for enrol: list.
type RelayEntry struct {
	ID      string   `yaml:"id"`
	URL     string   `yaml:"url"`
	Decoy   string   `yaml:"decoy"`
	Fronts  []string `yaml:"fronts"`
	Drain   bool     `yaml:"drain"`
	Weight  byte     `yaml:"weight"`
	Path    string   `yaml:"path"`
	MaskPub string   `yaml:"mask_pub"` // hex, optional
}

// CanaryConfig is the path canary: how this node presents TLS so members
// can tell a healthy HTTPS path from a mangled one. Off by default except fat_hello.
type CanaryConfig struct {
	FatHello   *bool `yaml:"fat_hello"`   // nil/true: Chrome-sized ClientHello (PQ)
	SplitHello bool  `yaml:"split_hello"` // ClientHello in two TLS records
	Pad        int   `yaml:"pad"`         // extra padding extension bytes, 0–2048
}

// Status canary_flags: bit 0 fat ClientHello, bit 1 split ClientHello.
const (
	CanaryFat   byte = 1 << 0
	CanarySplit byte = 1 << 1
)

// Fat is on by default (Chrome-sized ClientHello) unless fat_hello is false.
func (d CanaryConfig) Fat() bool {
	if d.FatHello == nil {
		return true
	}
	return *d.FatHello
}

// Flags is the Status canary_flags byte.
func (d CanaryConfig) Flags() byte {
	var f byte
	if d.Fat() {
		f |= CanaryFat
	}
	if d.SplitHello {
		f |= CanarySplit
	}
	return f
}

// Pad16 clamps extra ClientHello padding to 0–2048.
func (d CanaryConfig) Pad16() uint16 {
	n := d.Pad
	if n < 0 {
		return 0
	}
	if n > 2048 {
		return 2048
	}
	return uint16(n)
}

// LoadClient reads YAML. Token and at least one outbound are required.
func LoadClient(path string) (*ClientConfig, error) {
	var c ClientConfig
	if err := load(path, &c); err != nil {
		return nil, err
	}
	if c.Listen.SOCKS5 == "" {
		c.Listen.SOCKS5 = DebugSOCKS5
	}
	if c.Select == "" {
		c.Select = "auto"
	}
	if c.Token == "" {
		return nil, fmt.Errorf("token is required")
	}
	if len(c.Outbounds) == 0 {
		return nil, fmt.Errorf("at least one outbound is required")
	}
	for i := range c.Outbounds {
		o := &c.Outbounds[i]
		if o.Fingerprint == "" {
			o.Fingerprint = "chrome"
		}
		if o.HeaderBudget <= 0 {
			o.HeaderBudget = 16 * 1024
		}
		if o.Inflight <= 0 {
			o.Inflight = 48
		}
		if o.Type == "" {
			return nil, fmt.Errorf("outbound %q: type is required", o.Name)
		}
		if o.URL == "" {
			return nil, fmt.Errorf("outbound %q: url is required", o.Name)
		}
	}
	return &c, nil
}

// LoadServer accepts YAML or switch-style commands.
func LoadServer(path string) (*ServerConfig, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	if startup.LooksYAML(string(b)) {
		var c ServerConfig
		if err := yaml.Unmarshal(b, &c); err != nil {
			return nil, err
		}
		return finishServer(&c)
	}
	return ParseCommands(string(b))
}

// finishServer fills listen :443, default paths, ALPN h3, and role controller.
func finishServer(c *ServerConfig) (*ServerConfig, error) {
	if c.Listen == "" {
		c.Listen = ":443"
	}
	if c.GetPath == "" {
		c.GetPath = "/static/v3/build"
	}
	if c.WSPath == "" {
		c.WSPath = "/api/v1/sync"
	}
	if c.H2Path == "" {
		c.H2Path = "/api/v1/tunnel"
	}
	if c.H3Path == "" {
		c.H3Path = "/api/v1/tunnel"
	}
	if c.QuicALPN == "" {
		c.QuicALPN = "h3"
	}
	if c.Role == "" {
		c.Role = "controller"
	}
	if c.RosterPath == "" {
		c.RosterPath = "/static/v3/manifest.json"
	}
	if c.ListsPath == "" {
		c.ListsPath = "/static/v3/lists.json"
	}
	if c.GetH2 > 4 {
		c.GetH2 = 4
	}
	return c, nil
}

// IsRelay is role relay (TLS edge); otherwise this node is the controller.
func (c *ServerConfig) IsRelay() bool { return c.Role == "relay" }

// EnabledFronts is cfg.fronts, else outbound types, else FrontOrder.
func (c *ClientConfig) EnabledFronts() []string {
	if len(c.Fronts) > 0 {
		return c.Fronts
	}
	seen := map[string]bool{}
	var out []string
	for _, o := range c.Outbounds {
		if seen[o.Type] {
			continue
		}
		seen[o.Type] = true
		out = append(out, o.Type)
	}
	if len(out) == 0 {
		return append([]string(nil), []string{"get", "ws", "h2", "quic", "h3"}...)
	}
	return out
}

// Allows is true when fronts is empty or lists front.
func (c *ClientConfig) Allows(front string) bool {
	if len(c.Fronts) == 0 {
		return true
	}
	for _, f := range c.Fronts {
		if f == front {
			return true
		}
	}
	return false
}

// EnabledFronts is cfg.fronts, else every protocol front.
func (c *ServerConfig) EnabledFronts() []string {
	if len(c.Fronts) == 0 {
		return []string{"get", "ws", "h2", "quic", "h3"}
	}
	return append([]string(nil), c.Fronts...)
}

// HasFront reports an enabled node front.
func (c *ServerConfig) HasFront(front string) bool {
	for _, f := range c.EnabledFronts() {
		if f == front {
			return true
		}
	}
	return false
}

// load unmarshals YAML from path into v.
func load(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return yaml.Unmarshal(b, v)
}

// Pick returns outbounds for select: auto (all) or a matching name/type.
func (c *ClientConfig) Pick(name string) []Outbound {
	if name == "" || name == "auto" {
		return c.Outbounds
	}
	for _, o := range c.Outbounds {
		if o.Name == name || o.Type == name {
			return []Outbound{o}
		}
	}
	return nil
}

// Package config loads client.yaml and node config (YAML or switch-style
// commands). CDN vendor names are deployment profiles, not protocol caps.
//
// Two programs read this package. The client (loft) reads client.yaml: its
// member key, the local SOCKS5/TUN inbound, and the list of outbounds (the
// node addresses and fronts to try). The node (loft-node) reads its own
// config (server.yaml, or the same settings written as switch-style
// commands): listen address, certificate, enabled fronts, decoy site, and
// its fleet role (controller or relay). The overlay network itself
// (members, IPs, ACL) is not here; it lives in network.conf (internal/netconf).
package config

import (
	"fmt"
	"os"
	"regexp"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/a3app/loft/internal/startup"
	"github.com/a3app/loft/internal/wire"
)

// ClientConfig is clients/go/configs/client.yaml.
//
// Bootstrap, ControllerPub and Tofu control the roster: the client fetches
// the controller-signed list of entry points from Bootstrap and checks it
// against ControllerPub. With Tofu ("trust on first use") and no key set,
// the first valid roster's key is pinned. Use and Default map the names of
// signed address lists to routing actions (direct, via loft, drop).
type ClientConfig struct {
	Listen        ListenConfig      `yaml:"listen"`
	Token         string            `yaml:"token"`
	MemberID      string            `yaml:"member_id,omitempty"`
	InstanceID    string            `yaml:"instance_id,omitempty"`
	Select        string            `yaml:"select"` // get | ws | h2 | quic | h3 | l3 | auto
	Fronts        []string          `yaml:"fronts"` // extra filter; empty = outbound types (else GET)
	Outbounds     []Outbound        `yaml:"outbounds"`
	ExtraCaps     uint64            `yaml:"-"`
	Bootstrap     string            `yaml:"bootstrap"` // HTTPS URL of signed roster manifest
	ControllerPub string            `yaml:"controller_pub"`
	Tofu          bool              `yaml:"tofu"` // empty controller_pub: pin first valid roster (off = refuse)
	Use           map[string]string `yaml:"use"`
	Default       string            `yaml:"default"`
	ListsURL      string            `yaml:"lists_url"`
	P2P           *bool             `yaml:"p2p"`                    // nil/true: try direct; false: only hub
	Pause         string            `yaml:"pause,omitempty"`        // empty | starlink — client draft pause, not on the wire
	EdgeURL       string            `yaml:"edge,omitempty"`         // optional CDN URL; GET only. Not the node preset plain|tight
	Inner         string            `yaml:"inner,omitempty"`        // clear | chacha | aes. Empty omits the Hello byte (ChaCha)
	CC            string            `yaml:"cc,omitempty"`           // congestion control ask: off | bbr | fixed. Empty = off
	CCUpMbps      uint32            `yaml:"cc_up_mbps,omitempty"`   // fixed: how fast this client may send, Mbit/s
	CCDownMbps    uint32            `yaml:"cc_down_mbps,omitempty"` // fixed: how fast this client can receive, Mbit/s
	// Experimental (branch experimentos). Disorder sends the start of the
	// outer ClientHello with TTL 1 so DPI misses the server name
	// (transport/disorder.go). L3Shape asks the node to shape the l3 front
	// against "TLS inside TLS" detection (transport/l3shape.go).
	Disorder bool `yaml:"disorder,omitempty"`
	L3Shape  bool `yaml:"l3_shape,omitempty"`
}

// Published SOCKS5 (GUI / client.yaml). Debug CLI binds DebugSOCKS5 so the
// two can run at once.
const (
	DefaultSOCKS5 = "127.0.0.1:1080"
	DebugSOCKS5   = "127.0.0.1:18080"
	// SOCKSOff as listen.socks5 turns the SOCKS5 listener off (loft CLI).
	// An empty value still means DebugSOCKS5, so old configs keep a proxy.
	SOCKSOff = "off"
	// DefaultGETPath is empty node get_path and empty client GET outbound.
	DefaultGETPath = "/static/v3/build"
	DefaultWSPath  = "/api/v1/sync"
	DefaultH2Path  = "/api/v1/tunnel"
)

// ListenConfig is local SOCKS5 and optional TUN.
type ListenConfig struct {
	SOCKS5     string `yaml:"socks5"`
	TUN        string `yaml:"tun"` // empty = off; "auto" creates utun
	TunAddr    string `yaml:"tun_addr"`
	KillSwitch bool   `yaml:"kill_switch"`
	HTTP       string `yaml:"http,omitempty"`      // LAN operator page, e.g. 0.0.0.0:8090
	HTTPPass   string `yaml:"http_pass,omitempty"` // empty = open on the bind
	Bind       string `yaml:"bind,omitempty"`      // same as -if; CLI -if wins
	Uplink     string `yaml:"uplink,omitempty"`    // RouterOS WAN name for container egress
	// Gateway is how a router in front of loft hands LAN traffic to the TUN
	// (the MikroTik container): "nat" (or empty) means the router rewrites
	// LAN sources to its own address (masquerade), so loft needs nothing
	// extra; "route" keeps the real LAN addresses, and loft then sends its
	// answers back to the router by a policy rule. See docs/mikrotik.md.
	Gateway string `yaml:"gateway,omitempty"`
}

// ValidGatewayMode accepts the listen.gateway values: empty, nat, route.
func ValidGatewayMode(s string) error {
	switch s {
	case "", "nat", "route":
		return nil
	}
	return fmt.Errorf("listen.gateway %q: use nat or route", s)
}

var ifNameRe = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9._-]{0,62}$`)

// ValidIFName is empty or a RouterOS / OS interface name (ether1, lte1, en0).
func ValidIFName(s string) error {
	if s == "" {
		return nil
	}
	if !ifNameRe.MatchString(s) {
		return fmt.Errorf("interface %q: use a name like ether1 or lte1", s)
	}
	return nil
}

// WriteClient stores cfg as YAML, mode 0600.
func WriteClient(path string, cfg *ClientConfig) error {
	if cfg == nil {
		return fmt.Errorf("nil config")
	}
	if err := ValidIFName(cfg.Listen.Bind); err != nil {
		return err
	}
	if err := ValidIFName(cfg.Listen.Uplink); err != nil {
		return err
	}
	if err := ValidGatewayMode(cfg.Listen.Gateway); err != nil {
		return err
	}
	b, err := yaml.Marshal(cfg)
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o600)
}

// Outbound is one TLS edge the client will try: a node (or a CDN in front
// of it) at URL, reached over one front (Type). Fingerprint picks which
// browser's TLS ClientHello to imitate. Decoy and MaskPub are what the
// client last learned about the node's cover site and mask key.
type Outbound struct {
	Name         string `yaml:"name"`
	Type         string `yaml:"type"` // get | ws | h2 | quic | h3 | l3
	CDN          string `yaml:"cdn"`  // optional edge profile: yandex | cloudflare | empty=generic
	URL          string `yaml:"url"`
	Path         string `yaml:"path"`
	SNI          string `yaml:"sni"`
	Fingerprint  string `yaml:"fingerprint"` // chrome | firefox | edge | 360 | qq | go
	Decoy        string `yaml:"decoy"`       // optional last-known node identity (host or host:port); empty = discover
	HeaderBudget int    `yaml:"header_budget"`
	Inflight     int    `yaml:"inflight"`
	H2MaxAgeMS   int    `yaml:"h2_max_age_ms"` // GET: recycle HTTP/2 TCP; 0 = keep until error
	Insecure     bool   `yaml:"insecure_skip_verify"`
	Mux          *bool  `yaml:"mux,omitempty"`      // nil/true: inner mux; false: HTTP/2 CONNECT splice
	MaskPub      string `yaml:"mask_pub,omitempty"` // 32-byte node X25519, hex or base64url
}

// UseMux is inner mux unless mux: false (HTTP/2 CONNECT splice).
func (o Outbound) UseMux() bool {
	return o.Mux == nil || *o.Mux
}

// ServerConfig is node listen, fronts, decoy, and controller/relay role.
//
// A controller is the main node: it holds members, leases and the signing
// key. A relay is an extra entry point that forwards its clients to the
// controller over one mux "trunk" and enrols itself in the controller's
// roster (Enrol: list = only relays named in Relays, open = any relay with
// a valid key).
type ServerConfig struct {
	Listen        string              `yaml:"listen"`
	CertFile      string              `yaml:"cert"`
	KeyFile       string              `yaml:"key"`
	Token         string              `yaml:"token"`
	CoverDir      string              `yaml:"cover_dir"`
	GetPath       string              `yaml:"get_path"`
	GetH2         int                 `yaml:"get_h2"` // GET HTTP/2 connections, 1–4; 0 = one, or four when edge is tight
	WSPath        string              `yaml:"ws_path"`
	H2Path        string              `yaml:"h2_path"`
	H3Path        string              `yaml:"h3_path"`
	Fronts        []string            `yaml:"fronts"`      // enabled fronts; empty = GET
	Decoy         []string            `yaml:"decoy"`       // this node's splice origins; first = identity; empty = off
	DecoyProxy    bool                `yaml:"decoy_proxy"` // send a PROXY v1 header on TCP splice (own nginx with proxy_protocol only)
	CDNPeers      []string            `yaml:"cdn_peers"`   // CIDRs allowed to use own SNI without sid (CDN origin pull)
	CDN           string              `yaml:"cdn"`         // legacy GET profile: yandex selects edge tight when edge is empty
	Edge          string              `yaml:"edge"`        // plain | tight; empty = plain, or tight when cdn is yandex
	Mask          bool                `yaml:"mask"`        // borrowed origin cert on loft TLS path
	QuicALPN      string              `yaml:"quic_alpn"`   // default h3; "loft" only for old clients
	Replicas      []string            `yaml:"replicas"`
	Canary        CanaryConfig        `yaml:"canary"`
	Role          string              `yaml:"role"`           // controller | relay; empty = controller
	Controller    string              `yaml:"controller"`     // relay: hub URL
	ControllerAlt []string            `yaml:"controller_alt"` // relay: standby controllers' hub URLs, tried after Controller
	PromoteAfter  int                 `yaml:"promote_after"`  // standby: seconds without the controller before auto-promote (default 300)
	RelayToken    string              `yaml:"-"`              // relay: its own key, from {dir}/relay.key
	ControllerPub string              `yaml:"controller_pub"` // relay: controller Ed25519 key (hex) that signs its slice
	ControllerKey string              `yaml:"controller_key"` // path to ed25519 seed
	Advertise     string              `yaml:"advertise"`      // public URL of this node, for roster
	Relays        []RelayEntry        `yaml:"relays"`         // relays allowed to enrol, each with its key (controller)
	AuthShared    bool                `yaml:"auth_shared"`    // accept LOFT_TOKEN as client secret when members.keys is empty
	ID            string              `yaml:"id"`             // relay identity
	RosterPath    string              `yaml:"roster_path"`
	ListsPath     string              `yaml:"lists_path"`
	Lists         map[string][]string `yaml:"lists"`
	Trace         string              `yaml:"trace"`                      // session event log path; empty = off
	Inner         []string            `yaml:"inner,omitempty"`            // extra data ciphers: clear, aes. Empty = ChaCha only
	CC            []string            `yaml:"cc,omitempty"`               // congestion control clients may ask for: bbr, fixed. Empty = off
	CCMaxUpMbps   uint32              `yaml:"cc_max_up_mbps,omitempty"`   // fixed: cap on a client's sending rate; 0 = none
	CCMaxDownMbps uint32              `yaml:"cc_max_down_mbps,omitempty"` // fixed: cap on the node's sending rate; 0 = none
}

// RelayEntry is one controller allowlist row for enrol: list.
type RelayEntry struct {
	ID        string `yaml:"id"`
	KeySHA256 string `yaml:"key_sha256"` // SHA-256 of the relay's key; the key itself is in relays.keys
	Replica   bool   `yaml:"replica"`    // a standby controller: it gets a copy of the controller's state
	// AutoPromote lets this standby become the controller by itself when it
	// and most relays have lost the controller for PromoteAfter.
	AutoPromote bool     `yaml:"auto_promote"`
	URL         string   `yaml:"url"`
	Decoy       string   `yaml:"decoy"`
	Fronts      []string `yaml:"fronts"`
	Drain       bool     `yaml:"drain"`
	Weight      byte     `yaml:"weight"`
	Path        string   `yaml:"path"`
	MaskPub     string   `yaml:"mask_pub"` // hex, optional
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
	if err := ValidIFName(c.Listen.Bind); err != nil {
		return nil, err
	}
	if err := ValidIFName(c.Listen.Uplink); err != nil {
		return nil, err
	}
	if err := ValidGatewayMode(c.Listen.Gateway); err != nil {
		return nil, err
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
			o.Inflight = 8
		}
		if o.Type == "" {
			return nil, fmt.Errorf("outbound %q: type is required", o.Name)
		}
		if o.URL == "" {
			return nil, fmt.Errorf("outbound %q: url is required", o.Name)
		}
	}
	if err := PinEdgeGET(&c, c.EdgeURL); err != nil {
		return nil, err
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
// Both the YAML and the command parser end here, so the defaults are the
// same whichever format the file uses. It also rejects a bad edge preset
// or an unknown inner cipher name.
func finishServer(c *ServerConfig) (*ServerConfig, error) {
	if c.Listen == "" {
		c.Listen = ":443"
	}
	if c.GetPath == "" {
		c.GetPath = DefaultGETPath
	}
	if c.WSPath == "" {
		c.WSPath = DefaultWSPath
	}
	if c.H2Path == "" {
		c.H2Path = DefaultH2Path
	}
	if c.H3Path == "" {
		c.H3Path = DefaultH2Path
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
	if _, err := c.EdgeByte(); err != nil {
		return nil, err
	}
	if _, err := c.InnerIDs(); err != nil {
		return nil, err
	}
	if _, err := c.CCIDs(); err != nil {
		return nil, err
	}
	// At most one standby may auto-promote. Two auto-promote standbys share
	// the controller seed, so on a controller outage both promote at the same
	// epoch with the same key; takeover fencing is "newer epoch wins" and
	// cannot break an equal-epoch tie, leaving two live controllers of the
	// same generation (split brain). Make the operator pick one; the rest are
	// manual-takeover standbys (loft-node -promote).
	auto := 0
	for _, e := range c.Relays {
		if e.AutoPromote {
			auto++
		}
	}
	if auto > 1 {
		return nil, fmt.Errorf("auto_promote is set on %d relays; at most one may auto-promote, or two standbys would split-brain on a controller outage", auto)
	}
	return c, nil
}

// InnerAsk is the Hello data-cipher byte. Empty and chacha omit the byte.
func (c *ClientConfig) InnerAsk() (set bool, id byte, err error) {
	if c == nil {
		return false, wire.InnerChaCha, nil
	}
	s := strings.TrimSpace(c.Inner)
	if s == "" {
		return false, wire.InnerChaCha, nil
	}
	id, err = wire.ParseInnerName(s)
	if err != nil {
		return false, 0, err
	}
	if id == wire.InnerChaCha {
		return false, id, nil
	}
	return true, id, nil
}

// CCAsk is the Hello congestion control ask and, for fixed, the two rates
// in kbit/s (up = what the client sends, down = what it receives).
func (c *ClientConfig) CCAsk() (id byte, up, down uint32, err error) {
	if c == nil {
		return wire.CCDefault, 0, 0, nil
	}
	id, err = wire.ParseCCName(c.CC)
	if err != nil || id != wire.CCFixed {
		return id, 0, 0, err
	}
	return id, c.CCUpMbps * 1000, c.CCDownMbps * 1000, nil
}

// CCIDs is the node's congestion control allow-list. Empty (or off) means
// every session stays on the system default.
func (c *ServerConfig) CCIDs() ([]byte, error) {
	if c == nil {
		return nil, nil
	}
	var out []byte
	seen := map[byte]bool{}
	for _, w := range c.CC {
		for _, part := range strings.Split(w, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			id, err := wire.ParseCCName(part)
			if err != nil {
				return nil, err
			}
			if id == wire.CCDefault || seen[id] {
				continue
			}
			seen[id] = true
			out = append(out, id)
		}
	}
	return out, nil
}

// InnerIDs is the node's extra allow-list (clear and/or aes). ChaCha is always
// allowed and is not returned. Empty means ChaCha only.
func (c *ServerConfig) InnerIDs() ([]byte, error) {
	if c == nil || len(c.Inner) == 0 {
		return nil, nil
	}
	var out []byte
	seen := map[byte]bool{}
	for _, w := range c.Inner {
		for _, part := range strings.Split(w, ",") {
			part = strings.TrimSpace(part)
			if part == "" {
				continue
			}
			id, err := wire.ParseInnerName(part)
			if err != nil {
				return nil, err
			}
			if id == wire.InnerChaCha || seen[id] {
				continue
			}
			seen[id] = true
			out = append(out, id)
		}
	}
	return out, nil
}

// EdgeByte is the Status edge preset. An empty edge with cdn yandex is tight.
// An explicit plain stays plain. Unknown names are an error.
func (c *ServerConfig) EdgeByte() (byte, error) {
	if c == nil {
		return wire.EdgePlain, nil
	}
	switch strings.ToLower(strings.TrimSpace(c.Edge)) {
	case "":
		if strings.EqualFold(strings.TrimSpace(c.CDN), "yandex") {
			return wire.EdgeTight, nil
		}
		return wire.EdgePlain, nil
	case "plain":
		return wire.EdgePlain, nil
	case "tight":
		return wire.EdgeTight, nil
	default:
		return 0, fmt.Errorf("edge %q: plain or tight", c.Edge)
	}
}

// IsRelay is role relay (TLS edge); otherwise this node is the controller.
func (c *ServerConfig) IsRelay() bool { return c.Role == "relay" || c.Role == "replica" }

// IsReplica is a standby controller: it runs as a relay and keeps a copy of
// the controller's state, so it can be promoted when the controller is gone.
func (c *ServerConfig) IsReplica() bool { return c.Role == "replica" }

// ControllerURLs is the controller trunk URL followed by any alternates
// (standby controllers), in the order a relay tries them.
func (c *ServerConfig) ControllerURLs() []string {
	out := []string{}
	if c.Controller != "" {
		out = append(out, c.Controller)
	}
	return append(out, c.ControllerAlt...)
}

// EnabledFronts is cfg.fronts, else outbound types, else GET.
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
		return wire.DefaultFronts()
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

// EnabledFronts is cfg.fronts, else GET.
func (c *ServerConfig) EnabledFronts() []string {
	if len(c.Fronts) == 0 {
		return wire.DefaultFronts()
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

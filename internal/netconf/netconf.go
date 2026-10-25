// Package netconf is Loft overlay 1.0 network.conf: members, overlay IPs,
// leases, Cisco-style ACL, IPAM, DNS policy, session timers, managed routes
// and VLANs. YAML (network.yaml) is a legacy fallback.
package netconf

import (
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/a3app/loft/internal/startup"
)

// Peers is how members of one network reach each other.
const (
	PeersHub  = "hub"  // only via the node / gateway; Wi-Fi AP isolation
	PeersMesh = "mesh" // members may talk to each other
	PeersVLAN = "vlan" // mesh inside a VLAN, isolated across VLANs unless rules allow
)

// Network is one overlay, like a ZeroTier network or a managed switch.
type Network struct {
	ID         string        `yaml:"id" json:"id"`
	Name       string        `yaml:"name" json:"name"`
	CIDR       string        `yaml:"cidr" json:"cidr"`
	PoolFrom   string        `yaml:"pool_from,omitempty" json:"pool_from,omitempty"`
	PoolTo     string        `yaml:"pool_to,omitempty" json:"pool_to,omitempty"`
	Gateway    string        `yaml:"gateway" json:"gateway"`
	MTU        int           `yaml:"mtu" json:"mtu"`
	Peers      string        `yaml:"peers" json:"peers"`                 // hub | mesh | vlan
	P2P        *bool         `yaml:"p2p,omitempty" json:"p2p,omitempty"` // nil: on if mesh/vlan
	Listen     []Listen      `yaml:"listen" json:"listen"`
	Routes     []Route       `yaml:"routes" json:"routes"`
	VLANs      []VLAN        `yaml:"vlans" json:"vlans"`
	Rules      []Rule        `yaml:"rules" json:"rules"`
	ACLDefault string        `yaml:"acl_default,omitempty" json:"acl_default,omitempty"` // accept | drop; empty = Peers
	Members    []Member      `yaml:"members" json:"members"`
	DNS        DNS           `yaml:"dns,omitempty" json:"dns,omitempty"`
	Session    SessionPolicy `yaml:"session,omitempty" json:"session,omitempty"`
	Enabled    bool          `yaml:"enabled" json:"enabled"`
}

// DNS is overlay resolver policy. Servers are pushed to clients in Status.
// Upstream is how this node answers queries to the gateway on port 53.
// Empty servers: the gateway address. Empty upstream: DoH to 1.1.1.1.
type DNS struct {
	Enabled  *bool    `yaml:"enabled,omitempty" json:"enabled,omitempty"` // nil = on
	Servers  []string `yaml:"servers,omitempty" json:"servers,omitempty"`
	Upstream []string `yaml:"upstream,omitempty" json:"upstream,omitempty"`
}

// Listen is one accept socket. Domain is SNI / CDN hostname, not a bind.
type Listen struct {
	IP     string `yaml:"ip" json:"ip"`
	Port   int    `yaml:"port" json:"port"`
	Domain string `yaml:"domain" json:"domain"`
	Front  string `yaml:"front" json:"front"` // get | ws | quic | h2 | h3
	SNI    string `yaml:"sni" json:"sni"`
}

// Route is a managed route pushed to members, like ZeroTier managed routes.
// Next hop is Via (overlay IP), Member (id), or Tag (first live/sticky member).
type Route struct {
	Dst    string `yaml:"dst" json:"dst"`
	Via    string `yaml:"via,omitempty" json:"via,omitempty"`
	Member string `yaml:"member,omitempty" json:"member,omitempty"`
	Tag    string `yaml:"tag,omitempty" json:"tag,omitempty"`
	Note   string `yaml:"note,omitempty" json:"note,omitempty"`
}

// VLAN is a segment (a bridge) inside the overlay. Membership comes from the
// member's credentials, not from 802.1Q on the wire. Empty CIDR is L2-only
// isolation inside the default overlay; a CIDR is its own L3 (gateway, pool, DNS).
type VLAN struct {
	ID       int    `yaml:"id" json:"id"`
	Name     string `yaml:"name" json:"name"`
	CIDR     string `yaml:"cidr,omitempty" json:"cidr,omitempty"`
	Gateway  string `yaml:"gateway,omitempty" json:"gateway,omitempty"`
	PoolFrom string `yaml:"pool_from,omitempty" json:"pool_from,omitempty"`
	PoolTo   string `yaml:"pool_to,omitempty" json:"pool_to,omitempty"`
	DNS      DNS    `yaml:"dns,omitempty" json:"dns,omitempty"`
	Peers    string `yaml:"peers,omitempty" json:"peers,omitempty"` // override: hub | mesh
}

// Member is a device identity. Auth is a key, not a password.
type Member struct {
	ID         string        `yaml:"id" json:"id"`
	Name       string        `yaml:"name" json:"name"`
	PubKey     string        `yaml:"pubkey,omitempty" json:"pubkey,omitempty"`
	KeySHA256  string        `yaml:"key_sha256,omitempty" json:"key_sha256,omitempty"`
	IP         string        `yaml:"ip,omitempty" json:"ip,omitempty"`
	VLAN       int           `yaml:"vlan,omitempty" json:"vlan,omitempty"`
	Tags       []string      `yaml:"tags,omitempty" json:"tags,omitempty"`
	Authorized bool          `yaml:"authorized" json:"authorized"`
	Multi      bool          `yaml:"multi,omitempty" json:"multi,omitempty"`
	MultiLimit int           `yaml:"multi_limit,omitempty" json:"multi_limit,omitempty"`
	Session    SessionPolicy `yaml:"session,omitempty" json:"session,omitempty"`
}

// P2PEnabled is the explicit p2p flag, or true when Peers is mesh or vlan.
func (n *Network) P2PEnabled() bool {
	if n.P2P != nil {
		return *n.P2P
	}
	return n.Peers == PeersMesh || n.Peers == PeersVLAN
}

// PeersMode is Status.Peers: 0 hub, 1 mesh, 2 vlan.
func (n *Network) PeersMode() byte {
	switch n.Peers {
	case PeersMesh:
		return 1
	case PeersVLAN:
		return 2
	default:
		return 0
	}
}

// StickyIP is the member's reserved overlay address; empty means pool assignment.
func (m Member) StickyIP() string { return m.IP }

// MaxSessions is 1 unless Multi; Multi with MultiLimit 0 defaults to 8.
func (m Member) MaxSessions() int {
	if !m.Multi {
		return 1
	}
	if m.MultiLimit > 0 {
		return m.MultiLimit
	}
	return 8
}

// Rule is one switch ACL line. First match wins. Empty match fields are wildcards.
type Rule struct {
	Action  string `yaml:"action" json:"action"` // accept | drop
	SrcVLAN *int   `yaml:"src_vlan,omitempty" json:"src_vlan,omitempty"`
	DstVLAN *int   `yaml:"dst_vlan,omitempty" json:"dst_vlan,omitempty"`
	Src     string `yaml:"src,omitempty" json:"src,omitempty"` // CIDR
	Dst     string `yaml:"dst,omitempty" json:"dst,omitempty"`
	SrcTag  string `yaml:"src_tag,omitempty" json:"src_tag,omitempty"` // member tag / group
	DstTag  string `yaml:"dst_tag,omitempty" json:"dst_tag,omitempty"`
	Proto   string `yaml:"proto,omitempty" json:"proto,omitempty"` // tcp | udp | icmp
	Dport   []int  `yaml:"dport,omitempty" json:"dport,omitempty"`
	Sport   []int  `yaml:"sport,omitempty" json:"sport,omitempty"`
}

// Packet is what the switch sees.
type Packet struct {
	SrcVLAN int
	DstVLAN int
	Src     net.IP
	Dst     net.IP
	SrcTags []string
	DstTags []string
	Proto   string
	Sport   int
	Dport   int
	ToGW    bool // destination is a segment gateway
	Routed  bool // destination matches ip route via a member
}

// Load reads one network.conf or legacy YAML path and Parse.
func Load(path string) (*Network, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return Parse(b)
}

// LoadDir reads network.conf, then legacy network.yaml.
func LoadDir(dir string) (*Network, error) {
	var last error
	for _, name := range []string{"network.conf", "network.yaml"} {
		n, err := Load(filepath.Join(dir, name))
		if err == nil {
			return n, nil
		}
		if !os.IsNotExist(err) {
			return nil, err
		}
		last = err
	}
	if last == nil {
		last = fmt.Errorf("network.conf: not found")
	}
	return nil, last
}

// Parse accepts switch-style commands, or YAML when LooksYAML.
func Parse(b []byte) (*Network, error) {
	body := string(b)
	if startup.LooksYAML(body) {
		var n Network
		if err := yaml.Unmarshal(b, &n); err != nil {
			return nil, err
		}
		if err := n.Validate(); err != nil {
			return nil, err
		}
		return &n, nil
	}
	return ParseCommands(body)
}

// Validate fills defaults (peers hub, MTU 1280, canonical pool, listen fronts)
// and rejects a missing id/CIDR, bad peers, ports, routes, ACL actions, or DNS.
func (n *Network) Validate() error {
	if n.ID == "" {
		return fmt.Errorf("network id is required")
	}
	if n.CIDR == "" {
		return fmt.Errorf("network cidr is required")
	}
	_, ipnet, err := net.ParseCIDR(n.CIDR)
	if err != nil {
		return fmt.Errorf("cidr: %w", err)
	}
	if err := n.canonicalizePool(ipnet); err != nil {
		return err
	}
	switch n.Peers {
	case "", PeersHub, PeersMesh, PeersVLAN:
	default:
		return fmt.Errorf("peers: want hub, mesh or vlan")
	}
	if n.Peers == "" {
		n.Peers = PeersHub
	}
	if n.MTU <= 0 {
		n.MTU = 1280
	}
	for i, l := range n.Listen {
		if l.Port <= 0 || l.Port > 65535 {
			return fmt.Errorf("listen[%d]: bad port", i)
		}
		f, err := NormalizeFront(l.Front)
		if err != nil {
			return fmt.Errorf("listen[%d]: %w", i, err)
		}
		n.Listen[i].Front = f
	}
	for i, r := range n.Routes {
		if _, _, err := net.ParseCIDR(r.Dst); err != nil {
			return fmt.Errorf("routes[%d].dst: %w", i, err)
		}
		if r.Via != "" && net.ParseIP(r.Via) == nil {
			return fmt.Errorf("routes[%d].via: want an IP", i)
		}
		if r.Tag != "" {
			if !ValidTag(r.Tag) {
				return fmt.Errorf("routes[%d]: bad tag", i)
			}
			n.Routes[i].Tag = strings.ToLower(r.Tag)
		}
	}
	switch startup.Word(n.ACLDefault) {
	case "", "accept", "permit":
		n.ACLDefault = ""
	case "drop", "deny":
		n.ACLDefault = "drop"
	default:
		return fmt.Errorf("acl_default: want accept or drop")
	}
	for i, r := range n.Rules {
		switch r.Action {
		case "accept", "drop":
		default:
			return fmt.Errorf("rules[%d]: action want accept or drop", i)
		}
		if r.SrcTag != "" {
			if !ValidTag(r.SrcTag) {
				return fmt.Errorf("rules[%d]: bad src_tag", i)
			}
			r.SrcTag = strings.ToLower(r.SrcTag)
			n.Rules[i].SrcTag = r.SrcTag
		}
		if r.DstTag != "" {
			if !ValidTag(r.DstTag) {
				return fmt.Errorf("rules[%d]: bad dst_tag", i)
			}
			r.DstTag = strings.ToLower(r.DstTag)
			n.Rules[i].DstTag = r.DstTag
		}
	}
	if err := n.validateDNS(); err != nil {
		return err
	}
	return n.validateSegs()
}

// ValidTag is a member group name: letter, then letters, digits, _ or -.
func ValidTag(s string) bool {
	if s == "" || len(s) > 32 {
		return false
	}
	for i, r := range s {
		switch {
		case r >= 'A' && r <= 'Z', r >= 'a' && r <= 'z':
		case i > 0 && ((r >= '0' && r <= '9') || r == '_' || r == '-'):
		default:
			return false
		}
	}
	return true
}

// HasTag reports a case-insensitive match on m.Tags.
func (m Member) HasTag(tag string) bool {
	return hasTag(m.Tags, tag)
}

// RouteHop is the configured next hop overlay IP for r: Via, else the
// member's sticky IP, else the first sticky IP with Tag. Empty or a
// segment gateway means the node itself.
func (n *Network) RouteHop(r Route) string {
	if n == nil {
		return ""
	}
	if ip := net.ParseIP(strings.TrimSpace(r.Via)); ip != nil {
		s := ip.String()
		if n.IsGateway(s) {
			return ""
		}
		return s
	}
	if r.Member != "" {
		if m := n.MemberByID(r.Member); m != nil && m.IP != "" && !n.IsGateway(m.IP) {
			return m.IP
		}
	}
	if r.Tag != "" {
		for _, m := range n.Members {
			if m.HasTag(r.Tag) && m.IP != "" && !n.IsGateway(m.IP) {
				return m.IP
			}
		}
	}
	return ""
}

// RouteVia is the overlay IP of the member that owns dest, longest prefix.
// Empty hop or hop equal to the network gateway means the node itself.
func (n *Network) RouteVia(ip net.IP) string {
	if n == nil || ip == nil {
		return ""
	}
	best := -1
	via := ""
	for _, r := range n.Routes {
		hop := n.RouteHop(r)
		if hop == "" {
			continue
		}
		_, cidr, err := net.ParseCIDR(r.Dst)
		if err != nil || !cidr.Contains(ip) {
			continue
		}
		ones, _ := cidr.Mask.Size()
		if ones >= best {
			best = ones
			via = hop
		}
	}
	return via
}

// DNSOn is the DNS.Enabled flag; nil means on.
func (n *Network) DNSOn() bool {
	if n.DNS.Enabled != nil {
		return *n.DNS.Enabled
	}
	return true
}

// DNSServers is what Status carries. Empty when DNS is off.
func (n *Network) DNSServers() []string {
	if !n.DNSOn() {
		return nil
	}
	var out []string
	for _, s := range n.DNS.Servers {
		ip := net.ParseIP(strings.TrimSpace(s))
		if ip != nil {
			out = append(out, ip.String())
		}
	}
	if len(out) == 0 && n.Gateway != "" {
		return []string{n.Gateway}
	}
	return out
}

// DNSUpstream is how this node answers gateway:53; empty means DoH to 1.1.1.1.
func (n *Network) DNSUpstream() []string {
	if len(n.DNS.Upstream) > 0 {
		return append([]string(nil), n.DNS.Upstream...)
	}
	return []string{"https://1.1.1.1/dns-query"}
}

// validateDNS requires DNS.Servers to be IPs and DNS.Upstream to pass CheckUpstream.
func (n *Network) validateDNS() error {
	if err := n.DNS.check(); err != nil {
		return err
	}
	for _, v := range n.VLANs {
		if err := v.DNS.check(); err != nil {
			return fmt.Errorf("vlan %d: %w", v.ID, err)
		}
	}
	return nil
}

// NormalizeFront accepts protocol fronts. Empty listen front is get.
func NormalizeFront(s string) (string, error) {
	switch s {
	case "", "get":
		return "get", nil
	case "ws", "quic", "h2", "h3", "l3":
		return s, nil
	default:
		return "", fmt.Errorf("unknown front %q (want get, ws, h2, quic, h3 or l3)", s)
	}
}

// VLANByID points at n.VLANs[i] so config can mutate that entry; nil if unknown.
func (n *Network) VLANByID(id int) *VLAN {
	for i := range n.VLANs {
		if n.VLANs[i].ID == id {
			return &n.VLANs[i]
		}
	}
	return nil
}

// MemberByID points at n.Members[i]; nil if the id is unknown.
func (n *Network) MemberByID(id string) *Member {
	for i := range n.Members {
		if n.Members[i].ID == id {
			return &n.Members[i]
		}
	}
	return nil
}

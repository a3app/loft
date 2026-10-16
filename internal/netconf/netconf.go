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
	ID       string        `yaml:"id" json:"id"`
	Name     string        `yaml:"name" json:"name"`
	CIDR     string        `yaml:"cidr" json:"cidr"`
	PoolFrom string        `yaml:"pool_from,omitempty" json:"pool_from,omitempty"`
	PoolTo   string        `yaml:"pool_to,omitempty" json:"pool_to,omitempty"`
	Gateway  string        `yaml:"gateway" json:"gateway"`
	MTU      int           `yaml:"mtu" json:"mtu"`
	Peers    string        `yaml:"peers" json:"peers"`                 // hub | mesh | vlan
	P2P      *bool         `yaml:"p2p,omitempty" json:"p2p,omitempty"` // nil: on if mesh/vlan
	Listen   []Listen      `yaml:"listen" json:"listen"`
	Routes   []Route       `yaml:"routes" json:"routes"`
	VLANs    []VLAN        `yaml:"vlans" json:"vlans"`
	Rules    []Rule        `yaml:"rules" json:"rules"`
	Members  []Member      `yaml:"members" json:"members"`
	DNS      DNS           `yaml:"dns,omitempty" json:"dns,omitempty"`
	Session  SessionPolicy `yaml:"session,omitempty" json:"session,omitempty"`
	Enabled  bool          `yaml:"enabled" json:"enabled"`
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
type Route struct {
	Dst  string `yaml:"dst" json:"dst"`
	Via  string `yaml:"via" json:"via"`
	Note string `yaml:"note,omitempty" json:"note,omitempty"`
}

// VLAN is a broadcast domain inside the overlay. Membership comes from the
// member's credentials, not from 802.1Q on the wire.
type VLAN struct {
	ID    int    `yaml:"id" json:"id"`
	Name  string `yaml:"name" json:"name"`
	CIDR  string `yaml:"cidr,omitempty" json:"cidr,omitempty"`
	Peers string `yaml:"peers,omitempty" json:"peers,omitempty"` // override: hub | mesh
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
	Proto   string
	Sport   int
	Dport   int
	ToGW    bool // destination is the network gateway
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
	}
	for i, r := range n.Rules {
		switch r.Action {
		case "accept", "drop":
		default:
			return fmt.Errorf("rules[%d]: action want accept or drop", i)
		}
	}
	return n.validateDNS()
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
	for i, s := range n.DNS.Servers {
		if net.ParseIP(strings.TrimSpace(s)) == nil {
			return fmt.Errorf("dns.servers[%d]: want an IP address", i)
		}
	}
	for i, u := range n.DNS.Upstream {
		if err := CheckUpstream(u); err != nil {
			return fmt.Errorf("dns.upstream[%d]: %w", i, err)
		}
	}
	return nil
}

// NormalizeFront accepts protocol fronts. Empty listen front is get.
func NormalizeFront(s string) (string, error) {
	switch s {
	case "", "get":
		return "get", nil
	case "ws", "quic", "h2", "h3":
		return s, nil
	default:
		return "", fmt.Errorf("unknown front %q (want get, ws, h2, quic or h3)", s)
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

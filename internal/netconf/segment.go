package netconf

// Segment L3: each VLAN with a CIDR is a bridge (Keenetic-style). ID 0 is
// the untagged default (ipif overlay). Empty VLAN CIDR is L2-only inside
// the default overlay.

import (
	"fmt"
	"net"
	"strings"
)

// Seg is one overlay segment: name, L3 and isolation.
type Seg struct {
	ID       int
	Name     string
	CIDR     string
	Gateway  string
	PoolFrom string
	PoolTo   string
	DNS      DNS
	Peers    string
}

// Seg is L3 of vlan id. Unknown or 0 is the default overlay.
func (n *Network) Seg(id int) Seg {
	if n == nil {
		return Seg{}
	}
	if id != 0 {
		if v := n.VLANByID(id); v != nil {
			s := Seg{
				ID: v.ID, Name: v.Name, CIDR: v.CIDR, Gateway: v.Gateway,
				PoolFrom: v.PoolFrom, PoolTo: v.PoolTo, DNS: v.DNS, Peers: v.Peers,
			}
			if s.Name == "" {
				s.Name = fmt.Sprintf("VLAN %d", v.ID)
			}
			if s.CIDR == "" {
				s.CIDR, s.Gateway, s.PoolFrom, s.PoolTo = n.CIDR, n.Gateway, n.PoolFrom, n.PoolTo
				if s.DNS.Enabled == nil && len(s.DNS.Servers) == 0 && len(s.DNS.Upstream) == 0 {
					s.DNS = n.DNS
				}
			} else if s.Gateway == "" {
				s.Gateway = firstHost(s.CIDR)
			}
			if s.Peers == "" {
				s.Peers = n.Peers
			}
			return s
		}
	}
	name := n.Name
	if name == "" {
		name = n.ID
	}
	return Seg{ID: 0, Name: name, CIDR: n.CIDR, Gateway: n.Gateway, PoolFrom: n.PoolFrom, PoolTo: n.PoolTo, DNS: n.DNS, Peers: n.Peers}
}

// Segs is the default overlay then each VLAN, in config order.
func (n *Network) Segs() []Seg {
	if n == nil {
		return nil
	}
	out := []Seg{n.Seg(0)}
	for i := range n.VLANs {
		out = append(out, n.Seg(n.VLANs[i].ID))
	}
	return out
}

// ContainsIP reports whether ip is in the default overlay or a VLAN CIDR.
func (n *Network) ContainsIP(ip net.IP) bool {
	if n == nil || ip == nil {
		return false
	}
	for _, s := range n.Segs() {
		_, cidr, err := net.ParseCIDR(s.CIDR)
		if err == nil && cidr.Contains(ip) {
			return true
		}
	}
	return false
}

// IsGateway reports whether ip is a segment gateway.
func (n *Network) IsGateway(ip string) bool {
	p := net.ParseIP(ip)
	if n == nil || p == nil {
		return false
	}
	for _, s := range n.Segs() {
		if g := net.ParseIP(s.Gateway); g != nil && g.Equal(p) {
			return true
		}
	}
	return false
}

// Gateways are unique segment gateway addresses, default first.
func (n *Network) Gateways() []string {
	var out []string
	seen := map[string]bool{}
	for _, s := range n.Segs() {
		if s.Gateway != "" && !seen[s.Gateway] {
			seen[s.Gateway] = true
			out = append(out, s.Gateway)
		}
	}
	return out
}

// DNSOn is the segment DNS.Enabled flag; nil means on.
func (s Seg) DNSOn() bool {
	if s.DNS.Enabled != nil {
		return *s.DNS.Enabled
	}
	return true
}

// DNSServers is what Status carries for a member of this segment.
func (s Seg) DNSServers() []string {
	if !s.DNSOn() {
		return nil
	}
	var out []string
	for _, x := range s.DNS.Servers {
		ip := net.ParseIP(strings.TrimSpace(x))
		if ip != nil {
			out = append(out, ip.String())
		}
	}
	if len(out) == 0 && s.Gateway != "" {
		return []string{s.Gateway}
	}
	return out
}

// validateSegs checks the overlay and every VLAN CIDR: IPv4, at most a /16
// (the allocator scans addresses one by one), no two segments overlapping,
// pool bounds inside the CIDR, and the VLAN gateway inside its CIDR.
func (n *Network) validateSegs() error {
	type span struct {
		lo, hi uint32
		name   string
	}
	var spans []span
	add := func(cidr, name string) error {
		_, ipnet, err := parseCIDR4(cidr)
		if err != nil {
			return fmt.Errorf("%s cidr: %w", name, err)
		}
		ones, bits := ipnet.Mask.Size()
		if bits-ones > 16 {
			return fmt.Errorf("%s cidr too wide to scan", name)
		}
		lo, hi := cidrSpan(ipnet)
		for _, s := range spans {
			if lo <= s.hi && hi >= s.lo {
				return fmt.Errorf("%s %s overlaps %s", name, cidr, s.name)
			}
		}
		spans = append(spans, span{lo, hi, name + " " + cidr})
		return nil
	}
	if n.CIDR != "" {
		if err := add(n.CIDR, "overlay"); err != nil {
			return err
		}
	}
	for i := range n.VLANs {
		v := &n.VLANs[i]
		if v.CIDR == "" {
			continue
		}
		name := fmt.Sprintf("vlan %d", v.ID)
		if err := add(v.CIDR, name); err != nil {
			return err
		}
		_, ipnet, err := parseCIDR4(v.CIDR)
		if err != nil {
			return err
		}
		if err := canonicalizePool(ipnet, &v.PoolFrom, &v.PoolTo); err != nil {
			return fmt.Errorf("%s pool: %w", name, err)
		}
		if v.Gateway != "" {
			g := net.ParseIP(v.Gateway)
			if g == nil || g.To4() == nil || !ipnet.Contains(g) {
				return fmt.Errorf("%s gateway %s is outside %s", name, v.Gateway, v.CIDR)
			}
		}
	}
	return nil
}

// cidrSpan is the first and last IPv4 address of n as integers, for the
// overlap check. IPv6 is 0, 0.
func cidrSpan(n *net.IPNet) (lo, hi uint32) {
	ip := n.IP.To4()
	if ip == nil {
		return 0, 0
	}
	mask := net.IP(n.Mask).To4()
	if mask == nil {
		return 0, 0
	}
	base := uint32(ip[0])<<24 | uint32(ip[1])<<16 | uint32(ip[2])<<8 | uint32(ip[3])
	m := uint32(mask[0])<<24 | uint32(mask[1])<<16 | uint32(mask[2])<<8 | uint32(mask[3])
	return base & m, base | ^m
}

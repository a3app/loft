package netconf

// First-match ACL plus default Peers (hub / mesh / vlan) for overlay packets.

import (
	"net"
	"strings"
)

// Allow reports whether the packet may pass. Empty Rules still apply Peers.
func (n *Network) Allow(p Packet) bool {
	if p.SrcVLAN != 0 && n.VLANByID(p.SrcVLAN) == nil && len(n.VLANs) > 0 {
		return false
	}
	for _, r := range n.Rules {
		if matchRule(r, p) {
			return r.Action == "accept"
		}
	}
	return n.defaultAllow(p)
}

// defaultAllow is Peers when no ACL matches: hub = ToGW only, mesh = all,
// vlan = same SrcVLAN/DstVLAN or ToGW. A VLAN may override Peers.
// ACLDefault drop keeps ToGW (DNS / gateway) and drops everything else,
// including Routed. Routed is otherwise allowed (lab LAN behind a member).
func (n *Network) defaultAllow(p Packet) bool {
	if n.ACLDefault == "drop" {
		return p.ToGW
	}
	if p.Routed {
		return true
	}
	peers := n.Peers
	if p.SrcVLAN != 0 {
		if v := n.VLANByID(p.SrcVLAN); v != nil && v.Peers != "" {
			peers = v.Peers
		}
	}
	switch peers {
	case PeersMesh:
		return true
	case PeersVLAN:
		if p.ToGW {
			return true
		}
		return p.SrcVLAN != 0 && p.SrcVLAN == p.DstVLAN
	default: // hub
		return p.ToGW
	}
}

// matchRule is true when every set field on r matches p. Empty fields are wildcards.
func matchRule(r Rule, p Packet) bool {
	if r.SrcVLAN != nil && p.SrcVLAN != *r.SrcVLAN {
		return false
	}
	if r.DstVLAN != nil && p.DstVLAN != *r.DstVLAN {
		return false
	}
	if r.Src != "" && !inCIDR(r.Src, p.Src) {
		return false
	}
	if r.Dst != "" && !inCIDR(r.Dst, p.Dst) {
		return false
	}
	if r.SrcTag != "" && !hasTag(p.SrcTags, r.SrcTag) {
		return false
	}
	if r.DstTag != "" && !hasTag(p.DstTags, r.DstTag) {
		return false
	}
	if r.Proto != "" && r.Proto != p.Proto {
		return false
	}
	if len(r.Dport) > 0 && !hasPort(r.Dport, p.Dport) {
		return false
	}
	if len(r.Sport) > 0 && !hasPort(r.Sport, p.Sport) {
		return false
	}
	return true
}

// hasTag is a case-insensitive membership test.
func hasTag(tags []string, want string) bool {
	if want == "" {
		return false
	}
	for _, t := range tags {
		if strings.EqualFold(t, want) {
			return true
		}
	}
	return false
}

// inCIDR treats a non-CIDR string as a single IP.
func inCIDR(cidr string, ip net.IP) bool {
	if ip == nil {
		return false
	}
	_, n, err := net.ParseCIDR(cidr)
	if err != nil {
		return ip.Equal(net.ParseIP(cidr))
	}
	return n.Contains(ip)
}

// hasPort is true if p is listed (ACL eq/range).
func hasPort(list []int, p int) bool {
	for _, x := range list {
		if x == p {
			return true
		}
	}
	return false
}

package netconf

// Overlay IPv4 IPAM: CIDR scan, optional pool, last-octet shorthand on /24.

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"strconv"
	"strings"
)

// errPool is a full CIDR or pool. errWide refuses prefixes shorter than /16.
var (
	errPool = errors.New("pool exhausted")
	errWide = errors.New("cidr too wide to scan")
)

// NextIP picks the first free address in CIDR, skipping network, broadcast, gateway and member sticky IPs.
func (n *Network) NextIP() (string, error) {
	used := map[string]bool{}
	for _, m := range n.Members {
		if m.IP != "" {
			used[m.IP] = true
		}
	}
	return n.nextFree(used)
}

// NextIPIn is NextIP for a segment (0 = default overlay).
func (n *Network) NextIPIn(vlan int) (string, error) {
	used := map[string]bool{}
	for _, m := range n.Members {
		if m.IP != "" {
			used[m.IP] = true
		}
	}
	return n.nextFreeIn(vlan, used)
}

// firstHost is network+1 of cidr, or empty.
func firstHost(cidr string) string {
	ip, ipnet, err := parseCIDR4(cidr)
	if err != nil {
		return ""
	}
	cur := append(net.IP(nil), ip.Mask(ipnet.Mask)...)
	inc(cur)
	if !ipnet.Contains(cur) || isBroadcast(cur, ipnet) {
		return ""
	}
	return cur.String()
}

// parseCIDR4 parses CIDR and canonicalizes IPv4 to 4-byte form.
func parseCIDR4(cidr string) (net.IP, *net.IPNet, error) {
	ip, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		return nil, nil, err
	}
	if v4 := ip.To4(); v4 != nil {
		ip = v4
		ipnet = &net.IPNet{IP: v4.Mask(ipnet.Mask[len(ipnet.Mask)-4:]), Mask: ipnet.Mask[len(ipnet.Mask)-4:]}
	}
	return ip, ipnet, nil
}

// inc adds one to ip in place, wrapping from the last octet.
func inc(ip net.IP) {
	for i := len(ip) - 1; i >= 0; i-- {
		ip[i]++
		if ip[i] != 0 {
			return
		}
	}
}

// canonicalizePool expands last-octet pool bounds to full IPv4 strings on n.
func (n *Network) canonicalizePool(ipnet *net.IPNet) error {
	return canonicalizePool(ipnet, &n.PoolFrom, &n.PoolTo)
}

func canonicalizePool(ipnet *net.IPNet, poolFrom, poolTo *string) error {
	from, to, err := poolBounds(*poolFrom, *poolTo, ipnet)
	if err != nil {
		return err
	}
	if from != nil {
		*poolFrom = from.String()
	}
	if to != nil {
		*poolTo = to.String()
	}
	return nil
}

// poolBounds expands PoolFrom/PoolTo. Both must be set or both empty; from ≤ to.
func (n *Network) poolBounds(ipnet *net.IPNet) (from, to net.IP, err error) {
	return poolBounds(n.PoolFrom, n.PoolTo, ipnet)
}

func poolBounds(poolFrom, poolTo string, ipnet *net.IPNet) (from, to net.IP, err error) {
	from, err = expandPoolIP(poolFrom, ipnet)
	if err != nil {
		return nil, nil, fmt.Errorf("pool_from: %w", err)
	}
	to, err = expandPoolIP(poolTo, ipnet)
	if err != nil {
		return nil, nil, fmt.Errorf("pool_to: %w", err)
	}
	if (from == nil) != (to == nil) {
		return nil, nil, fmt.Errorf("pool_from and pool_to must both be set, or both empty")
	}
	if from != nil && bytes.Compare(from, to) > 0 {
		return nil, nil, fmt.Errorf("pool_from is after pool_to")
	}
	return from, to, nil
}

// expandPoolIP accepts a full IPv4 address, or a last octet (0–255) when CIDR is /24.
func expandPoolIP(s string, ipnet *net.IPNet) (net.IP, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	if ip := net.ParseIP(s); ip != nil {
		v4 := ip.To4()
		if v4 == nil {
			return nil, fmt.Errorf("must be IPv4")
		}
		if !ipnet.Contains(v4) {
			return nil, fmt.Errorf("%s is outside the overlay CIDR", v4)
		}
		return append(net.IP(nil), v4...), nil
	}
	ones, bits := ipnet.Mask.Size()
	if ones != 24 || bits != 32 {
		return nil, fmt.Errorf("%q: write a full address", s)
	}
	n, err := strconv.Atoi(s)
	if err != nil || n < 0 || n > 255 {
		return nil, fmt.Errorf("%q is not an address", s)
	}
	base := ipnet.IP.To4()
	if base == nil {
		return nil, fmt.Errorf("overlay CIDR is not IPv4")
	}
	out := append(net.IP(nil), base...)
	out[3] = byte(n)
	if !ipnet.Contains(out) {
		return nil, fmt.Errorf("%s is outside the overlay CIDR", out)
	}
	return out, nil
}

// isBroadcast is the directed broadcast of n (all-host-bits-one).
func isBroadcast(ip net.IP, n *net.IPNet) bool {
	bcast := make(net.IP, len(ip))
	copy(bcast, ip)
	for i := range bcast {
		bcast[i] = ip[i] | ^n.Mask[i]
	}
	return ip.Equal(bcast)
}

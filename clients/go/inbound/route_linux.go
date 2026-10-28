//go:build linux

package inbound

// route_linux.go installs IPv4 split default (0/1 + 128/1) and optional kill-switch.
//
// "Split default": instead of replacing the system's default route (0.0.0.0/0),
// we add two routes, 0.0.0.0/1 and 128.0.0.0/1, through the TUN. Together they
// cover every IPv4 address, and because they are more specific than /0 the
// kernel prefers them, while the original default route stays in place and
// comes back by itself when we remove ours.
//
// All changes go through ifcfg (netlink by default, see internal/ifcfg), so
// no "ip" program is needed.

import (
	"net"

	"github.com/a3app/loft/internal/ifcfg"
)

// enableKillSwitch adds split default then blackholes 0/0 on undo.
func enableKillSwitch(tun string, exclude []string) (func(), error) {
	undo, err := addSplitDefault(tun, exclude)
	if err != nil {
		return nil, err
	}
	return func() {
		undo()
		// With the tunnel gone, replace the default route by a blackhole:
		// traffic is dropped instead of leaking out unprotected.
		_ = ifcfg.Default().RouteReplace(ifcfg.Route{Dst: cidr("0.0.0.0/0"), Type: ifcfg.Blackhole})
	}, nil
}

// addSplitDefault pins exclude hosts, then 0/1 and 128/1 on tun. ::/0 waits for overlay IPv6.
func addSplitDefault(tun string, exclude []string) (func(), error) {
	b := ifcfg.Default()
	// undo collects the reverse of every change that worked, run newest
	// first, so the routing table ends up exactly as we found it.
	var undo []func()
	rollback := func() {
		for i := len(undo) - 1; i >= 0; i-- {
			undo[i]()
		}
	}

	// Exclude hosts (the node's address) would get a route via the old
	// default gateway so the tunnel's own packets do not loop into the
	// TUN. gateway() is empty on Linux, because there loft keeps its own
	// sockets off the TUN by binding them to the uplink interface (see
	// internal/protect). The old "ip route add HOST via" with an empty
	// gateway always failed, so skipping it keeps the same behaviour.
	if gw := net.ParseIP(gateway()); gw != nil {
		for _, host := range exclude {
			dst := hostNet(host)
			if dst == nil {
				continue
			}
			r := ifcfg.Route{Dst: dst, Gw: gw}
			if err := b.RouteAdd(r); err == nil {
				undo = append(undo, func() { _ = b.RouteDel(ifcfg.Route{Dst: r.Dst}) })
			}
		}
	}

	// IPv4 split default.
	for _, half := range []string{"0.0.0.0/1", "128.0.0.0/1"} {
		r := ifcfg.Route{Dst: cidr(half), Dev: tun}
		if err := b.RouteAdd(r); err != nil {
			rollback()
			return nil, err
		}
		undo = append(undo, func() { _ = b.RouteDel(ifcfg.Route{Dst: r.Dst}) })
	}

	// 2000::/3 (all public IPv6) unreachable, so Happy Eyeballs gives up on
	// IPv6 at once and uses IPv4 through the TUN. Overlay IPv6 later may
	// replace this with ::/0. Optional: a host without IPv6 refuses it.
	v6 := ifcfg.Route{Dst: cidr("2000::/3"), Type: ifcfg.Unreachable}
	if b.RouteReplace(v6) == nil {
		undo = append(undo, func() { _ = b.RouteDel(v6) })
	}
	return rollback, nil
}

// gateway is unused on Linux (exclude hosts are added without via).
func gateway() string { return "" }

// cidr parses a constant prefix written in this file; a typo is a bug, so
// it panics instead of returning an error.
func cidr(s string) *net.IPNet {
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		panic("inbound: bad CIDR " + s)
	}
	return n
}

// hostNet turns "1.2.3.4" or "1.2.3.4/32" into a network; nil if invalid.
func hostNet(s string) *net.IPNet {
	if _, n, err := net.ParseCIDR(s); err == nil {
		return n
	}
	ip := net.ParseIP(s)
	if ip == nil {
		return nil
	}
	bits := 128
	if ip.To4() != nil {
		ip, bits = ip.To4(), 32
	}
	return &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)}
}

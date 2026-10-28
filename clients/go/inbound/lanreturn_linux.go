//go:build linux

package inbound

// lanreturn_linux.go keeps answers to LAN hosts on their way back to the
// router when loft runs in a router's container as a LAN gateway
// (listen.gateway: nat or route).
//
// The problem it solves: inside the container loft installs the split
// default (0.0.0.0/1 and 128.0.0.0/1 into the TUN). loft's own stack answers
// a LAN host, say 192.168.88.10, by writing the reply into the TUN; the
// kernel then looks for a route to 192.168.88.10, finds the split default
// (more specific than the real default route to the router) and sends the
// reply straight back into the TUN. loft sees its own reply as a new packet
// and the connection dies with a reset.
//
// The fix is two policy rules that both use table lanReturnTable, which
// holds a single default route to the router:
//   - "whatever comes out of the TUN": the answers loft's stack writes for
//     LAN hosts going through the gateway;
//   - "whatever is sent from the container's own veth address": the
//     answers of the container's own services (SOCKS :1080, the LAN page
//     :8090) to a LAN host that talks to them directly. Those leave a
//     local socket, not the TUN, so the first rule does not see them, and
//     the split default would push them into the TUN as well.
// So answers for LAN hosts go back to the router, which knows the LAN.
// Local addresses still win, because the kernel checks its "local" table
// before any rule of ours. loft's own front sockets are pinned to the veth
// (-if) and would leave that way anyway.
//
// In "nat" mode the router rewrites the gateway traffic of LAN hosts to its
// own address, which the container reaches directly, so the TUN rule is
// not needed. The second rule still is: hosts that the router does not
// rewrite (another VLAN, the router's VPN clients, LAN hosts outside the
// loft-lan list, a port forward) talk to SOCKS and the LAN page with their
// own addresses, and without the rule those answers vanish into the TUN.

import (
	"fmt"
	"net"

	"github.com/a3app/loft/internal/ifcfg"
)

// lanReturnTable is the routing table of the route mode, and the priority
// of its first rule (the second gets the next number). Below 256 so that
// busybox "ip" (LOFT_NETCFG=ip) accepts it too, and far from the numbers
// 1..10 guides like to use by hand.
const lanReturnTable = 176

// EnableLANReturn installs the rule and the table for the route mode on the
// TUN named tun and returns the undo. The next hop is the current IPv4
// default route of the container (the router's side of the veth).
//
// Only for a dedicated gateway (a router's container): on a general Linux
// host with IP forwarding on, the rule would let overlay members route
// through this machine. That is why it runs only with listen.gateway: route.
//
// mode is listen.gateway: "route" installs both rules, "nat" only the one
// for the container's own address.
func EnableLANReturn(tun, mode string) (func(), error) {
	gw, dev, err := ifcfg.DefaultGatewayV4()
	if err != nil {
		return nil, fmt.Errorf("lan return: %w", err)
	}
	b := ifcfg.Default()
	route := ifcfg.Route{
		Dst:   &net.IPNet{IP: net.IPv4zero.To4(), Mask: net.CIDRMask(0, 32)},
		Gw:    gw,
		Dev:   dev,
		Table: lanReturnTable,
	}
	var rules []ifcfg.Rule
	if mode == "route" {
		rules = append(rules, ifcfg.Rule{IIF: tun, Table: lanReturnTable, Priority: lanReturnTable})
	}
	if own := firstIPv4(dev); own != nil {
		rules = append(rules, ifcfg.Rule{
			Src:   &net.IPNet{IP: own, Mask: net.CIDRMask(32, 32)},
			Table: lanReturnTable, Priority: lanReturnTable + 1,
		})
	}
	if len(rules) == 0 {
		return nil, fmt.Errorf("lan return: no IPv4 address on %s", dev)
	}
	// Replace, not add: after a crash the table may still hold our route.
	if err := b.RouteReplace(route); err != nil {
		return nil, fmt.Errorf("lan return: %w", err)
	}
	undo := func(added []ifcfg.Rule) {
		for i := len(added) - 1; i >= 0; i-- {
			_ = b.RuleDel(added[i])
		}
		_ = b.RouteDel(route)
	}
	for i, r := range rules {
		// A rule left over from a crash would make the add fail; drop it.
		_ = b.RuleDel(r)
		if err := b.RuleAdd(r); err != nil {
			undo(rules[:i])
			return nil, fmt.Errorf("lan return: %w", err)
		}
	}
	return func() { undo(rules) }, nil
}

// firstIPv4 is the first IPv4 address on interface name, or nil.
func firstIPv4(name string) net.IP {
	ifi, err := net.InterfaceByName(name)
	if err != nil {
		return nil
	}
	addrs, err := ifi.Addrs()
	if err != nil {
		return nil
	}
	for _, a := range addrs {
		if n, ok := a.(*net.IPNet); ok {
			if v4 := n.IP.To4(); v4 != nil {
				return v4
			}
		}
	}
	return nil
}

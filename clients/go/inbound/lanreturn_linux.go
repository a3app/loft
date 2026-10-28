//go:build linux

package inbound

// lanreturn_linux.go is the "route" mode of the LAN gateway (listen.gateway:
// route), used when loft runs in a router's container and LAN hosts send
// their traffic through it with their real addresses.
//
// The problem it solves: inside the container loft installs the split
// default (0.0.0.0/1 and 128.0.0.0/1 into the TUN). loft's own stack answers
// a LAN host, say 192.168.88.10, by writing the reply into the TUN; the
// kernel then looks for a route to 192.168.88.10, finds the split default
// (more specific than the real default route to the router) and sends the
// reply straight back into the TUN. loft sees its own reply as a new packet
// and the connection dies with a reset.
//
// The fix is one policy rule: "whatever comes out of the TUN uses table
// lanReturnTable", and that table holds a single default route to the
// router. So answers for LAN hosts go back to the router, which knows the
// LAN. Local addresses still win, because the kernel checks its "local"
// table before any rule of ours.
//
// In "nat" mode (the default) none of this is needed: the router rewrites
// LAN sources to its own address, and the container already has a route
// to that address.

import (
	"fmt"
	"net"

	"github.com/a3app/loft/internal/ifcfg"
)

// lanReturnTable is the routing table (and the rule priority) of the route
// mode. Below 256 so that busybox "ip" (LOFT_NETCFG=ip) accepts it too, and
// far from the numbers 1..10 guides like to use by hand.
const lanReturnTable = 176

// EnableLANReturn installs the rule and the table for the route mode on the
// TUN named tun and returns the undo. The next hop is the current IPv4
// default route of the container (the router's side of the veth).
//
// Only for a dedicated gateway (a router's container): on a general Linux
// host with IP forwarding on, the rule would let overlay members route
// through this machine. That is why it runs only with listen.gateway: route.
func EnableLANReturn(tun string) (func(), error) {
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
	rule := ifcfg.Rule{IIF: tun, Table: lanReturnTable, Priority: lanReturnTable}
	// Replace, not add: after a crash the table may still hold our route.
	if err := b.RouteReplace(route); err != nil {
		return nil, fmt.Errorf("lan return: %w", err)
	}
	// A rule left over from a crash would make the add fail; drop it first.
	_ = b.RuleDel(rule)
	if err := b.RuleAdd(rule); err != nil {
		_ = b.RouteDel(route)
		return nil, fmt.Errorf("lan return: %w", err)
	}
	return func() {
		_ = b.RuleDel(rule)
		_ = b.RouteDel(route)
	}, nil
}

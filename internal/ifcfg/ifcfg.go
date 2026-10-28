// Package ifcfg sets addresses and routes on a Linux network interface.
//
// loft needs a handful of such changes when it brings up a TUN device: put
// the overlay address on it, switch it on, and point routes at it. For years
// this was done by running the "ip" program (iproute2). That breaks wherever
// "ip" is missing: a container image built FROM scratch (our MikroTik image),
// distroless images, stripped-down systems. So the default way now talks to
// the kernel directly over netlink, the same channel "ip" itself uses.
//
// There are two ways ("backends") behind one interface:
//
//   - netlink (default): no outside programs, see netlink_linux.go.
//   - ip: runs the "ip" program, exactly the commands loft used before.
//     It stays as a deliberate alternative for developers: handy to compare
//     against, and it works with busybox "ip" as well as with iproute2.
//
// The backend is picked once by the LOFT_NETCFG environment variable:
// unset or "netlink" means netlink, "ip" means the ip program. There is no
// automatic fallback from one to the other on purpose: a silent fallback
// would hide a netlink bug behind a working "ip", and nobody would notice.
package ifcfg

import (
	"fmt"
	"net"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
)

// RouteType says what the kernel does with packets that match a route.
type RouteType int

const (
	// Unicast is a normal route: send matching packets out of an interface
	// (Route.Dev) and/or to a next hop (Route.Gw).
	Unicast RouteType = iota
	// Blackhole silently drops matching packets. loft's kill switch uses
	// it so that nothing leaks past a dead tunnel.
	Blackhole
	// Unreachable drops matching packets and tells the sender "network
	// unreachable" at once. loft uses it for public IPv6, so apps give up
	// on IPv6 quickly and use IPv4 through the tunnel instead of waiting.
	Unreachable
)

// Route is one entry of the main routing table.
type Route struct {
	// Dst is the destination network, e.g. 0.0.0.0/1. A prefix length of
	// 0 means the default route. Its address family (IPv4 or IPv6) picks
	// the family of the whole route.
	Dst *net.IPNet
	// Dev is the interface name, e.g. "tun0". Optional for deletes and for
	// Blackhole/Unreachable routes.
	Dev string
	// Gw is the next hop (the "via" address). Optional.
	Gw net.IP
	// Type is Unicast unless set.
	Type RouteType
}

// Backend makes the changes. All methods behave like the matching "ip"
// command: an add of something that already exists fails (EEXIST), a delete
// of something that is not there fails (ESRCH / EADDRNOTAVAIL), and callers
// decide whether that matters.
type Backend interface {
	// AddrAdd puts address a (with its prefix length) on interface dev,
	// like "ip addr add 10.10.0.10/24 dev tun0". The kernel then also adds
	// the route to the address's subnet by itself.
	AddrAdd(dev string, a *net.IPNet) error
	// AddrDel removes that address again, like "ip addr del".
	AddrDel(dev string, a *net.IPNet) error
	// LinkUp switches the interface on and sets its MTU (skipped when
	// mtu <= 0), like "ip link set dev tun0 up mtu 1280".
	LinkUp(dev string, mtu int) error
	// RouteAdd adds a route and fails if the same one exists ("ip route add").
	RouteAdd(r Route) error
	// RouteReplace adds the route or overwrites the one with the same
	// destination ("ip route replace").
	RouteReplace(r Route) error
	// RouteDel deletes a route ("ip route del").
	RouteDel(r Route) error
	// Name is "netlink" or "ip", for logs.
	Name() string
}

var (
	defaultOnce    sync.Once
	defaultBackend Backend
)

// Default returns the backend chosen by LOFT_NETCFG (read once per process).
func Default() Backend {
	defaultOnce.Do(func() {
		switch v := strings.TrimSpace(os.Getenv("LOFT_NETCFG")); v {
		case "", "netlink":
			defaultBackend = Netlink()
		case "ip":
			defaultBackend = IPTool()
		default:
			// A typo should not leave the tunnel without addresses, so
			// carry on with the default, but say so loudly.
			fmt.Fprintf(os.Stderr, "ifcfg: unknown LOFT_NETCFG=%q, using netlink\n", v)
			defaultBackend = Netlink()
		}
	})
	return defaultBackend
}

// IPTool returns the backend that runs the "ip" program found in PATH.
func IPTool() Backend { return ipTool{} }

// ipTool runs "ip" (iproute2 or busybox). Every method builds exactly the
// command line loft used before netlink, so switching LOFT_NETCFG=ip gives
// the old behaviour back.
type ipTool struct{}

func (ipTool) Name() string { return "ip" }

func (ipTool) AddrAdd(dev string, a *net.IPNet) error {
	return runIP(familyFlag(a.IP), "addr", "add", cidrString(a), "dev", dev)
}

func (ipTool) AddrDel(dev string, a *net.IPNet) error {
	return runIP(familyFlag(a.IP), "addr", "del", cidrString(a), "dev", dev)
}

func (ipTool) LinkUp(dev string, mtu int) error {
	args := []string{"link", "set", "dev", dev, "up"}
	if mtu > 0 {
		args = append(args, "mtu", strconv.Itoa(mtu))
	}
	return runIP("", args...)
}

func (ipTool) RouteAdd(r Route) error { return runIP(familyFlag(r.Dst.IP), routeArgs("add", r)...) }
func (ipTool) RouteReplace(r Route) error {
	return runIP(familyFlag(r.Dst.IP), routeArgs("replace", r)...)
}
func (ipTool) RouteDel(r Route) error { return runIP(familyFlag(r.Dst.IP), routeArgs("del", r)...) }

// routeArgs turns r into "route <verb> [type] <dst> [via gw] [dev name]",
// the order "ip" expects: the type keyword comes before the destination.
func routeArgs(verb string, r Route) []string {
	args := []string{"route", verb}
	switch r.Type {
	case Blackhole:
		args = append(args, "blackhole")
	case Unreachable:
		args = append(args, "unreachable")
	}
	args = append(args, dstString(r.Dst))
	if r.Gw != nil {
		args = append(args, "via", r.Gw.String())
	}
	if r.Dev != "" {
		args = append(args, "dev", r.Dev)
	}
	return args
}

// runIP runs "ip [-6] args..." and puts the program's own message into the
// error, because that message ("RTNETLINK answers: File exists") is the only
// useful part when something fails.
func runIP(family string, args ...string) error {
	if family != "" {
		args = append([]string{family}, args...)
	}
	out, err := exec.Command("ip", args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("ip %s: %s %w", strings.Join(args, " "), strings.TrimSpace(string(out)), err)
	}
	return nil
}

// familyFlag is "-6" for an IPv6 address. "ip" guesses the family from the
// address too, but only once it has parsed it, and "ip route del
// unreachable 2000::/3" without -6 is ambiguous on some versions.
func familyFlag(ip net.IP) string {
	if ip.To4() == nil {
		return "-6"
	}
	return ""
}

// cidrString prints an interface address with its prefix: 10.10.0.10/24.
// (net.IPNet.String would print the network, 10.10.0.0/24, which is wrong
// for an address on an interface.)
func cidrString(a *net.IPNet) string {
	ones, _ := a.Mask.Size()
	return a.IP.String() + "/" + strconv.Itoa(ones)
}

// dstString prints a route destination; a zero-length prefix is "default",
// the word loft always used for it.
func dstString(n *net.IPNet) string {
	if ones, _ := n.Mask.Size(); ones == 0 {
		return "default"
	}
	return n.String()
}

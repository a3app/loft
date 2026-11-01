package protect

// Physical IPv4 uplink: skip TUN/VPN so overlay sockets do not hairpin SSTP.
//
// "Hairpin" means a socket leaves through another tunnel (or loft's own)
// and comes back, instead of using the real link. To avoid it, loft finds
// the interface of the real default route: it reads the routing table
// (netstat on macOS, /proc/net/route on Linux and Android), skips anything
// that looks like a tunnel, and falls back to the first up interface.

import (
	"context"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// tunnelIface is a VPN/TUN/PPP name. Overlay must not bind these.
func tunnelIface(name string) bool {
	n := strings.ToLower(strings.TrimSpace(name))
	if n == "" {
		return false
	}
	for _, p := range []string{
		"utun", "tun", "tap", "ppp", "ipsec", "wg", "zt", "tailscale",
		"feth", "awdl", "llw", "gif", "stf", "p2p", "anpi", "vmnet", "vnic",
		"lo", "loft",
	} {
		if n == p || strings.HasPrefix(n, p) {
			return true
		}
	}
	return false
}

// PhysicalName is the IPv4 interface of the real default route, or empty.
func PhysicalName() string {
	if name := physicalFromRoutes(); name != "" {
		return name
	}
	return physicalFromIfaces()
}

// physicalFromRoutes reads the OS routing table; other systems return empty.
func physicalFromRoutes() string {
	switch runtime.GOOS {
	case "darwin":
		return darwinPhysical()
	case "linux", "android":
		return linuxPhysical()
	default:
		return ""
	}
}

// darwinPhysical asks netstat for the IPv4 default routes (400 ms limit).
func darwinPhysical() string {
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	out, err := exec.CommandContext(ctx, "netstat", "-rn", "-f", "inet").Output()
	if err != nil {
		return ""
	}
	return pickPhysical(parseNetstatDefaults(string(out)))
}

// uplinkHop is one default route: gateway and interface.
type uplinkHop struct {
	gw, iface string
}

// parseNetstatDefaults reads `netstat -rn -f inet` default rows.
func parseNetstatDefaults(s string) []uplinkHop {
	var out []uplinkHop
	for _, raw := range strings.Split(s, "\n") {
		f := strings.Fields(raw)
		if len(f) < 3 || f[0] != "default" {
			continue
		}
		out = append(out, uplinkHop{gw: f[1], iface: f[len(f)-1]})
	}
	return out
}

// pickPhysical skips tunnels and link-only routes and prefers an en*
// interface (Ethernet or Wi-Fi on macOS), else the first other one.
func pickPhysical(hops []uplinkHop) string {
	var en, other string
	for _, hop := range hops {
		if tunnelIface(hop.iface) || linkGateway(hop.gw) {
			continue
		}
		if strings.HasPrefix(hop.iface, "en") {
			if en == "" {
				en = hop.iface
			}
			continue
		}
		if other == "" {
			other = hop.iface
		}
	}
	if en != "" {
		return en
	}
	return other
}

// linkGateway is a route with no gateway IP ("link#20"), which a VPN uses
// to capture the default route.
func linkGateway(gw string) bool {
	return strings.HasPrefix(gw, "link#") || strings.HasPrefix(gw, "link/")
}

func linuxPhysical() string {
	b, err := os.ReadFile("/proc/net/route")
	if err != nil {
		return ""
	}
	return pickLinuxRoute(string(b))
}

// pickLinuxRoute reads /proc/net/route and returns the non-tunnel interface
// of the default route (destination 00000000) with the lowest metric.
func pickLinuxRoute(s string) string {
	best, metric := "", int(^uint(0)>>1)
	for i, line := range strings.Split(s, "\n") {
		if i == 0 {
			continue
		}
		f := strings.Fields(line)
		if len(f) < 8 || f[1] != "00000000" {
			continue
		}
		name := f[0]
		if tunnelIface(name) {
			continue
		}
		m, err := strconv.Atoi(f[6])
		if err != nil {
			m = metric
		}
		if best == "" || m < metric {
			best, metric = name, m
		}
	}
	return best
}

// physicalFromIfaces is the fallback when no route was found: the first up,
// non-loopback, non-tunnel interface with an IPv4, preferring en*.
func physicalFromIfaces() string {
	ifis, err := net.Interfaces()
	if err != nil {
		return ""
	}
	var en, other string
	for _, ifi := range ifis {
		if ifi.Flags&net.FlagUp == 0 || ifi.Flags&net.FlagLoopback != 0 {
			continue
		}
		if tunnelIface(ifi.Name) {
			continue
		}
		if firstIPv4(&ifi) == nil {
			continue
		}
		if strings.HasPrefix(ifi.Name, "en") {
			if en == "" {
				en = ifi.Name
			}
			continue
		}
		if other == "" {
			other = ifi.Name
		}
	}
	if en != "" {
		return en
	}
	return other
}

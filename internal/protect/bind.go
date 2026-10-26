package protect

// Bind pins outbound sockets to an interface name or a local/gateway IPv4.
// On desktop and routers there is no VpnService hook, so loft keeps its own
// sockets off the tunnel by binding them to the physical interface.

import (
	"fmt"
	"net"
	"strings"
	"sync"
)

var (
	bindMu   sync.Mutex
	bindIdx  int
	bindIP   net.IP
	bindDev  string
	bindAuto bool
)

// Bind pins later Timeout/NetDialer/ListenUDP4 sockets to spec.
// spec is an interface name (en0), a local IPv4, or an IPv4 on that subnet
// (the gateway). Empty clears the pin.
func Bind(spec string) error {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		bindMu.Lock()
		bindIdx, bindIP, bindDev, bindAuto = 0, nil, "", false
		bindMu.Unlock()
		return nil
	}
	idx, ip, name, err := resolveBind(spec)
	if err != nil {
		return err
	}
	bindMu.Lock()
	bindIdx, bindIP, bindDev, bindAuto = idx, ip, name, false
	bindMu.Unlock()
	return nil
}

// BindUplink pins spec. Empty spec pins the physical default (skip TUN/VPN).
// Auto-pin failure is ignored so a missing uplink does not abort start.
func BindUplink(spec string) error {
	spec = strings.TrimSpace(spec)
	if spec != "" {
		return Bind(spec)
	}
	name := PhysicalName()
	if name == "" {
		return Bind("")
	}
	if err := Bind(name); err != nil {
		return nil
	}
	bindMu.Lock()
	bindAuto = true
	bindMu.Unlock()
	return nil
}

// Bound is "en0 172.20.10.7", or empty when unbound.
func Bound() string {
	bindMu.Lock()
	defer bindMu.Unlock()
	if bindDev == "" {
		return ""
	}
	if len(bindIP) == 0 {
		return bindDev
	}
	return bindDev + " " + bindIP.String()
}

// BoundName is the -if interface, or empty.
func BoundName() string {
	bindMu.Lock()
	defer bindMu.Unlock()
	return bindDev
}

// BoundIP is the IPv4 Bind pinned, or nil.
func BoundIP() net.IP {
	return localIPv4()
}

// RefreshBound re-reads the -if address after DHCP / hotspot reconnect.
// Auto-pin re-picks the physical iface when the user hops Wi-Fi / LTE.
func RefreshBound() {
	bindMu.Lock()
	auto := bindAuto
	name := bindDev
	bindMu.Unlock()
	if auto {
		_ = BindUplink("")
		return
	}
	if name == "" {
		return
	}
	_ = Bind(name)
}

func boundIndex() int {
	bindMu.Lock()
	defer bindMu.Unlock()
	return bindIdx
}

func localIPv4() net.IP {
	bindMu.Lock()
	defer bindMu.Unlock()
	if len(bindIP) == 0 {
		return nil
	}
	return append(net.IP(nil), bindIP...)
}

// resolveBind turns a Bind spec into an interface index, its IPv4 and its
// name. It tries an interface name first, then an exact local address,
// then any interface whose subnet contains the address (a gateway IP).
func resolveBind(spec string) (idx int, ip net.IP, name string, err error) {
	if ifi, e := net.InterfaceByName(spec); e == nil {
		return ifi.Index, firstIPv4(ifi), ifi.Name, nil
	}
	parsed := net.ParseIP(spec)
	if parsed == nil {
		return 0, nil, "", fmt.Errorf("unknown interface %q", spec)
	}
	parsed = parsed.To4()
	if parsed == nil {
		return 0, nil, "", fmt.Errorf("want IPv4 %s", spec)
	}
	ifaces, e := net.Interfaces()
	if e != nil {
		return 0, nil, "", e
	}
	for _, ifi := range ifaces {
		ipn := v4nets(&ifi)
		for _, n := range ipn {
			if n.IP.Equal(parsed) {
				return ifi.Index, n.IP, ifi.Name, nil
			}
		}
	}
	for _, ifi := range ifaces {
		ipn := v4nets(&ifi)
		for _, n := range ipn {
			if n.Contains(parsed) {
				return ifi.Index, n.IP, ifi.Name, nil
			}
		}
	}
	return 0, nil, "", fmt.Errorf("no interface for %s", spec)
}

func firstIPv4(ifi *net.Interface) net.IP {
	for _, n := range v4nets(ifi) {
		return n.IP
	}
	return nil
}

// v4nets lists the IPv4 networks configured on ifi, in 4-byte form.
func v4nets(ifi *net.Interface) []*net.IPNet {
	addrs, err := ifi.Addrs()
	if err != nil {
		return nil
	}
	var out []*net.IPNet
	for _, a := range addrs {
		n, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		v4 := n.IP.To4()
		if v4 == nil {
			continue
		}
		out = append(out, &net.IPNet{IP: v4, Mask: n.Mask})
	}
	return out
}

package protect

// Bind pins outbound sockets to an interface name or a local/gateway IPv4.

import (
	"fmt"
	"net"
	"strings"
	"sync"
)

var (
	bindMu  sync.Mutex
	bindIdx int
	bindIP  net.IP
	bindDev string
)

// Bind pins later Timeout/NetDialer/ListenUDP4 sockets to spec.
// spec is an interface name (en0), a local IPv4, or an IPv4 on that subnet
// (the gateway). Empty clears the pin.
func Bind(spec string) error {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		bindMu.Lock()
		bindIdx, bindIP, bindDev = 0, nil, ""
		bindMu.Unlock()
		return nil
	}
	idx, ip, name, err := resolveBind(spec)
	if err != nil {
		return err
	}
	bindMu.Lock()
	bindIdx, bindIP, bindDev = idx, ip, name
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
func RefreshBound() {
	bindMu.Lock()
	name := bindDev
	bindMu.Unlock()
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

package transport

// Path watch: physical IPv4 change (Wi-Fi / LTE). Supervise rebinds GET
// HTTP/2 (same sid) and Parks other fronts. -if pins the key to that NIC:
// a leftover TUN 0/1 is not a path change and must not RST the GET session.
//
// When a phone moves from Wi-Fi to LTE, the old TCP connection under the
// front is dead, but the OS may take a long time to say so. Waiting for a
// timeout would freeze the user's traffic. So the client samples which local
// IPv4 address its traffic leaves from, and when that changes it reconnects
// right away. -if is the client flag that binds loft to one network
// interface; then only that interface's address counts. TUN 0/1 refers to
// the default-route halves (0.0.0.0/1, 128.0.0.0/1) a TUN client installs.

import (
	"context"
	"net"
	"strings"
	"time"

	"github.com/a3app/loft/internal/protect"
)

// pathProbe is the local IPv4 used as the egress key. Tests replace it.
var pathProbe = physicalAddr

// pathTick is how often WatchPath samples egress. Tests shorten it.
var pathTick = 250 * time.Millisecond

// hostOnly is the IP of host:port; addr without a port is returned as-is.
func hostOnly(addr string) string {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return host
}

// overlaySrc is a tunnel/link-local source: TUN 0/1 is not a physical path change.
// While loft's own TUN holds the default route, the "egress" address is the
// overlay address (10.8.x.x by default) or a utun/tun/ppp interface; such a
// sample says nothing about the physical network and is ignored.
func overlaySrc(ip string) bool {
	p := net.ParseIP(ip)
	if p == nil {
		return false
	}
	v4 := p.To4()
	if v4 == nil {
		return false
	}
	if v4[0] == 10 && v4[1] == 8 {
		return true
	}
	if v4[0] == 169 && v4[1] == 254 {
		return true
	}
	name := ifaceByIP(ip)
	if strings.HasPrefix(name, "utun") || strings.HasPrefix(name, "tun") || strings.HasPrefix(name, "ppp") {
		return true
	}
	return false
}

// physicalAddr is the IPv4 WatchPath keys on: -if when set, else default egress.
func physicalAddr() string {
	if name := protect.BoundName(); name != "" {
		if ip := ifaceIPv4(name); ip != "" {
			return ip
		}
		return ""
	}
	eg := egressAddr()
	if overlaySrc(eg) {
		return ""
	}
	return eg
}

// ifaceIPv4 is the first IPv4 on named iface, or empty.
func ifaceIPv4(name string) string {
	ifi, err := net.InterfaceByName(name)
	if err != nil {
		return ""
	}
	addrs, err := ifi.Addrs()
	if err != nil {
		return ""
	}
	for _, a := range addrs {
		n, ok := a.(*net.IPNet)
		if !ok {
			continue
		}
		v4 := n.IP.To4()
		if v4 != nil && !v4.IsLinkLocalUnicast() {
			return v4.String()
		}
	}
	return ""
}

// egressAddr is the source IPv4 of a UDP datagram toward 1.1.1.1.
// The ephemeral port is stripped: a new socket every tick would look like a path change.
func egressAddr() string {
	d := *protect.NetDialer(400 * time.Millisecond)
	c, err := d.Dial("udp4", "1.1.1.1:53")
	if err != nil {
		c, err = d.Dial("udp4", "8.8.8.8:53")
		if err != nil {
			return ""
		}
	}
	addr := hostOnly(c.LocalAddr().String())
	_ = c.Close()
	return addr
}

// PathInfo is one physical-path sample: iface, local IPv4, default gw, egress.
type PathInfo struct {
	IfName string
	IP     string
	GW     string
	Egress string
}

// Key is ip/egress/if; used to detect a path change for drive logs.
func (p PathInfo) Key() string {
	return p.IfName + "|" + p.IP + "|" + p.GW + "|" + p.Egress
}

// SamplePath reads the current physical IPv4 path (iface, IP, gateway).
func SamplePath() PathInfo {
	ip := pathProbe()
	name := protect.BoundName()
	if name == "" {
		name = ifaceByIP(ip)
	}
	eg := ip
	if name == "" {
		eg = egressAddr()
		if overlaySrc(eg) {
			eg = ip
		}
	}
	return PathInfo{IfName: name, IP: ip, GW: defaultGateway(), Egress: eg}
}

// ifaceByIP is the interface that owns ip, or empty.
func ifaceByIP(ip string) string {
	want := net.ParseIP(ip)
	if want == nil {
		return ""
	}
	ifis, err := net.Interfaces()
	if err != nil {
		return ""
	}
	for _, ifi := range ifis {
		addrs, err := ifi.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			n, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			if n.IP.Equal(want) {
				return ifi.Name
			}
		}
	}
	return ""
}

// MergePath forwards a signal from a or b (buffered; extra signals drop).
func MergePath(ctx context.Context, a, b <-chan struct{}) <-chan struct{} {
	out := make(chan struct{}, 1)
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-a:
				select {
				case out <- struct{}{}:
				default:
				}
			case <-b:
				select {
				case out <- struct{}{}:
				default:
				}
			}
		}
	}()
	return out
}

// WatchPath sends on the returned channel when the default IPv4 egress changes.
// Empty and overlay samples are skipped, so losing the network for a moment
// (no address) is not reported; only a move to a different address is.
func WatchPath(ctx context.Context) <-chan struct{} {
	ch := make(chan struct{}, 1)
	go func() {
		last := pathProbe()
		t := time.NewTicker(pathTick)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				now := pathProbe()
				if now == "" || overlaySrc(now) {
					continue
				}
				if last == "" {
					last = now
					continue
				}
				if now == last {
					continue
				}
				last = now
				protect.RefreshBound()
				select {
				case ch <- struct{}{}:
				default:
				}
			}
		}
	}()
	return ch
}

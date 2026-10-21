package inbound

// ipv6.go classifies TUN frames. Native IPv6 is dropped until overlay IPv6 is assigned.

import "github.com/a3app/loft/internal/transport"

const (
	tunDrop = iota
	tunIPv4
	tunIPv6
)

// overlayIPv6Configured is true when the node assigned an overlay IPv6
// address. Until then Loft is an IPv4-only tunnel: no ::/0, no inet6 on
// utun, inbound IPv6 dropped so Happy Eyeballs uses IPv4.
func overlayIPv6Configured() bool {
	return transport.OverlayIPv6() != ""
}

// classifyTUN returns tunIPv4, tunIPv6 (only if overlay IPv6 is set), or tunDrop.
func classifyTUN(raw []byte) int {
	if len(raw) == 0 {
		return tunDrop
	}
	switch raw[0] >> 4 {
	case 4:
		if len(raw) < 20 {
			return tunDrop
		}
		return tunIPv4
	case 6:
		if len(raw) < 40 {
			return tunDrop
		}
		if overlayIPv6Configured() {
			return tunIPv6
		}
		return tunDrop
	default:
		return tunDrop
	}
}

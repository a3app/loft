//go:build windows

package protect

import "golang.org/x/sys/windows"

// IP_UNICAST_IF / IPV6_UNICAST_IF are not in x/sys v0.49.
// The index is network byte order. A socket without this follows the
// routing table, so the tunnel default route (metric 5) swallows loft's
// own dials and the GET idle timer cancels them.
const (
	ipUnicastIF   = 31
	ipv6UnicastIF = 31
)

func setBoundIf(fd, idx int, ipv6 bool) error {
	if idx <= 0 {
		return nil
	}
	v := htonl(uint32(idx))
	level := windows.IPPROTO_IP
	opt := ipUnicastIF
	if ipv6 {
		level = windows.IPPROTO_IPV6
		opt = ipv6UnicastIF
	}
	return windows.SetsockoptInt(windows.Handle(fd), level, opt, int(v))
}

func htonl(v uint32) uint32 {
	return v<<24 | (v&0xff00)<<8 | (v&0xff0000)>>8 | v>>24
}

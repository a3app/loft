//go:build darwin

package protect

import "golang.org/x/sys/unix"

// setBoundIf pins the socket to interface idx. macOS has separate options
// for IPv4 (IP_BOUND_IF) and IPv6 (IPV6_BOUND_IF).
func setBoundIf(fd, idx int, ipv6 bool) error {
	if idx <= 0 {
		return nil
	}
	if ipv6 {
		return unix.SetsockoptInt(fd, unix.IPPROTO_IPV6, unix.IPV6_BOUND_IF, idx)
	}
	return unix.SetsockoptInt(fd, unix.IPPROTO_IP, unix.IP_BOUND_IF, idx)
}

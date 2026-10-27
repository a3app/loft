//go:build unix

package transport

import (
	"net"

	"golang.org/x/sys/unix"
)

// setSockTTL sets the IP TTL (IPv6: unicast hop limit) of t and returns the
// old value. Used by disorder (disorder.go).
func setSockTTL(t *net.TCPConn, ttl int) (int, error) {
	rc, err := t.SyscallConn()
	if err != nil {
		return 0, err
	}
	level, opt := unix.IPPROTO_IP, unix.IP_TTL
	if a, ok := t.LocalAddr().(*net.TCPAddr); ok && a.IP.To4() == nil {
		level, opt = unix.IPPROTO_IPV6, unix.IPV6_UNICAST_HOPS
	}
	var old int
	var serr error
	if err := rc.Control(func(fd uintptr) {
		old, serr = unix.GetsockoptInt(int(fd), level, opt)
		if serr == nil {
			serr = unix.SetsockoptInt(int(fd), level, opt, ttl)
		}
	}); err != nil {
		return 0, err
	}
	return old, serr
}

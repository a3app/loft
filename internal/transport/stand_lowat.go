//go:build linux || darwin

package transport

import (
	"net"

	"golang.org/x/sys/unix"
)

// standApplyLowat caps unsent bytes so a short write is not stuck behind the
// send queue. standLowat zero leaves the kernel limit.
func standApplyLowat(t *net.TCPConn) {
	n := standLowat()
	if n <= 0 || t == nil {
		return
	}
	raw, err := t.SyscallConn()
	if err != nil {
		return
	}
	_ = raw.Control(func(fd uintptr) {
		_ = unix.SetsockoptInt(int(fd), unix.IPPROTO_TCP, unix.TCP_NOTSENT_LOWAT, n)
	})
}

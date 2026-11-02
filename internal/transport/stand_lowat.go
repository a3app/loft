//go:build linux || darwin

package transport

import (
	"net"

	"golang.org/x/sys/unix"
)

// standApplyLowat caps unsent bytes so a short write is not stuck behind the
// send queue. standLowat zero leaves the kernel limit (does not write).
func standApplyLowat(t *net.TCPConn) {
	n := standLowat()
	if n <= 0 {
		return
	}
	setLowat(t, n)
}

// standClearLowat writes TCP_NOTSENT_LOWAT 0 (kernel default, unlimited).
// Accept already applied notsentLowat; standApplyLowat(0) does not undo it.
func standClearLowat(t *net.TCPConn) { setLowat(t, 0) }

func setLowat(t *net.TCPConn, n int) {
	if t == nil {
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

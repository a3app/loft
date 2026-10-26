package transport

import (
	"net"

	"golang.org/x/sys/unix"
)

// setTCPCongestion switches the socket's congestion control, e.g. to "bbr".
// It fails when the kernel has no such module or does not allow it.
func setTCPCongestion(t *net.TCPConn, name string) error {
	rc, err := t.SyscallConn()
	if err != nil {
		return err
	}
	var serr error
	if err := rc.Control(func(fd uintptr) {
		serr = unix.SetsockoptString(int(fd), unix.IPPROTO_TCP, unix.TCP_CONGESTION, name)
	}); err != nil {
		return err
	}
	return serr
}

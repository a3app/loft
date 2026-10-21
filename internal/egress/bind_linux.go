package egress

import (
	"syscall"

	"golang.org/x/sys/unix"
)

// bindDev ties a socket to an interface; with the panel's policy rule
// (oif DEV lookup TABLE) the kernel then routes it out of that channel.
func bindDev(dev string) func(network, address string, c syscall.RawConn) error {
	return func(_, _ string, c syscall.RawConn) error {
		var serr error
		if err := c.Control(func(fd uintptr) { serr = unix.SetsockoptString(int(fd), unix.SOL_SOCKET, unix.SO_BINDTODEVICE, dev) }); err != nil {
			return err
		}
		return serr
	}
}

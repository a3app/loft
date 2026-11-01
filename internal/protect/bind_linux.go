//go:build linux

package protect

import "golang.org/x/sys/unix"

// setBoundIf pins the socket to interface idx (SO_BINDTOIFINDEX). One
// option covers IPv4 and IPv6 on Linux.
func setBoundIf(fd, idx int, _ bool) error {
	if idx <= 0 {
		return nil
	}
	return unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_BINDTOIFINDEX, idx)
}

//go:build linux

package protect

import "golang.org/x/sys/unix"

func setBoundIf(fd, idx int, _ bool) error {
	if idx <= 0 {
		return nil
	}
	return unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_BINDTOIFINDEX, idx)
}

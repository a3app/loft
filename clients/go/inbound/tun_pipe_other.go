//go:build !windows

package inbound

// tun_pipe_other.go is the non-Windows stub for the packet pipe. Only the
// Windows service hands packets over a named pipe (tun_pipe_windows.go).

import "fmt"

// OpenPacketPipe is the Windows service pipe. Other systems use utun.
func OpenPacketPipe(name string) (tunIO, error) {
	return nil, fmt.Errorf("tun pipe: %s", name)
}

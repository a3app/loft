//go:build !windows

package inbound

import "fmt"

// OpenPacketPipe is the Windows service pipe. Other systems use utun.
func OpenPacketPipe(name string) (tunIO, error) {
	return nil, fmt.Errorf("tun pipe: %s", name)
}

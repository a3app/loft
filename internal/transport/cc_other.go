//go:build !linux

package transport

import (
	"errors"
	"net"
)

// setTCPCongestion is Linux only; elsewhere TCP keeps the system default.
func setTCPCongestion(*net.TCPConn, string) error {
	return errors.New("tcp congestion control: linux only")
}

//go:build !linux

package transport

import (
	"errors"
	"net"
)

var errNoTCPCC = errors.New("tcp congestion control: linux only")

// setTCPCongestion is Linux only; elsewhere TCP keeps the system default.
func setTCPCongestion(*net.TCPConn, string) error { return errNoTCPCC }

// TCPBBRReady is Linux only; elsewhere TCP fronts stay on the default.
func TCPBBRReady() error { return errNoTCPCC }

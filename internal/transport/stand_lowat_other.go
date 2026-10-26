//go:build !linux && !darwin

package transport

import "net"

// standApplyLowat does nothing here: TCP_NOTSENT_LOWAT is set only on
// Linux and macOS.
func standApplyLowat(t *net.TCPConn) {}

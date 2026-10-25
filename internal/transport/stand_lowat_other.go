//go:build !linux && !darwin

package transport

import "net"

func standApplyLowat(t *net.TCPConn) {}

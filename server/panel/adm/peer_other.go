//go:build !linux

package adm

// This file is the non-Linux stand-in for peer_linux.go, so the package
// still builds (and its tests run) on macOS and other systems.

import (
	"errors"
	"net"
)

// PeerUID refuses everyone off Linux: the daemon is Linux-only.
func PeerUID(int) func(net.Conn) error {
	return func(net.Conn) error { return errors.New("peer credentials need Linux") }
}

//go:build !linux

package adm

import (
	"errors"
	"net"
)

// PeerUID refuses everyone off Linux: the daemon is Linux-only.
func PeerUID(int) func(net.Conn) error {
	return func(net.Conn) error { return errors.New("peer credentials need Linux") }
}

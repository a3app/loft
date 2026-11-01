//go:build !linux

package inbound

import "errors"

// EnableLANReturn is Linux-only: the LAN gateway exists for loft in a
// router's container, which is always Linux. See lanreturn_linux.go.
func EnableLANReturn(string, string) (func(), error) {
	return nil, errors.New("listen.gateway works on Linux only")
}

//go:build !linux

package egress

import (
	"errors"
	"syscall"
)

// bindDev is the non-Linux stand-in: SO_BINDTODEVICE exists only on Linux,
// so interface channels fail here instead of silently using the default
// route. Direct (dev == "") still works.
func bindDev(dev string) func(network, address string, c syscall.RawConn) error {
	if dev == "" {
		return nil
	}
	return func(string, string, syscall.RawConn) error { return errors.New("binding to an interface needs Linux") }
}

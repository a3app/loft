//go:build !linux

package egress

import (
	"errors"
	"syscall"
)

func bindDev(dev string) func(network, address string, c syscall.RawConn) error {
	if dev == "" {
		return nil
	}
	return func(string, string, syscall.RawConn) error { return errors.New("binding to an interface needs Linux") }
}

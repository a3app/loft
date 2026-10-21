//go:build !linux

package egress

import (
	"errors"
	"syscall"
)

func bindDev(string) func(network, address string, c syscall.RawConn) error {
	return func(string, string, syscall.RawConn) error { return errors.New("binding to an interface needs Linux") }
}

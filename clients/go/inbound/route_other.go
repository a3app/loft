//go:build !darwin && !linux

package inbound

// route_other.go stubs kill-switch and split default off Darwin and Linux.

import "fmt"

// enableKillSwitch is unsupported off Darwin and Linux.
func enableKillSwitch(string, []string) (func(), error) {
	return nil, fmt.Errorf("kill-switch: unsupported os")
}

// addSplitDefault is a no-op off Darwin and Linux.
func addSplitDefault(string, []string) (func(), error) {
	return func() {}, nil
}

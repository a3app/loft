//go:build linux

package inbound

// dns_apply_linux.go sets per-device DNS via resolvectl and reverts it on undo.

import "os/exec"

// applyDNS sets resolvectl DNS and ~. on dev; undo reverts the device.
func applyDNS(dev string, servers []string) func() {
	if dev == "" || len(servers) == 0 {
		return func() {}
	}
	args := append([]string{"dns", dev}, servers...)
	_ = exec.Command("resolvectl", args...).Run()
	_ = exec.Command("resolvectl", "domain", dev, "~.").Run()
	return func() {
		_ = exec.Command("resolvectl", "revert", dev).Run()
	}
}

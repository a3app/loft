//go:build darwin || linux

package inbound

// tun_name_unix.go sets the TUN interface name on macOS and Linux. The water
// library keeps the name in a different config field per system, so each
// build gets its own setter (see tun_name_other.go).

import "github.com/songgao/water"

// setWaterName asks water to give the new interface this name.
func setWaterName(cfg *water.Config, name string) {
	cfg.Name = name
}

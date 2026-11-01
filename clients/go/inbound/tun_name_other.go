//go:build !darwin && !linux

package inbound

// tun_name_other.go sets the TUN interface name on systems other than macOS
// and Linux (in practice Windows). The water library keeps the name in a
// different config field per system, so each build gets its own setter.

import "github.com/songgao/water"

// setWaterName asks water to give the new interface this name.
func setWaterName(cfg *water.Config, name string) {
	cfg.InterfaceName = name
}

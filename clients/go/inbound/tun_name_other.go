//go:build !darwin && !linux

package inbound

import "github.com/songgao/water"

func setWaterName(cfg *water.Config, name string) {
	cfg.InterfaceName = name
}

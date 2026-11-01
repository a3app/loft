package ifcfg

import (
	"fmt"
	"os"
	"strings"
)

// PromoteSecondaries turns on net.ipv4.conf.<dev>.promote_secondaries.
//
// Why it matters: when an interface has two IPv4 addresses in the same
// subnet, Linux calls the first one "primary" and the second "secondary".
// By default, deleting the primary also deletes every secondary. When loft
// moves the TUN from its old overlay address to a new one, it adds the new
// address first (so the interface is never without one, which would drop
// all routes through it) and then deletes the old one. Without this switch
// that delete would take the new address with it.
//
// It writes the /proc/sys file directly instead of running "sysctl", for
// the same reason this package exists: the program may not be there. Both
// backends share it.
func PromoteSecondaries(dev string) error {
	// The name becomes part of a file path, so refuse anything that could
	// step out of the directory. Real interface names are at most 15 bytes
	// (IFNAMSIZ-1) and never contain "/".
	if dev == "" || dev == "." || dev == ".." || strings.Contains(dev, "/") || len(dev) > 15 {
		return fmt.Errorf("ifcfg: bad interface name %q", dev)
	}
	p := "/proc/sys/net/ipv4/conf/" + dev + "/promote_secondaries"
	if err := os.WriteFile(p, []byte("1"), 0); err != nil {
		return fmt.Errorf("ifcfg: %w", err)
	}
	return nil
}

// Package inbound is the loft client listener: SOCKS5, userspace TUN
// (ICMP/UDP/TCP), Darwin/Linux DNS and routes, optional overlay IPv6.
package inbound

import (
	"fmt"
	"net"
	"os/exec"
	"runtime"
	"strconv"
	"strings"

	"github.com/a3app/loft/internal/transport"

	"github.com/songgao/water"
)

// Device is a live TUN. Attach hooks ICMP on each new mux session (metro).
type Device struct {
	ts   *tunStack
	ifc  tunIO
	undo func()
}

// Attach hooks ICMP/UDP datagrams on the current mux session.
func (d *Device) Attach(dial transport.Dialer) {
	if d == nil || d.ts == nil {
		return
	}
	d.ts.Attach(dial)
}

// Close undoes DNS/routes, stops the stack, and closes the TUN.
func (d *Device) Close() error {
	if d == nil {
		return nil
	}
	if d.undo != nil {
		d.undo()
		d.undo = nil
	}
	if d.ts != nil {
		d.ts.Close()
		d.ts = nil
	}
	if d.ifc != nil {
		err := d.ifc.Close()
		d.ifc = nil
		return err
	}
	return nil
}

// OverlayCIDR is Status.IP with the prefix from tun_addr (default /24).
func OverlayCIDR(overlayIP, tunAddr string) string {
	ones := 24
	if tunAddr != "" {
		if _, n, err := net.ParseCIDR(tunAddr); err == nil {
			if o, _ := n.Mask.Size(); o > 0 {
				ones = o
			}
		}
	}
	ip := strings.TrimSpace(overlayIP)
	if ip == "" {
		if tunAddr != "" {
			return tunAddr
		}
		return "10.7.0.2/24"
	}
	if strings.Contains(ip, "/") {
		return ip
	}
	return ip + "/" + strconv.Itoa(ones)
}

// StartTUN brings up a real utun/tun, userspace stack, and optional kill-switch.
func StartTUN(device, cidr string, killSwitch bool, d transport.Dialer, exclude []string) (*Device, error) {
	if cidr == "" {
		cidr = OverlayCIDR(transport.OverlayIP(), "")
	}
	cfg := water.Config{DeviceType: water.TUN}
	if device != "" && device != "auto" {
		cfg.Name = device
	}
	ifc, err := water.New(cfg)
	if err != nil {
		return nil, fmt.Errorf("tun: %w", err)
	}
	ip, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		ifc.Close()
		return nil, err
	}
	if err := configureTUN(ifc.Name(), ip, ipnet); err != nil {
		ifc.Close()
		return nil, err
	}
	st, err := newStack(ifc, d, ip, ipnet)
	if err != nil {
		ifc.Close()
		return nil, err
	}
	var undo func()
	if killSwitch {
		undo, err = enableKillSwitch(ifc.Name(), exclude)
		if err != nil {
			st.Close()
			ifc.Close()
			return nil, fmt.Errorf("kill-switch: %w", err)
		}
	} else {
		undo, _ = addSplitDefault(ifc.Name(), exclude)
	}
	dnsUndo := applyDNS(ifc.Name(), transport.OverlayDNS())
	if undo != nil && dnsUndo != nil {
		prev := undo
		undo = func() { dnsUndo(); prev() }
	} else if undo == nil {
		undo = dnsUndo
	}
	return &Device{ts: st, ifc: ifc, undo: undo}, nil
}

// configureTUN sets address and MTU. IPv6 stays off until overlay IPv6.
func configureTUN(name string, ip net.IP, n *net.IPNet) error {
	ones, _ := n.Mask.Size()
	switch runtime.GOOS {
	case "darwin":
		// NEPacketTunnel / SSTP Connect: overlay IP as both p2p ends (A --> A).
		if err := run("ifconfig", name, "inet", ip.String(), ip.String(), "netmask", "255.255.255.255", "up", "mtu", "1280"); err != nil {
			return err
		}
		// IPv4-only until the node assigns overlay IPv6. Do not install ::/0.
		_ = run("ifconfig", name, "inet6", "ifdisabled")
		_ = run("ifconfig", "lo0", "inet", ip.String(), "-alias")
		netn := ip.Mask(n.Mask).String() + "/" + strconv.Itoa(ones)
		_ = run("route", "-n", "add", "-inet", netn, "-interface", name)
		return nil
	case "linux":
		if err := run("ip", "addr", "add", ip.String()+"/"+strconv.Itoa(ones), "dev", name); err != nil {
			return err
		}
		return run("ip", "link", "set", "dev", name, "up", "mtu", "1280")
	default:
		return fmt.Errorf("tun: os %s", runtime.GOOS)
	}
}

// run executes name with args and wraps CombinedOutput on failure.
func run(name string, args ...string) error {
	out, err := exec.Command(name, args...).CombinedOutput()
	if err != nil {
		return fmt.Errorf("%s %s: %s %w", name, strings.Join(args, " "), out, err)
	}
	return nil
}

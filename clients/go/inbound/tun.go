// Package inbound is the loft client listener: SOCKS5, userspace TUN
// (ICMP/UDP/TCP), Darwin/Linux DNS and routes, optional overlay IPv6.
//
// "Inbound" means where local traffic enters the client: apps either talk
// to the SOCKS5 proxy (socks5.go) or the system routes their packets into a
// TUN, a virtual network interface (this file). Both hand the traffic to a
// transport.Dialer, the live session to the loft node. The overlay is the
// private network the node gives its members, with addresses like 10.7.0.x.
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
	l3   *l3Pump
	ifc  tunIO
	undo func()
}

// Attach hooks ICMP/UDP datagrams on the current mux session, or an L3 IP pump.
func (d *Device) Attach(dial transport.Dialer) {
	if d == nil {
		return
	}
	if l, ok := dial.(interface{ Live() transport.Dialer }); ok {
		if inner := l.Live(); inner != nil {
			dial = inner
		}
	}
	if n, ok := dial.(*L3Net); ok && d.ifc != nil {
		n.AttachTUN(d.ifc)
		return
	}
	if p, ok := ipPumpOf(dial); ok {
		d.attachL3(p)
		return
	}
	if d.ts != nil {
		d.ts.Attach(dial)
	}
}

// ipPumpOf reports whether front d carries raw IP packets (an L3 front),
// either directly or through an IPPump method, and returns that pump.
func ipPumpOf(d transport.Dialer) (transport.IPPump, bool) {
	if p, ok := d.(transport.IPPump); ok {
		return p, true
	}
	type hasPump interface {
		IPPump() (transport.IPPump, bool)
	}
	if h, ok := d.(hasPump); ok {
		return h.IPPump()
	}
	return nil, false
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
	if d.l3 != nil {
		d.l3.Stop()
		d.l3 = nil
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
	return openTUN(device, cidr, killSwitch, d, exclude, false)
}

// StartRoutedTUN opens a named TUN and the userspace stack, and does not
// install an address, a default route, a split default, or DNS. KeeneticOS
// owns the routing table: the device is opkgtunN and ndmc registers OpkgTunN.
// "auto" is refused so a desktop utun is never created by mistake.
func StartRoutedTUN(device, cidr string, d transport.Dialer) (*Device, error) {
	if device == "" || device == "auto" {
		return nil, fmt.Errorf("tun: routed mode needs a device name")
	}
	return openTUN(device, cidr, false, d, nil, true)
}

// Name is the kernel interface (opkgtun0, utun6). Empty after Close.
func (d *Device) Name() string {
	if d == nil || d.ifc == nil {
		return ""
	}
	return d.ifc.Name()
}

// StartTUNIO runs the userspace stack on a device the caller already opened.
// Address, DNS, and routes stay with the caller. The Windows service uses
// this with a named pipe of IP packets in front of Wintun.
func StartTUNIO(ifc tunIO, cidr string, d transport.Dialer) (*Device, error) {
	if cidr == "" {
		cidr = OverlayCIDR(transport.OverlayIP(), "")
	}
	ip, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		ifc.Close()
		return nil, err
	}
	live := d
	if l, ok := d.(interface{ Live() transport.Dialer }); ok {
		if inner := l.Live(); inner != nil {
			live = inner
		}
	}
	if _, ok := live.(*L3Net); ok {
		dev, err := openDevice(ifc, nil, nil, false, nil, true)
		if err != nil {
			return nil, err
		}
		dev.Attach(d)
		return dev, nil
	}
	if _, ok := ipPumpOf(d); ok {
		dev, err := openDevice(ifc, nil, nil, false, nil, true)
		if err != nil {
			return nil, err
		}
		dev.Attach(d)
		return dev, nil
	}
	st, err := newStack(ifc, d, ip, ipnet)
	if err != nil {
		ifc.Close()
		return nil, err
	}
	return openDevice(ifc, st, nil, false, nil, true)
}

// AttachTUN opens an existing TUN by name and runs the userspace stack on it.
// The kernel address, MTU, DNS, and routes stay with the caller (ConnMan,
// NetworkManager). Same packet path as StartTUN.
func AttachTUN(device, cidr string, d transport.Dialer) (*Device, error) {
	return openTUN(device, cidr, false, d, nil, true)
}

// openTUN is the shared body of StartTUN and AttachTUN. It opens a TUN with
// the given name (or a system-chosen one for "" and "auto"), configures it
// unless noExec is set, and picks the packet path: an L3 front gets raw
// packet copying, any other front gets the userspace stack. On any error
// everything set up so far is undone.
//
// Steps:
//  1. Open the TUN interface.
//  2. Unless noExec (the system's network manager owns the interface), give
//     it the overlay address and the extra routes the node announced.
//  3. Look through a transport.Holder (which swaps fronts on reconnect) to
//     the current front. L3 fronts take IP packets as they are, so no
//     userspace stack is needed.
//  4. Any other front carries mux streams: run the userspace TCP/IP stack
//     that turns packets into streams.
func openTUN(device, cidr string, killSwitch bool, d transport.Dialer, exclude []string, noExec bool) (*Device, error) {
	if cidr == "" {
		cidr = OverlayCIDR(transport.OverlayIP(), "")
	}
	cfg := water.Config{DeviceType: water.TUN}
	if device != "" && device != "auto" {
		setWaterName(&cfg, device)
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
	var extraUndo func()
	if !noExec {
		if err := configureTUN(ifc.Name(), ip, ipnet); err != nil {
			ifc.Close()
			return nil, err
		}
		extraUndo = addExtraCIDRs(ifc.Name(), ipnet, transport.OverlayRoutes())
	}
	live := d
	if l, ok := d.(interface{ Live() transport.Dialer }); ok {
		if inner := l.Live(); inner != nil {
			live = inner
		}
	}
	if _, ok := live.(*L3Net); ok {
		dev, err := openDevice(ifc, nil, extraUndo, killSwitch, exclude, noExec)
		if err != nil {
			return nil, err
		}
		dev.Attach(d)
		return dev, nil
	}
	if _, ok := ipPumpOf(d); ok {
		dev, err := openDevice(ifc, nil, extraUndo, killSwitch, exclude, noExec)
		if err != nil {
			return nil, err
		}
		dev.Attach(d)
		return dev, nil
	}
	st, err := newStack(ifc, d, ip, ipnet)
	if err != nil {
		if extraUndo != nil {
			extraUndo()
		}
		ifc.Close()
		return nil, err
	}
	return openDevice(ifc, st, extraUndo, killSwitch, exclude, noExec)
}

// openDevice wraps an opened TUN into a Device. Unless noExec is set it also
// sends traffic into the TUN: either a kill-switch (traffic cannot leave
// outside the tunnel) or just the split default routes, then the overlay
// DNS. All of it is collected into one undo that Device.Close runs. If the
// kill-switch cannot be set, everything is torn down and an error returned,
// because silently running without it would leak traffic.
func openDevice(ifc tunIO, st *tunStack, extraUndo func(), killSwitch bool, exclude []string, noExec bool) (*Device, error) {
	if noExec {
		return &Device{ts: st, ifc: ifc}, nil
	}
	var undo func()
	var err error
	if killSwitch {
		undo, err = enableKillSwitch(ifc.Name(), exclude)
		if err != nil {
			if extraUndo != nil {
				extraUndo()
			}
			if st != nil {
				st.Close()
			}
			ifc.Close()
			return nil, fmt.Errorf("kill-switch: %w", err)
		}
	} else {
		undo, _ = addSplitDefault(ifc.Name(), exclude)
	}
	dnsUndo := applyDNS(ifc.Name(), transport.OverlayDNS())
	undo = chainUndo(undo, dnsUndo, extraUndo)
	return &Device{ts: st, ifc: ifc, undo: undo}, nil
}

// chainUndo runs undos in reverse order. Nil funcs are skipped.
func chainUndo(fns ...func()) func() {
	var live []func()
	for _, f := range fns {
		if f != nil {
			live = append(live, f)
		}
	}
	if len(live) == 0 {
		return nil
	}
	return func() {
		for i := len(live) - 1; i >= 0; i-- {
			live[i]()
		}
	}
}

// addExtraCIDRs installs Status managed routes on the TUN (not the overlay prefix).
func addExtraCIDRs(name string, overlay *net.IPNet, cidrs []string) func() {
	var added []string
	for _, c := range cidrs {
		_, n, err := net.ParseCIDR(c)
		if err != nil {
			continue
		}
		if overlay != nil && overlay.String() == n.String() {
			continue
		}
		if err := addHostRoute(name, c); err == nil {
			added = append(added, c)
		}
	}
	if len(added) == 0 {
		return nil
	}
	return func() {
		for i := len(added) - 1; i >= 0; i-- {
			delHostRoute(name, added[i])
		}
	}
}

// addHostRoute pins cidr on the TUN so lab LAN behind a member enters the overlay.
func addHostRoute(name, cidr string) error {
	switch runtime.GOOS {
	case "darwin":
		return run("route", "-n", "add", "-inet", cidr, "-interface", name)
	case "linux":
		return run("ip", "route", "add", cidr, "dev", name)
	default:
		return fmt.Errorf("tun: os %s", runtime.GOOS)
	}
}

// delHostRoute undoes addHostRoute.
func delHostRoute(name, cidr string) {
	switch runtime.GOOS {
	case "darwin":
		_ = run("route", "-n", "delete", "-inet", cidr)
	case "linux":
		_ = run("ip", "route", "del", cidr, "dev", name)
	}
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

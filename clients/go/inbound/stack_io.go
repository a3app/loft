package inbound

// Packet pipe as a TUN: mobile Packet Tunnel / VpnService pumps IP here.

import (
	"io"
	"net"

	"github.com/a3app/loft/internal/transport"
)

// namedIO is a ReadWriteCloser with a log name (no kernel TUN).
type namedIO struct {
	io.ReadWriteCloser
	name string
}

// Name is the interface name for tun_stack logs.
func (n namedIO) Name() string { return n.name }

// StartStack binds an IP packet pipe. Mux fronts get gVisor; l3 splices IP.
// overlayIP is Status (loft) or the synthetic VLESS address. No OS routes.
func StartStack(rw io.ReadWriteCloser, name string, d transport.Dialer, overlayIP string) (*Device, error) {
	if name == "" {
		name = "pkt"
	}
	dev := namedIO{ReadWriteCloser: rw, name: name}
	live := d
	if l, ok := d.(interface{ Live() transport.Dialer }); ok {
		if inner := l.Live(); inner != nil {
			live = inner
		}
	}
	if _, ok := live.(*L3Net); ok {
		out := &Device{ifc: dev}
		out.Attach(d)
		return out, nil
	}
	if _, ok := ipPumpOf(d); ok {
		out := &Device{ifc: dev}
		out.Attach(d)
		return out, nil
	}
	cidr := OverlayCIDR(overlayIP, "")
	ip, ipnet, err := net.ParseCIDR(cidr)
	if err != nil {
		_ = rw.Close()
		return nil, err
	}
	st, err := newStack(dev, d, ip, ipnet)
	if err != nil {
		_ = rw.Close()
		return nil, err
	}
	return &Device{ts: st, ifc: dev}, nil
}

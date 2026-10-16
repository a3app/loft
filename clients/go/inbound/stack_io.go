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

// StartStack binds the gVisor userspace stack to an IP packet pipe.
// overlayIP is Status (loft) or the synthetic VLESS address. No OS routes.
func StartStack(rw io.ReadWriteCloser, name string, d transport.Dialer, overlayIP string) (*Device, error) {
	if name == "" {
		name = "pkt"
	}
	dev := namedIO{ReadWriteCloser: rw, name: name}
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

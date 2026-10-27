package inbound

// tun_l3.go is the TUN path for L3 fronts. A front is one way of carrying
// loft traffic to the node over HTTPS; the L3 front carries whole IP packets
// instead of mux streams. With it the TUN skips the userspace TCP stack
// (tun_stack.go) and two goroutines just copy packets both ways.

import (
	"sync"

	"github.com/a3app/loft/internal/transport"
)

// l3Pump copies IP packets between a TUN and an L3 front (no gVisor).
type l3Pump struct {
	stop chan struct{}
	once sync.Once
}

// attachL3 switches the device to raw packet copying through pump p. Any
// earlier pump is stopped first, so after a reconnect only the new front
// receives packets.
func (d *Device) attachL3(p transport.IPPump) {
	if d == nil || d.ifc == nil || p == nil {
		return
	}
	d.muStopL3()
	stop := make(chan struct{})
	d.l3 = &l3Pump{stop: stop}
	go pumpTUNToL3(d.ifc, p, stop)
	go pumpL3ToTUN(d.ifc, p, stop)
}

// muStopL3 stops the current L3 pump, if any, and forgets it.
func (d *Device) muStopL3() {
	if d.l3 != nil {
		d.l3.Stop()
		d.l3 = nil
	}
}

// Stop ends both copy goroutines.
func (p *l3Pump) Stop() {
	if p == nil {
		return
	}
	p.once.Do(func() { close(p.stop) })
}

// pumpTUNToL3 reads packets from the TUN and writes each one to the front.
// Each packet is copied because the read buffer is reused. It returns when
// the TUN read fails or stop is closed.
func pumpTUNToL3(ifc tunIO, p transport.IPPump, stop <-chan struct{}) {
	buf := make([]byte, 65535)
	for {
		n, err := ifc.Read(buf)
		if err != nil {
			return
		}
		select {
		case <-stop:
			return
		default:
		}
		if n == 0 {
			continue
		}
		_ = p.WriteIP(append([]byte(nil), buf[:n]...))
	}
}

// pumpL3ToTUN reads packets from the front and writes them into the TUN,
// so the operating system receives the replies. It returns when the front
// read fails or stop is closed.
func pumpL3ToTUN(ifc tunIO, p transport.IPPump, stop <-chan struct{}) {
	for {
		ip, err := p.ReadIP()
		if err != nil {
			return
		}
		select {
		case <-stop:
			return
		default:
		}
		if len(ip) == 0 {
			continue
		}
		_, _ = ifc.Write(ip)
	}
}

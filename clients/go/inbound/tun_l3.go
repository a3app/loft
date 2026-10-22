package inbound

import (
	"sync"

	"github.com/a3app/loft/internal/transport"
)

// l3Pump copies IP packets between a TUN and an L3 front (no gVisor).
type l3Pump struct {
	stop chan struct{}
	once sync.Once
}

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

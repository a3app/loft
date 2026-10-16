package transport

// UDP dest splice: when decoy is on, UDP/443 is dest's datagrams, not loft QUIC.

import (
	"net"
	"sync"
	"time"
)

const udpFlowIdle = 60 * time.Second

type udpFlow struct {
	up   *net.UDPConn
	from net.Addr
}

// SpliceUDP relays every datagram on listen to dest and dest replies to the
// client. A probe that speaks QUIC to the node IP sees the origin, not loft.
func SpliceUDP(listen, dest string) error {
	if dest == "" {
		return nil
	}
	_, addr := splitDecoy(dest)
	pc, err := net.ListenPacket("udp", listen)
	if err != nil {
		return err
	}
	defer pc.Close()
	upAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return err
	}
	var mu sync.Mutex
	flows := map[string]*udpFlow{}
	buf := make([]byte, 64<<10)
	for {
		n, from, err := pc.ReadFrom(buf)
		if err != nil {
			return err
		}
		key := from.String()
		mu.Lock()
		f := flows[key]
		if f == nil {
			up, derr := net.DialUDP("udp", nil, upAddr)
			if derr != nil {
				mu.Unlock()
				continue
			}
			f = &udpFlow{up: up, from: from}
			flows[key] = f
			go relayUDPReply(pc, f, func() {
				mu.Lock()
				if cur := flows[key]; cur == f {
					delete(flows, key)
				}
				mu.Unlock()
				_ = up.Close()
			})
		}
		mu.Unlock()
		_, _ = f.up.Write(buf[:n])
	}
}

func relayUDPReply(pc net.PacketConn, f *udpFlow, done func()) {
	defer done()
	b := make([]byte, 64<<10)
	for {
		_ = f.up.SetReadDeadline(time.Now().Add(udpFlowIdle))
		n, err := f.up.Read(b)
		if err != nil {
			return
		}
		if _, err := pc.WriteTo(b[:n], f.from); err != nil {
			return
		}
	}
}

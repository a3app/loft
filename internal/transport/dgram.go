package transport

// CmdDgram path: dest:1 inside the overlay CIDR is switched ICMP
// (FilterDgram, sync DeliverICMP so GET can return the echo-reply in the
// same request). dest:1 elsewhere is internet ICMP (async). gateway:53 is
// DNS. Remaining datagrams are internet UDP.

import (
	"net"

	"github.com/a3app/loft/internal/mux"
)

// overlayICMPPort is CmdDgram dest:1 — IP proto ICMP, IPv4 packet as payload.
const overlayICMPPort = "1"

// hookServerDgrams answers dest:1 inside mux.Push (FilterDgram) so GET
// pullDownlink can return the overlay echo-reply in the same request.
// Internet ICMP is started here but completes asynchronously.
func hookServerDgrams(ms *mux.Session) {
	if ms == nil {
		return
	}
	ms.FilterDgram(func(addr string, data []byte) bool {
		host, port, err := net.SplitHostPort(addr)
		if err != nil || port != overlayICMPPort {
			return false
		}
		if NetSwitch != nil && NetSwitch.Contains(host) {
			NetSwitch.DeliverICMP(ms, data)
			return true
		}
		DeliverInternetICMP(ms, data)
		return true
	})
}

// serveDgrams is the node's UDP relay: each CmdDgram is one UDP packet.
// dst:1 inside the overlay CIDR is switched ICMP. dst:1 elsewhere is a
// real ICMP echo from the node, not internet UDP port 1.
func serveDgrams(ms *mux.Session) {
	hookServerDgrams(ms)
	pc, err := net.ListenPacket("udp", ":0")
	if err != nil {
		return
	}
	defer pc.Close()
	go func() {
		buf := make([]byte, 65535)
		for {
			n, addr, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			if ua, ok := addr.(*net.UDPAddr); ok {
				if v4 := ua.IP.To4(); v4 != nil {
					addr = &net.UDPAddr{IP: v4, Port: ua.Port}
				}
			}
			_ = ms.WriteDgram(addr.String(), buf[:n])
		}
	}()
	for {
		addr, data, err := ms.ReadDgram()
		if err != nil {
			return
		}
		host, port, err := net.SplitHostPort(addr)
		if err == nil && port == overlayICMPPort {
			if NetSwitch != nil && NetSwitch.Contains(host) {
				NetSwitch.DeliverICMP(ms, data)
			} else {
				DeliverInternetICMP(ms, data)
			}
			continue
		}
		if err == nil && port == dnsPort && NetSwitch != nil && NetSwitch.IsDNS(host) {
			NetSwitch.DeliverDNS(ms, addr, data)
			continue
		}
		uaddr, err := net.ResolveUDPAddr("udp", addr)
		if err != nil {
			continue
		}
		_, _ = pc.WriteTo(data, uaddr)
	}
}

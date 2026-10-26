package transport

// CmdDgram path: dest:1 inside the overlay CIDR is switched ICMP
// (FilterDgram, sync DeliverICMP so GET can return the echo-reply in the
// same request). dest:1 elsewhere is internet ICMP (async). gateway:53 is
// DNS (FilterDgram starts DeliverDNS so GET does not wait on ReadDgram).
// Remaining datagrams are internet UDP.
//
// A Dgram frame carries one datagram inside the mux, addressed "host:port".
// UDP is the normal case. Port 1 is borrowed to mean "this payload is an
// ICMP packet" (1 is the IP protocol number of ICMP), so ping works through
// fronts that can only carry datagrams. The overlay gateway on port 53 is
// the node's DNS resolver for members.

import (
	"net"
	"strings"

	"github.com/a3app/loft/internal/egress"
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
		if err != nil {
			return false
		}
		if port == overlayICMPPort {
			if o := NetSwitch(); o != nil && (o.Contains(host) || o.Routed(host) != "") {
				o.DeliverICMP(ms, data)
				return true
			}
			DeliverInternetICMP(ms, data)
			return true
		}
		// GET pullHold is 80ms; DoH is slower. Start resolve at Push so the
		// next pad can carry the reply. Phone TUN also stubs gateway:53.
		if port == dnsPort {
			if o := NetSwitch(); o != nil && o.IsDNS(host) {
				go o.DeliverDNS(ms, addr, data)
				return true
			}
		}
		return false
	})
}

// serveDgrams is the node's UDP relay: each CmdDgram is one UDP packet.
// dst:1 inside the overlay CIDR is switched ICMP. dst:1 elsewhere is a
// real ICMP echo from the node, not internet UDP port 1.
// Without exit rules, all of a session's internet UDP goes out of one node
// socket and replies come back on it; with exit rules, an egress.UDP is
// made for the member and remade when the rules are reloaded.
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
	var eu *egress.UDP
	var eur *egress.Router
	defer func() {
		if eu != nil {
			eu.Close()
		}
	}()
	for {
		addr, data, err := ms.ReadDgram()
		if err != nil {
			return
		}
		host, port, err := net.SplitHostPort(addr)
		if err == nil && port == overlayICMPPort {
			if o := NetSwitch(); o != nil && (o.Contains(host) || o.Routed(host) != "") {
				o.DeliverICMP(ms, data)
			} else {
				DeliverInternetICMP(ms, data)
			}
			continue
		}
		if o := NetSwitch(); err == nil && port == dnsPort && o != nil && o.IsDNS(host) {
			o.DeliverDNS(ms, addr, data)
			continue
		}
		// Same drop as CONNECT: loopback, link-local, metadata names. No
		// lookup per datagram; the resolved address is checked on send.
		if err != nil {
			continue
		}
		if denied, _ := spliceNameDenied(strings.ToLower(strings.TrimSuffix(host, "."))); denied {
			continue
		}
		if r := Egress.Load(); r != nil {
			// Exit rules: the session's datagrams go the way its member's rules say.
			if eu == nil || eur != r {
				if eu != nil {
					eu.Close()
				}
				eu, eur = r.NewUDP(whoOf(ms), func(from string, b []byte) { _ = ms.WriteDgram(from, b) }), r
			}
			eu.Send(addr, data)
			continue
		}
		uaddr, err := net.ResolveUDPAddr("udp", addr)
		if err != nil || spliceIPDenied(uaddr.IP) {
			continue
		}
		_, _ = pc.WriteTo(data, uaddr)
	}
}

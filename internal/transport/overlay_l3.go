package transport

// L3 overlay switch: gateway ICMP, member IP, internet NAT.
//
// An L3 session carries whole IP packets instead of mux streams. The node
// switches each packet: a ping to the gateway is answered here, a packet to
// another member's overlay IP is passed to that member, and anything else
// leaves through the node's NAT to the internet (l3inet.go).

import (
	"encoding/binary"
	"net"
	"strconv"
)

// DeliverL3 switches one IPv4 datagram from an L3 session.
// The source address is forced to the session's lease, so a member cannot
// send packets as another overlay IP. Inside the overlay CIDR the same ACL
// as for mux streams applies; the routed-subnet lookup is done only for
// destinations inside the overlay CIDR. Outside it, local-only addresses
// and addresses the lists drop are discarded before NAT.
func (o *Overlay) DeliverL3(from l3Sink, pkt []byte) {
	if o == nil || o.N() == nil || from == nil || len(pkt) < 20 {
		return
	}
	if pkt[0]>>4 != 4 {
		return
	}
	ihl := int(pkt[0]&0x0f) * 4
	if ihl < 20 || ihl > len(pkt) {
		return
	}
	src := net.IP(pkt[12:16]).String()
	dstIP := net.IP(pkt[16:20]).To4()
	if dstIP == nil {
		return
	}
	dst := dstIP.String()
	o.mu.Lock()
	srcP := o.byL3[from]
	dstP := o.tab[dst]
	o.mu.Unlock()
	if srcP == nil {
		return
	}
	if srcP.ip != src {
		sip := net.ParseIP(srcP.ip)
		if sip == nil || sip.To4() == nil {
			return
		}
		pkt = rewriteIPv4Src(pkt, sip)
		src = srcP.ip
	}
	proto, dport := l3ProtoPort(pkt, ihl)
	p := o.packet(srcP, dstIP, dstP, proto, dport, false)
	if o.Contains(dst) {
		if dst == o.N().Gateway {
			if pkt[9] == ipProtoICMP {
				reply, ok := icmpEchoSwap(pkt)
				if ok {
					_ = from.WriteIP(reply)
				}
			}
			return
		}
		if dstP == nil {
			if via := o.Routed(dst); via != "" {
				o.mu.Lock()
				viaP := o.tab[via]
				o.mu.Unlock()
				p = o.packet(srcP, dstIP, viaP, proto, dport, true)
				if !o.N().Allow(p) || viaP == nil {
					return
				}
				l3Emit(viaP, pkt)
				return
			}
		}
		if !o.N().Allow(p) {
			return
		}
		if dstP == nil {
			return
		}
		l3Emit(dstP, pkt)
		return
	}
	if LookupAddr("", dstIP) == ActDrop {
		return
	}
	if dstIP.IsLoopback() || dstIP.IsMulticast() || dstIP.IsUnspecified() || dstIP.IsLinkLocalUnicast() {
		return
	}
	l3Inet(o.l3Reply(srcP.ip), o.whoMember(srcP.member), net.ParseIP(srcP.ip).To4(), pkt)
}

// l3Reply looks up the live L3 sink by overlay IP so NAT survives CONNECT replace.
func (o *Overlay) l3Reply(ip string) func([]byte) error {
	return func(pkt []byte) error {
		return o.WriteL3(ip, pkt)
	}
}

// l3ProtoPort is the protocol name and destination port of pkt, for ACL
// checks. ICMP uses the pseudo-port 1, as Dgram frames do.
func l3ProtoPort(pkt []byte, ihl int) (proto, dport string) {
	switch pkt[9] {
	case 1:
		return "icmp", overlayICMPPort
	case 6:
		proto = "tcp"
	case 17:
		proto = "udp"
	default:
		return "ip", ""
	}
	if len(pkt) >= ihl+4 {
		dport = strconv.Itoa(int(binary.BigEndian.Uint16(pkt[ihl+2 : ihl+4])))
	}
	return proto, dport
}

// l3Emit hands pkt to a member session. An L3 peer gets the packet as is.
// A mux peer can take only ICMP (as a Dgram to port 1); other packets to a
// mux peer are dropped here.
func l3Emit(p *ovPeer, pkt []byte) {
	if p == nil {
		return
	}
	if p.l3 != nil {
		_ = p.l3.WriteIP(pkt)
		return
	}
	if p.sess != nil && len(pkt) >= 20 && pkt[9] == ipProtoICMP {
		_ = p.sess.WriteDgram(net.JoinHostPort(p.ip, overlayICMPPort), pkt)
	}
}

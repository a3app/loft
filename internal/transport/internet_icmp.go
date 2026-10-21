package transport

// Internet ICMP: CmdDgram dest:1 outside the overlay CIDR is emitted as a
// real echo from the node; the reply returns as CmdDgram session-ip:1, async.

import (
	"encoding/binary"
	"net"
	"time"

	"golang.org/x/net/icmp"

	"github.com/a3app/loft/internal/mux"
)

// internetICMPSem caps concurrent internet echoes; internetICMPPing is the emit hook (tests replace it).
var (
	internetICMPSem  = make(chan struct{}, 16)
	internetICMPPing = pingInternetEcho
)

const internetICMPWait = 3 * time.Second

// DeliverInternetICMP emits a real ICMP echo from the node and returns the
// reply to the session as CmdDgram dst:1. Overlay dests stay in DeliverICMP.
func DeliverInternetICMP(from *mux.Session, pkt []byte) {
	if from == nil || len(pkt) < 28 || pkt[0]>>4 != 4 || pkt[9] != ipProtoICMP {
		return
	}
	ihl := int(pkt[0]&0x0f) * 4
	if ihl < 20 || len(pkt) < ihl+8 || pkt[ihl] != icmpEcho {
		return
	}
	dst := net.IP(pkt[16:20]).To4()
	if dst == nil || dst.IsLoopback() || dst.IsMulticast() || dst.IsUnspecified() || dst.IsLinkLocalUnicast() {
		return
	}
	if LookupAddr("", dst) == ActDrop {
		return
	}
	replyTo := net.IP(pkt[12:16]).To4()
	if o := NetSwitch(); o != nil {
		ip := o.SessionIP(from)
		if ip == "" {
			return
		}
		replyTo = net.ParseIP(ip).To4()
	}
	if replyTo == nil {
		return
	}
	echo := append([]byte(nil), pkt[ihl:]...)
	go func() {
		select {
		case internetICMPSem <- struct{}{}:
			defer func() { <-internetICMPSem }()
		default:
			return
		}
		body, src, err := internetICMPPing(dst, echo)
		body = restoreICMPIdent(stripIPv4(body), echo)
		if err != nil || len(body) < 8 || body[0] != icmpReply {
			return
		}
		if src == nil {
			src = dst
		}
		out := encapsulateIPv4(src, replyTo, ipProtoICMP, body)
		_ = from.WriteDgram(net.JoinHostPort(replyTo.String(), overlayICMPPort), out)
	}()
}

// encapsulateIPv4 builds a 20-byte IPv4 header around payload (no options).
func encapsulateIPv4(src, dst net.IP, proto byte, payload []byte) []byte {
	src4, dst4 := src.To4(), dst.To4()
	if src4 == nil || dst4 == nil {
		return nil
	}
	ihl := 20
	pkt := make([]byte, ihl+len(payload))
	pkt[0] = 0x45
	binary.BigEndian.PutUint16(pkt[2:4], uint16(len(pkt)))
	pkt[8] = 64
	pkt[9] = proto
	copy(pkt[12:16], src4)
	copy(pkt[16:20], dst4)
	binary.BigEndian.PutUint16(pkt[10:12], ipChecksum(pkt[:ihl]))
	copy(pkt[ihl:], payload)
	return pkt
}

// pingInternetEcho writes echo on ip4:icmp (or UDP ICMP) and reads one reply.
func pingInternetEcho(dst net.IP, echo []byte) ([]byte, net.IP, error) {
	c, err := icmp.ListenPacket("ip4:icmp", "0.0.0.0")
	if err != nil {
		c, err = icmp.ListenPacket("udp4", "0.0.0.0")
		if err != nil {
			return nil, nil, err
		}
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(internetICMPWait))
	if err := writeICMP(c, echo, dst); err != nil {
		return nil, nil, err
	}
	buf := make([]byte, 1500)
	n, peer, err := c.ReadFrom(buf)
	if err != nil {
		return nil, nil, err
	}
	src := addrIP(peer)
	if src == nil {
		src = dst
	}
	return buf[:n], src, nil
}

// Linux SOCK_DGRAM IPPROTO_ICMP overwrites the identifier. The client's
// ping socket (and Darwin utun NAT) match on the original id+seq.
func restoreICMPIdent(reply, echo []byte) []byte {
	if len(reply) < 8 || len(echo) < 8 {
		return reply
	}
	out := append([]byte(nil), reply...)
	copy(out[4:8], echo[4:8])
	binary.BigEndian.PutUint16(out[2:4], 0)
	binary.BigEndian.PutUint16(out[2:4], ipChecksum(out))
	return out
}

// stripIPv4 drops an outer IPv4 header when b is an ICMP-in-IP packet.
func stripIPv4(b []byte) []byte {
	if len(b) < 20 || b[0]>>4 != 4 {
		return b
	}
	ihl := int(b[0]&0x0f) * 4
	if ihl < 20 || ihl >= len(b) || b[9] != ipProtoICMP {
		return b
	}
	return b[ihl:]
}

// writeICMP sends echo as UDPAddr first (SOCK_DGRAM ICMP), then IPAddr.
func writeICMP(c net.PacketConn, echo []byte, dst net.IP) error {
	if _, err := c.WriteTo(echo, &net.UDPAddr{IP: dst}); err == nil {
		return nil
	}
	_, err := c.WriteTo(echo, &net.IPAddr{IP: dst})
	return err
}

// addrIP extracts an IP from TCPAddr, UDPAddr, IPAddr, or host:port.
func addrIP(a net.Addr) net.IP {
	switch v := a.(type) {
	case *net.TCPAddr:
		return v.IP
	case *net.UDPAddr:
		return v.IP
	case *net.IPAddr:
		return v.IP
	default:
		host, _, err := net.SplitHostPort(a.String())
		if err != nil {
			return net.ParseIP(a.String())
		}
		return net.ParseIP(host)
	}
}

package transport

// Internet ICMP: CmdDgram dest:1 outside the overlay CIDR is emitted as a
// real echo from the node; the reply returns as CmdDgram session-ip:1, async.
//
// This makes "ping 8.8.8.8" work for a member: the client puts the ICMP echo
// packet in a Dgram frame, the node sends a real echo from its own address,
// and wraps the reply in a fresh IPv4 header addressed to the member's
// overlay IP. Only echo requests are sent, never other ICMP types.

import (
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"time"

	"golang.org/x/net/icmp"

	"github.com/a3app/loft/internal/egress"
	"github.com/a3app/loft/internal/mux"
)

// internetICMPSem caps concurrent internet echoes; internetICMPPing is the emit hook (tests replace it).
var (
	internetICMPSem  = make(chan struct{}, 16)
	internetICMPPing = pingInternetEcho
)

// internetICMPWait is how long one echo waits for its reply.
const internetICMPWait = 3 * time.Second

// DeliverInternetICMP emits a real ICMP echo from the node and returns the
// reply to the session as CmdDgram dst:1. Overlay dests stay in DeliverICMP.
// Local-only destinations and addresses the lists drop are ignored. The
// reply goes to the session's overlay IP, not to the source written in the
// packet, so a member cannot aim replies at someone else. When 16 echoes are
// already in flight, new ones are dropped rather than queued.
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
	if LookupAddr("", dst) == ActDrop || !icmpOut(whoOf(from), dst) {
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

// pingInternetEcho sends one echo to dst and waits for its reply.
//
// Two kinds of socket can send an ICMP echo without being a full raw-packet
// program, and the order matters:
//
//  1. The Linux "ping socket" (udp4 here, SOCK_DGRAM + IPPROTO_ICMP). Any
//     user may open it when their group is inside
//     net.ipv4.ping_group_range; the installer puts the loft group there.
//     The kernel hands this socket only the replies to its own echoes, so
//     concurrent pings from many members cannot mix up.
//  2. A raw ip4:icmp socket. It needs root or CAP_NET_RAW, and it receives
//     EVERY ICMP packet that reaches the host: other members' replies,
//     pings from scanners, "unreachable" errors. So it is only the fallback,
//     and its replies are checked one by one (see readEchoReply).
//
// If neither can be opened, the echo is dropped; the node says so once at
// start (InternetPingReady), not on every ping.
func pingInternetEcho(dst net.IP, echo []byte) ([]byte, net.IP, error) {
	raw := false
	c, err := icmp.ListenPacket("udp4", "0.0.0.0")
	if err != nil {
		c, err = icmp.ListenPacket("ip4:icmp", "0.0.0.0")
		if err != nil {
			return nil, nil, err
		}
		raw = true
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(internetICMPWait))
	if err := writeICMP(c, echo, dst); err != nil {
		return nil, nil, err
	}
	return readEchoReply(c, dst, echo, raw)
}

// readEchoReply reads from c until the reply to echo arrives from dst, or
// the socket deadline passes. Everything else is skipped:
//   - packets from another address (someone else's ping, a scanner);
//   - ICMP that is not an echo reply (errors, echo requests to the host);
//   - replies with another sequence number (an older echo of this member);
//   - on a raw socket also replies with another identifier: they belong to
//     another member's ping that went out at the same time. (A ping socket
//     gets its identifier rewritten by the kernel, and the kernel already
//     filtered by it, so there only the sequence number is compared.)
func readEchoReply(c net.PacketConn, dst net.IP, echo []byte, raw bool) ([]byte, net.IP, error) {
	if len(echo) < 8 {
		return nil, nil, errShortEcho
	}
	buf := make([]byte, 1500)
	for {
		n, peer, err := c.ReadFrom(buf)
		if err != nil {
			return nil, nil, err // the deadline ends the wait
		}
		src := addrIP(peer)
		if src == nil || !src.Equal(dst) {
			continue
		}
		body := stripIPv4(buf[:n])
		if len(body) < 8 || body[0] != icmpReply {
			continue
		}
		if body[6] != echo[6] || body[7] != echo[7] {
			continue // sequence number
		}
		if raw && (body[4] != echo[4] || body[5] != echo[5]) {
			continue // identifier
		}
		return append([]byte(nil), buf[:n]...), src, nil
	}
}

// errShortEcho: an echo shorter than its 8-byte ICMP header.
var errShortEcho = errors.New("icmp: echo too short")

// InternetPingReady reports why this process cannot send ICMP echoes to the
// internet, or nil. loft-node logs it at start: without it "ping 8.8.8.8"
// through the node just times out and nobody can tell why.
func InternetPingReady() error {
	c, err := icmp.ListenPacket("udp4", "0.0.0.0")
	if err == nil {
		c.Close()
		return nil
	}
	c, rawErr := icmp.ListenPacket("ip4:icmp", "0.0.0.0")
	if rawErr == nil {
		c.Close()
		return nil
	}
	return fmt.Errorf("ping socket: %v; raw socket: %v", err, rawErr)
}

// restoreICMPIdent puts the client's original identifier and sequence
// number back into the reply and recomputes the checksum.
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

// icmpOut reports whether a member's ping to dst may leave the node. Exit
// rules carry only TCP and UDP, so a ping goes out only when the member's
// route to dst is direct; a block rule or an assigned channel stops it.
func icmpOut(who egress.Who, dst net.IP) bool {
	r := Egress.Load()
	return r == nil || r.DirectFor(who, dst.String())
}

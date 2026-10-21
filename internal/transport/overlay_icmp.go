package transport

// Overlay ICMP echo helpers: swap request to reply, rewrite Darwin utun dummy src.

import (
	"encoding/binary"
	"net"
)

// ipProtoICMP, icmpEcho, and icmpReply are IPv4 ICMP echo numbers.
const (
	ipProtoICMP = 1
	icmpEcho    = 8
	icmpReply   = 0
)

// icmpEchoSwap turns an IPv4 echo request into a reply (addrs swapped).
func icmpEchoSwap(pkt []byte) ([]byte, bool) {
	if len(pkt) < 28 || pkt[0]>>4 != 4 {
		return nil, false
	}
	ihl := int(pkt[0]&0x0f) * 4
	if ihl < 20 || len(pkt) < ihl+8 {
		return nil, false
	}
	if pkt[9] != ipProtoICMP || pkt[ihl] != icmpEcho {
		return nil, false
	}
	out := append([]byte(nil), pkt...)
	copy(out[12:16], pkt[16:20])
	copy(out[16:20], pkt[12:16])
	out[8] = 64
	binary.BigEndian.PutUint16(out[10:12], 0)
	binary.BigEndian.PutUint16(out[10:12], ipChecksum(out[:ihl]))
	out[ihl] = icmpReply
	binary.BigEndian.PutUint16(out[ihl+2:ihl+4], 0)
	binary.BigEndian.PutUint16(out[ihl+2:ihl+4], ipChecksum(out[ihl:]))
	return out, true
}

// rewriteIPv4Src stamps the session overlay IP and fixes the IP checksum.
// ICMP checksum does not cover addresses.
func rewriteIPv4Src(pkt []byte, src net.IP) []byte {
	ip4 := src.To4()
	if ip4 == nil || len(pkt) < 20 {
		return pkt
	}
	out := append([]byte(nil), pkt...)
	copy(out[12:16], ip4)
	ihl := int(out[0]&0x0f) * 4
	if ihl < 20 || ihl > len(out) {
		return out
	}
	binary.BigEndian.PutUint16(out[10:12], 0)
	binary.BigEndian.PutUint16(out[10:12], ipChecksum(out[:ihl]))
	return out
}

// ipChecksum is RFC 1071 ones-complement (IP and ICMP headers).
func ipChecksum(b []byte) uint16 {
	var s uint32
	for i := 0; i+1 < len(b); i += 2 {
		s += uint32(b[i])<<8 | uint32(b[i+1])
	}
	if len(b)%2 == 1 {
		s += uint32(b[len(b)-1]) << 8
	}
	for s > 0xffff {
		s = (s >> 16) + (s & 0xffff)
	}
	return ^uint16(s)
}

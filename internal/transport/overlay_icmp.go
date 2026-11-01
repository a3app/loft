package transport

// Overlay ICMP echo helpers: swap request to reply, rewrite Darwin utun dummy src.
// On macOS the utun interface sends overlay pings from a dummy link-local
// address (169.254.8.1), so the node puts the session's real overlay IP in
// the source before switching the packet.

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

// rewriteIPv4Src stamps the session overlay IP and fixes the checksums.
// The IP header checksum is recomputed. ICMP's checksum does not cover
// addresses; TCP's and UDP's do (through the pseudo-header), so theirs is
// updated for the new source, otherwise the receiving kernel drops the
// packet. That is done incrementally (RFC 1624), which also works for the
// first fragment of a datagram whose rest the node never sees.
func rewriteIPv4Src(pkt []byte, src net.IP) []byte {
	ip4 := src.To4()
	if ip4 == nil || len(pkt) < 20 {
		return pkt
	}
	out := append([]byte(nil), pkt...)
	var old [4]byte
	copy(old[:], out[12:16])
	copy(out[12:16], ip4)
	ihl := int(out[0]&0x0f) * 4
	if ihl < 20 || ihl > len(out) {
		return out
	}
	binary.BigEndian.PutUint16(out[10:12], 0)
	binary.BigEndian.PutUint16(out[10:12], ipChecksum(out[:ihl]))
	if binary.BigEndian.Uint16(out[6:8])&0x1fff != 0 {
		return out // a later fragment: no TCP/UDP header in it
	}
	var at int
	switch out[9] {
	case ipProtoTCP:
		at = ihl + 16
	case ipProtoUDP:
		at = ihl + 6
	default:
		return out
	}
	if at+2 > len(out) {
		return out
	}
	sum := binary.BigEndian.Uint16(out[at : at+2])
	if out[9] == ipProtoUDP && sum == 0 {
		return out // UDP without a checksum stays without one
	}
	sum = checksumReplace(sum, old[:], ip4)
	if out[9] == ipProtoUDP && sum == 0 {
		sum = 0xffff // 0 means "no checksum" in UDP
	}
	binary.BigEndian.PutUint16(out[at:at+2], sum)
	return out
}

// checksumReplace updates a ones-complement checksum for 16-bit words of
// from replaced by to (RFC 1624: HC' = ~(~HC + ~m + m')).
func checksumReplace(sum uint16, from, to []byte) uint16 {
	s := uint32(^sum)
	for i := 0; i+1 < len(from) && i+1 < len(to); i += 2 {
		s += uint32(^binary.BigEndian.Uint16(from[i:])) & 0xffff
		s += uint32(binary.BigEndian.Uint16(to[i:]))
	}
	for s > 0xffff {
		s = (s & 0xffff) + (s >> 16)
	}
	return ^uint16(s)
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

package sstp

// Inner MTU: LCP MRU, SSTP+TLS overhead cap (Windows WAN is 1400), ICMP PMTU.

import (
	"bufio"
	"encoding/binary"
	"net"
	"time"
)

const (
	sstpMinMTU  = 1280 // IPv6 minimum; known-good when a 1400 TLS record black-holes
	sstpSafeMTU = 1400 // Windows SSTP WAN miniport default
)

func sstpInnerMTU(pppInfo int) int {
	if pppInfo <= 0 {
		pppInfo = 1500
	}
	if pppInfo > sstpSafeMTU {
		pppInfo = sstpSafeMTU
	}
	if pppInfo < sstpMinMTU {
		pppInfo = sstpMinMTU
	}
	return pppInfo
}

// probeMTU is SoftEther/Windows-style: ping the PPP peer with DF.
// If the peer answers small echoes, binary-search up to hi.
// If ICMP is filtered, keep hi (do not drop to 1280 on silence).
func (s *Session) probeMTU(br *bufio.Reader, hi int) int {
	hi = sstpInnerMTU(hi)
	if s.IP.To4() == nil || s.Peer.To4() == nil {
		return hi
	}
	if !s.echoMTU(br, sstpMinMTU) {
		return hi
	}
	if s.echoMTU(br, hi) {
		return hi
	}
	lo, best := sstpMinMTU, sstpMinMTU
	for hi-lo > 16 {
		mid := (lo + hi + 1) / 2
		mid = mid / 8 * 8
		if mid <= lo {
			mid = lo + 8
		}
		if s.echoMTU(br, mid) {
			best = mid
			lo = mid
		} else {
			hi = mid - 8
		}
	}
	return best
}

func (s *Session) echoMTU(br *bufio.Reader, total int) bool {
	id := uint16(time.Now().UnixNano())
	seq := uint16(total)
	echo := icmpEchoDF(s.IP, s.Peer, id, seq, total)
	if len(echo) == 0 {
		return false
	}
	if err := s.writePkt(pkt{data: s.ppp.wrapIP(echo)}); err != nil {
		return false
	}
	deadline := time.Now().Add(400 * time.Millisecond)
	_ = s.conn.SetReadDeadline(deadline)
	defer func() { _ = s.conn.SetReadDeadline(time.Time{}) }()
	for time.Now().Before(deadline) {
		pk, err := readPkt(br)
		if err != nil {
			if ne, ok := err.(net.Error); ok && ne.Timeout() {
				return false
			}
			return false
		}
		if pk.ctrl {
			if pk.typ == msgEchoRequest {
				_ = s.writePkt(echoResponse())
			}
			continue
		}
		ip, ok := s.ppp.unwrapIP(pk.data)
		if !ok || len(ip) == 0 {
			_ = s.ppp.handle(pk.data)
			_ = s.flushPPP()
			continue
		}
		if icmpEchoReplyMatch(ip, s.IP, id, seq) {
			return true
		}
		select {
		case s.inbound <- ip:
		default:
		}
	}
	return false
}

func icmpEchoDF(src, dst net.IP, id, seq uint16, total int) []byte {
	if total < 28 {
		total = 28
	}
	s4, d4 := src.To4(), dst.To4()
	if s4 == nil || d4 == nil {
		return nil
	}
	b := make([]byte, total)
	b[0] = 0x45
	binary.BigEndian.PutUint16(b[2:4], uint16(total))
	binary.BigEndian.PutUint16(b[4:6], id)
	binary.BigEndian.PutUint16(b[6:8], 0x4000) // DF
	b[8] = 64
	b[9] = 1
	copy(b[12:16], s4)
	copy(b[16:20], d4)
	binary.BigEndian.PutUint16(b[10:12], inetSum(b[:20]))
	b[20] = 8
	binary.BigEndian.PutUint16(b[24:26], id)
	binary.BigEndian.PutUint16(b[26:28], seq)
	binary.BigEndian.PutUint16(b[22:24], inetSum(b[20:]))
	return b
}

func icmpEchoReplyMatch(ip []byte, our net.IP, id, seq uint16) bool {
	if len(ip) < 28 || ip[0]>>4 != 4 || ip[9] != 1 {
		return false
	}
	ihl := int(ip[0]&0xf) * 4
	if ihl < 20 || len(ip) < ihl+8 || ip[ihl] != 0 {
		return false
	}
	if our.To4() == nil || !net.IP(ip[16:20]).Equal(our.To4()) {
		return false
	}
	gotID := binary.BigEndian.Uint16(ip[ihl+4 : ihl+6])
	gotSeq := binary.BigEndian.Uint16(ip[ihl+6 : ihl+8])
	return gotID == id && gotSeq == seq
}

// ICMPNextHopMTU is RFC 1191: ICMP type 3 code 4, next-hop MTU at offset 6.
func ICMPNextHopMTU(ip []byte) int {
	if len(ip) < 28 || ip[0]>>4 != 4 || ip[9] != 1 {
		return 0
	}
	ihl := int(ip[0]&0xf) * 4
	if ihl < 20 || len(ip) < ihl+8 {
		return 0
	}
	if ip[ihl] != 3 || ip[ihl+1] != 4 {
		return 0
	}
	return int(binary.BigEndian.Uint16(ip[ihl+6 : ihl+8]))
}

func inetSum(b []byte) uint16 {
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

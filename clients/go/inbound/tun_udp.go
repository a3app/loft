package inbound

// tun_udp.go NATs Darwin dummy-src UDP onto overlay IPv4 and rebuilds replies.

import (
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/a3app/loft/internal/transport"
)

const ipProtoUDP = 17

var udpSeen atomic.Uint32

// udpFlow remembers a Darwin dummy (or other) UDP 4-tuple so replies can be rebuilt.
type udpFlow struct {
	src   [4]byte
	sport uint16
	seen  time.Time
}

// handleUDP sends unfragmented IPv4 UDP as mux datagrams and records NAT state.
func (t *tunStack) handleUDP(raw []byte) bool {
	if len(raw) < 28 || raw[9] != ipProtoUDP {
		return false
	}
	ihl := int(raw[0]&0x0f) * 4
	if ihl < 20 || len(raw) < ihl+8 {
		return false
	}
	if binary.BigEndian.Uint16(raw[6:8])&0x3fff != 0 {
		return false
	}
	dst := net.IP(raw[16:20])
	if transport.LookupAddr("", dst) == transport.ActDrop {
		return true
	}
	sport := binary.BigEndian.Uint16(raw[ihl : ihl+2])
	dport := binary.BigEndian.Uint16(raw[ihl+2 : ihl+4])
	ulen := int(binary.BigEndian.Uint16(raw[ihl+4 : ihl+6]))
	end := ihl + ulen
	if ulen < 8 || end > len(raw) {
		end = len(raw)
	}
	payload := raw[ihl+8 : end]
	src := net.IP(raw[12:16]).To4()
	dst4 := dst.To4()
	if src == nil || dst4 == nil {
		return true
	}
	dest := net.JoinHostPort(dst4.String(), strconv.Itoa(int(dport)))
	var s4 [4]byte
	copy(s4[:], src)
	t.rememberUDP(dest, udpFlow{src: s4, sport: sport, seen: time.Now()})
	if t.d == nil {
		return true
	}
	sess := t.d.Session()
	if sess == nil {
		return true
	}
	n := udpSeen.Add(1)
	if n <= 8 {
		fmt.Fprintf(os.Stderr, "tun udp dest=%s src=%s:%d n=%d\n", dest, net.IP(s4[:]), sport, len(payload))
	}
	_ = sess.WriteDgram(dest, payload)
	return true
}

// rememberUDP stores src/sport for dest, dropping idle and capping map size.
func (t *tunStack) rememberUDP(dest string, f udpFlow) {
	t.udpMu.Lock()
	defer t.udpMu.Unlock()
	if t.udpNat == nil {
		t.udpNat = make(map[string][]udpFlow)
	}
	now := time.Now()
	list := t.udpNat[dest]
	out := list[:0]
	found := false
	for _, x := range list {
		if now.Sub(x.seen) > udpIdle {
			continue
		}
		if x.src == f.src && x.sport == f.sport {
			x.seen = now
			out = append(out, x)
			found = true
			continue
		}
		out = append(out, x)
	}
	if !found {
		if len(out) >= 8 {
			out = out[1:]
		}
		out = append(out, f)
	}
	n := 0
	for _, l := range t.udpNat {
		n += len(l)
	}
	if n >= maxUDP {
		t.udpNat = map[string][]udpFlow{dest: {f}}
		return
	}
	t.udpNat[dest] = out
}

// canonHostPort maps IPv4-mapped IPv6 hosts to IPv4 in host:port.
func canonHostPort(addr string) string {
	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	if ip := net.ParseIP(host); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			host = v4.String()
		}
	}
	return net.JoinHostPort(host, port)
}

// onOverlayUDP rebuilds IPv4 UDP replies onto remembered TUN sources.
func (t *tunStack) onOverlayUDP(addr string, data []byte) bool {
	addr = canonHostPort(addr)
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return false
	}
	srcIP := net.ParseIP(host).To4()
	if srcIP == nil {
		return false
	}
	port, err := strconv.Atoi(portStr)
	if err != nil || port <= 0 || port > 65535 {
		return false
	}
	t.udpMu.Lock()
	flows := append([]udpFlow(nil), t.udpNat[addr]...)
	t.udpMu.Unlock()
	if len(flows) == 0 {
		return false
	}
	if t.ifc == nil {
		return true
	}
	for _, f := range flows {
		pkt := encodeUDP(srcIP, uint16(port), net.IP(f.src[:]), f.sport, data)
		if _, err := t.ifc.Write(pkt); err != nil {
			fmt.Fprintf(os.Stderr, "tun udp rx: %v\n", err)
		}
	}
	return true
}

// encodeUDP builds an IPv4 UDP packet with IP and UDP checksums.
func encodeUDP(src net.IP, sport uint16, dst net.IP, dport uint16, payload []byte) []byte {
	s4, d4 := src.To4(), dst.To4()
	if s4 == nil || d4 == nil {
		return nil
	}
	ihl := 20
	ulen := 8 + len(payload)
	pkt := make([]byte, ihl+ulen)
	pkt[0] = 0x45
	binary.BigEndian.PutUint16(pkt[2:4], uint16(len(pkt)))
	pkt[8] = 64
	pkt[9] = ipProtoUDP
	copy(pkt[12:16], s4)
	copy(pkt[16:20], d4)
	binary.BigEndian.PutUint16(pkt[10:12], checksum(pkt[:ihl]))
	binary.BigEndian.PutUint16(pkt[20:22], sport)
	binary.BigEndian.PutUint16(pkt[22:24], dport)
	binary.BigEndian.PutUint16(pkt[24:26], uint16(ulen))
	copy(pkt[28:], payload)
	sum := udpChecksum(s4, d4, pkt[20:20+ulen])
	binary.BigEndian.PutUint16(pkt[26:28], sum)
	return pkt
}

// udpChecksum is the IPv4 UDP checksum (0 is sent as 0xffff).
func udpChecksum(src, dst net.IP, udp []byte) uint16 {
	s4, d4 := src.To4(), dst.To4()
	var s uint32
	s += uint32(s4[0])<<8 | uint32(s4[1])
	s += uint32(s4[2])<<8 | uint32(s4[3])
	s += uint32(d4[0])<<8 | uint32(d4[1])
	s += uint32(d4[2])<<8 | uint32(d4[3])
	s += 17
	s += uint32(len(udp))
	for i := 0; i+1 < len(udp); i += 2 {
		s += uint32(udp[i])<<8 | uint32(udp[i+1])
	}
	if len(udp)%2 == 1 {
		s += uint32(udp[len(udp)-1]) << 8
	}
	for s > 0xffff {
		s = (s >> 16) + (s & 0xffff)
	}
	c := ^uint16(s)
	if c == 0 {
		c = 0xffff
	}
	return c
}

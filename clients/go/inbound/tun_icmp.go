package inbound

// tun_icmp.go answers overlay pings locally and forwards the rest as mux datagrams.

import (
	"encoding/binary"
	"fmt"
	"net"
	"os"
	"sync/atomic"

	"github.com/a3app/loft/internal/mux"
	"github.com/a3app/loft/internal/transport"
)

const (
	ipProtoICMP   = 1
	icmpEcho      = 8
	icmpReply     = 0
	icmpDgramPort = "1" // CmdDgram dest:1 carries an IPv4 ICMP packet
	// Darwin utun dummy p2p local. Kernel sources overlay pings from here.
	tunLocal = "169.254.8.1"
)

var icmpReplies atomic.Uint32

// icmpEchoReply answers only when dest is our overlay IP.
func icmpEchoReply(pkt []byte, our net.IP) ([]byte, bool) {
	pkt = stripUTUN(pkt)
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
	if our == nil || our.To4() == nil {
		return nil, false
	}
	if !net.IP(pkt[16:20]).Equal(our.To4()) {
		return nil, false
	}
	return icmpSwap(pkt)
}

// icmpSwap turns an echo request into an echo-reply by swapping IPv4 addrs.
func icmpSwap(pkt []byte) ([]byte, bool) {
	ihl := int(pkt[0]&0x0f) * 4
	out := append([]byte(nil), pkt...)
	copy(out[12:16], pkt[16:20])
	copy(out[16:20], pkt[12:16])
	out[8] = 64
	binary.BigEndian.PutUint16(out[10:12], 0)
	binary.BigEndian.PutUint16(out[10:12], checksum(out[:ihl]))
	out[ihl] = icmpReply
	binary.BigEndian.PutUint16(out[ihl+2:ihl+4], 0)
	binary.BigEndian.PutUint16(out[ihl+2:ihl+4], checksum(out[ihl:]))
	return out, true
}

// sameNet is true when our and dst share the first three IPv4 octets.
func sameNet(our, dst net.IP) bool {
	a, b := our.To4(), dst.To4()
	if a == nil || b == nil {
		return false
	}
	return a[0] == b[0] && a[1] == b[1] && a[2] == b[2]
}

// stripUTUN drops a 4-byte Darwin utun family header if present.
func stripUTUN(b []byte) []byte {
	if len(b) >= 5 && b[0] == 0 && b[1] == 0 && b[2] == 0 && b[3] <= 30 && b[4]>>4 == 4 {
		return b[4:]
	}
	return b
}

// checksum is the Internet checksum (ones' complement) used by IPv4 and ICMP.
func checksum(b []byte) uint16 {
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

// logICMP prints the first few local echo-replies to stderr.
func logICMP(pkt []byte) {
	n := icmpReplies.Add(1)
	if n > 8 {
		return
	}
	dst := net.IP(pkt[16:20]).String()
	fmt.Fprintf(os.Stderr, "tun icmp echo-reply %s\n", dst)
}

// Attach stores the dialer, overlay IP, and hooks mux datagrams for ICMP/UDP.
func (t *tunStack) Attach(d transport.Dialer) {
	t.d = d
	if ip := net.ParseIP(transport.OverlayIP()); ip != nil {
		t.our = ip.To4()
	}
	if d != nil {
		hookICMP(d.Session(), t)
	}
}

// inOverlay is true if ip is in the TUN prefix, else same /24 as our overlay IP.
func (t *tunStack) inOverlay(ip net.IP) bool {
	if t.net != nil && ip != nil {
		return t.net.Contains(ip)
	}
	return sameNet(t.our, ip)
}

// rewriteIPv4 copies pkt, optionally replaces src/dst, and fixes the IP checksum.
func rewriteIPv4(pkt []byte, src, dst net.IP) []byte {
	if len(pkt) < 20 {
		return pkt
	}
	out := append([]byte(nil), pkt...)
	if src4 := src.To4(); src4 != nil {
		copy(out[12:16], src4)
	}
	if dst4 := dst.To4(); dst4 != nil {
		copy(out[16:20], dst4)
	}
	ihl := int(out[0]&0x0f) * 4
	if ihl < 20 || ihl > len(out) {
		return out
	}
	binary.BigEndian.PutUint16(out[10:12], 0)
	binary.BigEndian.PutUint16(out[10:12], checksum(out[:ihl]))
	return out
}

// icmpKey is ICMP id<<16|seq used to restore Darwin dummy sources on reply.
func icmpKey(pkt []byte) (uint32, bool) {
	if len(pkt) < 28 || pkt[0]>>4 != 4 {
		return 0, false
	}
	ihl := int(pkt[0]&0x0f) * 4
	if ihl < 20 || len(pkt) < ihl+8 {
		return 0, false
	}
	id := binary.BigEndian.Uint16(pkt[ihl+4 : ihl+6])
	seq := binary.BigEndian.Uint16(pkt[ihl+6 : ihl+8])
	return uint32(id)<<16 | uint32(seq), true
}

// natOut rewrites Darwin dummy (or other) sources to overlay IP and remembers ICMP id+seq.
func (t *tunStack) natOut(pkt []byte) []byte {
	if t.our == nil || len(pkt) < 20 {
		return pkt
	}
	src := net.IP(pkt[12:16])
	if src.Equal(t.our) {
		return pkt
	}
	if k, ok := icmpKey(pkt); ok {
		var a [4]byte
		copy(a[:], src.To4())
		t.natMu.Lock()
		if t.nat == nil {
			t.nat = make(map[uint32][4]byte)
		}
		if len(t.nat) >= 64 {
			t.nat = make(map[uint32][4]byte)
		}
		t.nat[k] = a
		t.natMu.Unlock()
	}
	return rewriteIPv4(pkt, t.our, nil)
}

// natIn restores the original IPv4 source on matching echo-replies.
func (t *tunStack) natIn(pkt []byte) []byte {
	if t.our == nil || len(pkt) < 20 {
		return pkt
	}
	dst := net.IP(pkt[16:20])
	if !dst.Equal(t.our) {
		return pkt
	}
	k, ok := icmpKey(pkt)
	if !ok {
		return pkt
	}
	t.natMu.Lock()
	orig, hit := t.nat[k]
	if hit {
		delete(t.nat, k)
	}
	t.natMu.Unlock()
	if !hit {
		return pkt
	}
	return rewriteIPv4(pkt, nil, net.IP(orig[:]))
}

// forUs is true if ip is our overlay address or the Darwin utun dummy.
func (t *tunStack) forUs(ip net.IP) bool {
	if t.our != nil && ip.Equal(t.our) {
		return true
	}
	return ip.Equal(net.ParseIP(tunLocal))
}

// handleICMP answers pings to our overlay IP locally and forwards other echoes as datagrams.
func (t *tunStack) handleICMP(raw []byte) bool {
	if len(raw) < 28 || raw[9] != ipProtoICMP {
		return false
	}
	dst := net.IP(raw[16:20])
	if reply, ok := icmpEchoReply(raw, t.our); ok {
		logICMP(reply)
		if _, err := t.ifc.Write(reply); err != nil {
			fmt.Fprintf(os.Stderr, "tun icmp write: %v\n", err)
		}
		return true
	}
	if t.our != nil && t.inOverlay(dst) && !dst.Equal(t.our) {
		t.sendOverlayICMP(raw)
		return true
	}
	ihl := int(raw[0]&0x0f) * 4
	if ihl < 20 {
		ihl = 20
	}
	if len(raw) >= ihl+8 && raw[ihl] == icmpEcho {
		t.sendOverlayICMP(raw)
		return true
	}
	return false
}

// sendOverlayICMP NATs pkt and writes it as CmdDgram dest:1.
func (t *tunStack) sendOverlayICMP(pkt []byte) {
	if t.d == nil {
		return
	}
	sess := t.d.Session()
	dst := net.IP(pkt[16:20]).String()
	if sess == nil {
		n := icmpReplies.Add(1)
		if n <= 8 {
			fmt.Fprintf(os.Stderr, "tun icmp overlay %s: no session\n", dst)
		}
		return
	}
	out := t.natOut(pkt)
	n := icmpReplies.Add(1)
	if n <= 8 {
		fmt.Fprintf(os.Stderr, "tun icmp dest=%s src=%s\n", dst, net.IP(out[12:16]))
	}
	_ = sess.WriteDgram(net.JoinHostPort(dst, icmpDgramPort), out)
}

// onOverlayICMP consumes inbound ICMP datagrams: reply to echoes, else inject into TUN.
func (t *tunStack) onOverlayICMP(_ string, data []byte) bool {
	pkt := stripUTUN(data)
	if len(pkt) < 28 || pkt[0]>>4 != 4 || pkt[9] != ipProtoICMP {
		return false
	}
	ihl := int(pkt[0]&0x0f) * 4
	if ihl < 20 || len(pkt) < ihl+8 {
		return false
	}
	dst := net.IP(pkt[16:20])
	if !t.forUs(dst) {
		return false
	}
	if pkt[ihl] == icmpEcho {
		if t.our != nil && !dst.Equal(t.our) {
			pkt = rewriteIPv4(pkt, nil, t.our)
		}
		reply, ok := icmpSwap(pkt)
		if !ok || t.d == nil {
			return true
		}
		sess := t.d.Session()
		if sess == nil {
			return true
		}
		src := net.IP(pkt[12:16]).String()
		_ = sess.WriteDgram(net.JoinHostPort(src, icmpDgramPort), reply)
		return true
	}
	pkt = t.natIn(pkt)
	if t.ifc == nil {
		return true
	}
	if _, err := t.ifc.Write(pkt); err != nil {
		fmt.Fprintf(os.Stderr, "tun icmp rx: %v\n", err)
	}
	return true
}

// hookICMP steals dest:1 ICMP datagrams, then overlay UDP, then gVisor UDP inject.
func hookICMP(sess *mux.Session, t *tunStack) {
	if sess == nil {
		return
	}
	sess.FilterDgram(func(addr string, data []byte) bool {
		addr = canonHostPort(addr)
		_, port, err := net.SplitHostPort(addr)
		if err == nil && port == icmpDgramPort {
			return t.onOverlayICMP(addr, data)
		}
		// TUN NAT is keyed by dest. Stealing 8.8.8.8:53 starved SOCKS
		// UDP ASSOCIATE. Copy to TUN and leave the datagram for ReadDgram.
		if t.onOverlayUDP(addr, data) {
			return false
		}
		return t.injectUDP(addr, data)
	})
}

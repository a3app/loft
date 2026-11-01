package inbound

import (
	"encoding/binary"
	"net"
	"testing"

	"github.com/a3app/loft/internal/sstp"
)

// TestIPv4DstPort reads TCP/UDP destination ports from a raw IPv4 header.
func TestIPv4DstPort(t *testing.T) {
	tcpPkt := make([]byte, 40)
	tcpPkt[0] = 0x45
	tcpPkt[9] = 6
	copy(tcpPkt[12:16], []byte{1, 2, 3, 4})
	copy(tcpPkt[16:20], []byte{5, 6, 7, 8})
	binary.BigEndian.PutUint16(tcpPkt[20:22], 12345)
	binary.BigEndian.PutUint16(tcpPkt[22:24], 443)
	port, proto, ok := ipv4DstPort(tcpPkt)
	if !ok || proto != 6 || port != 443 {
		t.Fatalf("tcp %d %d %v", port, proto, ok)
	}

	udpPkt := make([]byte, 28)
	udpPkt[0] = 0x45
	udpPkt[9] = 17
	binary.BigEndian.PutUint16(udpPkt[20:22], 53000)
	binary.BigEndian.PutUint16(udpPkt[22:24], 53)
	port, proto, ok = ipv4DstPort(udpPkt)
	if !ok || proto != 17 || port != 53 {
		t.Fatalf("udp %d %d %v", port, proto, ok)
	}

	opt := make([]byte, 48)
	opt[0] = 0x46 // IHL 6
	opt[9] = 6
	binary.BigEndian.PutUint16(opt[26:28], 80)
	port, proto, ok = ipv4DstPort(opt)
	if !ok || proto != 6 || port != 80 {
		t.Fatalf("opt %d %d %v", port, proto, ok)
	}

	if _, _, ok := ipv4DstPort([]byte{0x45}); ok {
		t.Fatal("short")
	}
	v6 := make([]byte, 40)
	v6[0] = 0x60
	if _, _, ok := ipv4DstPort(v6); ok {
		t.Fatal("ipv6")
	}
}

func TestICMPInnerSrcPort(t *testing.T) {
	inner := make([]byte, 28)
	inner[0] = 0x45
	inner[9] = 6
	binary.BigEndian.PutUint16(inner[20:22], 49152)
	binary.BigEndian.PutUint16(inner[22:24], 443)
	ip := make([]byte, 20+8+len(inner))
	ip[0] = 0x45
	ip[9] = 1
	ip[20] = 3
	ip[21] = 4
	binary.BigEndian.PutUint16(ip[26:28], 1300)
	copy(ip[28:], inner)
	p, ok := icmpInnerSrcPort(ip)
	if !ok || p != 49152 {
		t.Fatalf("port %d %v", p, ok)
	}
	if sstp.ICMPNextHopMTU(ip) != 1300 {
		t.Fatalf("pmtu %d", sstp.ICMPNextHopMTU(ip))
	}
}

func TestSSTPNetBytes(t *testing.T) {
	n := &SSTPNet{}
	n.up.Add(100)
	n.down.Add(40)
	u, d := n.Bytes()
	if u != 100 || d != 40 {
		t.Fatalf("bytes %d %d", u, d)
	}
}

// TestSSTPFromTUNLocalEcho: A --> A delivers ping of our overlay IP into utun.
// fromTUN answers that locally and leaves gateway echo for PPP.
func TestSSTPFromTUNLocalEcho(t *testing.T) {
	our := net.IPv4(172, 16, 223, 103)
	gw := net.IPv4(172, 16, 223, 1)
	rep, ok := icmpEchoReply(makeEcho(our, our, 1, 1, []byte("x")), our)
	if !ok {
		t.Fatal("ping of overlay IP must reply locally")
	}
	if !net.IP(rep[12:16]).Equal(our) || !net.IP(rep[16:20]).Equal(our) {
		t.Fatalf("addrs %s -> %s", net.IP(rep[12:16]), net.IP(rep[16:20]))
	}
	if _, ok := icmpEchoReply(makeEcho(our, gw, 1, 1, []byte("x")), our); ok {
		t.Fatal("gateway echo must go onto PPP")
	}
}

func TestParseDNSList(t *testing.T) {
	got := ParseDNSList("8.8.8.8, 1.1.1.1;9.9.9.9  8.8.8.8 0.0.0.0 ::1 junk")
	if JoinDNS(got) != "8.8.8.8,1.1.1.1,9.9.9.9" {
		t.Fatalf("parse %q", JoinDNS(got))
	}
}

func TestResolveDNS(t *testing.T) {
	ipcp := []net.IP{net.IPv4(8, 8, 8, 8)}
	peer := net.IPv4(172, 16, 223, 1)
	extra := []net.IP{net.IPv4(1, 1, 1, 1)}

	if g := JoinDNS(ResolveDNS(ipcp, peer, nil, false)); g != "8.8.8.8" {
		t.Fatalf("ipcp %s", g)
	}
	if g := JoinDNS(ResolveDNS(nil, peer, nil, false)); g != "172.16.223.1" {
		t.Fatalf("peer %s", g)
	}
	if g := JoinDNS(ResolveDNS(ipcp, peer, extra, false)); g != "8.8.8.8,1.1.1.1" {
		t.Fatalf("append %s", g)
	}
	if g := JoinDNS(ResolveDNS(nil, peer, extra, false)); g != "172.16.223.1,1.1.1.1" {
		t.Fatalf("peer+extra %s", g)
	}
	if g := JoinDNS(ResolveDNS(ipcp, peer, extra, true)); g != "1.1.1.1" {
		t.Fatalf("replace %s", g)
	}
	if g := JoinDNS(ResolveDNS(ipcp, peer, nil, true)); g != "8.8.8.8" {
		t.Fatalf("replace empty extra %s", g)
	}
	if g := JoinDNS(ResolveDNS(nil, nil, nil, false)); g != "" {
		t.Fatalf("empty %s", g)
	}
}

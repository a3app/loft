package sstp

import (
	"encoding/binary"
	"net"
	"testing"
)

func TestInnerMTU(t *testing.T) {
	cases := []struct {
		in, want int
	}{
		{0, 1400},
		{1500, 1400},
		{1400, 1400},
		{1280, 1280},
		{1000, 1280},
		{1300, 1300},
	}
	for _, c := range cases {
		if g := sstpInnerMTU(c.in); g != c.want {
			t.Fatalf("inner %d: %d want %d", c.in, g, c.want)
		}
	}
}

func TestICMPEchoDF(t *testing.T) {
	src := net.IPv4(172, 16, 223, 103)
	dst := net.IPv4(172, 16, 223, 1)
	b := icmpEchoDF(src, dst, 0x1111, 1400, 1400)
	if len(b) != 1400 {
		t.Fatalf("len %d", len(b))
	}
	if b[0] != 0x45 || b[9] != 1 {
		t.Fatalf("hdr %x", b[:10])
	}
	if binary.BigEndian.Uint16(b[2:4]) != 1400 {
		t.Fatalf("tot %d", binary.BigEndian.Uint16(b[2:4]))
	}
	if binary.BigEndian.Uint16(b[6:8]) != 0x4000 {
		t.Fatalf("flags %x", b[6:8])
	}
	if inetSum(b[:20]) != 0 {
		t.Fatalf("ip csum")
	}
	if inetSum(b[20:]) != 0 {
		t.Fatalf("icmp csum")
	}
	if b[20] != 8 {
		t.Fatalf("type %d", b[20])
	}
	if binary.BigEndian.Uint16(b[24:26]) != 0x1111 || binary.BigEndian.Uint16(b[26:28]) != 1400 {
		t.Fatalf("id/seq")
	}
}

func TestICMPEchoReplyMatch(t *testing.T) {
	our := net.IPv4(172, 16, 223, 103)
	peer := net.IPv4(172, 16, 223, 1)
	req := icmpEchoDF(our, peer, 9, 1280, 1280)
	rep := make([]byte, len(req))
	copy(rep, req)
	copy(rep[12:16], peer.To4())
	copy(rep[16:20], our.To4())
	binary.BigEndian.PutUint16(rep[10:12], 0)
	binary.BigEndian.PutUint16(rep[10:12], inetSum(rep[:20]))
	rep[20] = 0
	binary.BigEndian.PutUint16(rep[22:24], 0)
	binary.BigEndian.PutUint16(rep[22:24], inetSum(rep[20:]))
	if !icmpEchoReplyMatch(rep, our, 9, 1280) {
		t.Fatal("match")
	}
	if icmpEchoReplyMatch(rep, our, 9, 1400) {
		t.Fatal("seq")
	}
	if icmpEchoReplyMatch(req, our, 9, 1280) {
		t.Fatal("echo req")
	}
}

func TestICMPNextHopMTU(t *testing.T) {
	b := make([]byte, 56)
	b[0] = 0x45
	b[9] = 1
	b[20] = 3
	b[21] = 4
	binary.BigEndian.PutUint16(b[26:28], 1300)
	if g := ICMPNextHopMTU(b); g != 1300 {
		t.Fatalf("pmtu %d", g)
	}
	b[21] = 1
	if ICMPNextHopMTU(b) != 0 {
		t.Fatal("not frag-needed")
	}
}

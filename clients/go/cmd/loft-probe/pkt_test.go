// pkt_test.go covers ICMP echo/reply checksums and a DNS A-record round trip.
package main

import (
	"encoding/binary"
	"net"
	"testing"
)

// TestMakeEchoAndReply checks echo checksums and that only a swapped reply parses.
func TestMakeEchoAndReply(t *testing.T) {
	src := net.IPv4(10, 8, 0, 5)
	dst := net.IPv4(8, 8, 8, 8)
	pkt := makeEcho(src, dst, 0x1234, 7, []byte("loft-probe"))
	if pkt[9] != ipProtoICMP {
		t.Fatal("proto")
	}
	if checksum(pkt[:20]) != 0 {
		t.Fatal("ip checksum")
	}
	ihl := 20
	if pkt[ihl] != icmpEcho {
		t.Fatal("type")
	}
	if checksum(pkt[ihl:]) != 0 {
		t.Fatal("icmp checksum")
	}
	if _, ok := parseICMPReply(pkt, 0x1234, 7); ok {
		t.Fatal("echo must not parse as reply")
	}
	rep := icmpSwapReply(pkt)
	got, ok := parseICMPReply(rep, 0x1234, 7)
	if !ok {
		t.Fatal("reply")
	}
	if !got.Equal(dst) {
		t.Fatalf("src %s", got)
	}
}

// TestDNSRoundTrip parses a minimal A answer for example.com.
func TestDNSRoundTrip(t *testing.T) {
	q := dnsQuery(0x1111, "example.com")
	if binary.BigEndian.Uint16(q[0:2]) != 0x1111 {
		t.Fatal("id")
	}
	// Build a minimal A answer.
	msg := append([]byte(nil), q...)
	msg[2] = 0x81
	msg[3] = 0x80
	binary.BigEndian.PutUint16(msg[6:8], 1)
	// pointer to name at offset 12, type A class IN ttl rdlen 4, 1.2.3.4
	ans := []byte{0xc0, 12, 0, 1, 0, 1, 0, 0, 0, 60, 0, 4, 1, 2, 3, 4}
	msg = append(msg, ans...)
	rcode, addrs, err := parseDNSAnswers(msg)
	if err != nil || rcode != 0 || len(addrs) != 1 || addrs[0].String() != "1.2.3.4" {
		t.Fatalf("rcode=%d addrs=%v err=%v", rcode, addrs, err)
	}
}

// icmpSwapReply turns an echo request into an echo-reply by swapping addrs.
func icmpSwapReply(pkt []byte) []byte {
	out := append([]byte(nil), pkt...)
	copy(out[12:16], pkt[16:20])
	copy(out[16:20], pkt[12:16])
	binary.BigEndian.PutUint16(out[10:12], 0)
	binary.BigEndian.PutUint16(out[10:12], checksum(out[:20]))
	out[20] = icmpReply
	binary.BigEndian.PutUint16(out[22:24], 0)
	binary.BigEndian.PutUint16(out[22:24], checksum(out[20:]))
	return out
}

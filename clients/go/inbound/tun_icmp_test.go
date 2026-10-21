package inbound

// tun_icmp_test.go covers local echo-reply, no fake gateway/neighbor, and Darwin ICMP NAT.

import (
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"

	"github.com/a3app/loft/internal/mux"
	"github.com/a3app/loft/internal/transport"
)

// TestICMPEchoReply answers an echo to our overlay IP with valid checksums.
func TestICMPEchoReply(t *testing.T) {
	our := net.IPv4(10, 8, 0, 82)
	req := makeEcho(our, our, 0x1234, 1, []byte("hello-loft"))
	rep, ok := icmpEchoReply(req, our)
	if !ok {
		t.Fatal("expected reply")
	}
	if rep[9] != ipProtoICMP {
		t.Fatalf("proto %d", rep[9])
	}
	ihl := int(rep[0]&0x0f) * 4
	if rep[ihl] != icmpReply {
		t.Fatalf("type %d", rep[ihl])
	}
	if !net.IP(rep[12:16]).Equal(our) || !net.IP(rep[16:20]).Equal(our) {
		t.Fatalf("addrs %s -> %s", net.IP(rep[12:16]), net.IP(rep[16:20]))
	}
	if checksum(rep[:ihl]) != 0 {
		t.Fatal("ip checksum")
	}
	if checksum(rep[ihl:]) != 0 {
		t.Fatal("icmp checksum")
	}
	if string(rep[ihl+8:]) != "hello-loft" {
		t.Fatalf("payload %q", rep[ihl+8:])
	}
}

// TestICMPEchoReplyStripsUTUN strips a Darwin 4-byte family header before swapping.
func TestICMPEchoReplyStripsUTUN(t *testing.T) {
	our := net.IPv4(10, 8, 0, 82)
	req := makeEcho(our, our, 7, 2, []byte("x"))
	wrapped := append([]byte{0, 0, 0, 2}, req...)
	rep, ok := icmpEchoReply(wrapped, our)
	if !ok {
		t.Fatal("expected reply")
	}
	if rep[0]>>4 != 4 {
		t.Fatal("still has utun header")
	}
}

// TestICMPSkipsInternet does not locally answer an echo to 8.8.8.8.
func TestICMPSkipsInternet(t *testing.T) {
	our := net.IPv4(10, 8, 0, 82)
	dst := net.IPv4(8, 8, 8, 8)
	req := makeEcho(our, dst, 1, 1, []byte("x"))
	if _, ok := icmpEchoReply(req, our); ok {
		t.Fatal("internet echo must not be faked")
	}
}

// TestICMPNoFakeGateway does not locally answer an echo to the overlay gateway.
func TestICMPNoFakeGateway(t *testing.T) {
	our := net.IPv4(10, 8, 0, 82)
	gw := net.IPv4(10, 8, 0, 1)
	req := makeEcho(our, gw, 1, 1, []byte("x"))
	if _, ok := icmpEchoReply(req, our); ok {
		t.Fatal("gateway must not be faked")
	}
}

// TestICMPNoFakeNeighbor does not locally answer an echo to another overlay host.
func TestICMPNoFakeNeighbor(t *testing.T) {
	our := net.IPv4(10, 8, 0, 82)
	nb := net.IPv4(10, 8, 0, 24)
	req := makeEcho(our, nb, 1, 1, []byte("x"))
	if _, ok := icmpEchoReply(req, our); ok {
		t.Fatal("neighbor must not be faked")
	}
}

// TestICMPIgnoresTCP does not treat a TCP packet as ICMP echo.
func TestICMPIgnoresTCP(t *testing.T) {
	pkt := make([]byte, 40)
	pkt[0] = 0x45
	pkt[9] = 6
	binary.BigEndian.PutUint16(pkt[2:4], 40)
	our := net.IPv4(10, 8, 0, 82)
	if _, ok := icmpEchoReply(pkt, our); ok {
		t.Fatal("tcp must not reply")
	}
}

// TestHandleICMPForwardsInternet writes internet echo as a mux datagram dest:1.
func TestHandleICMPForwardsInternet(t *testing.T) {
	our := net.IPv4(10, 8, 0, 82)
	dst := net.IPv4(8, 8, 8, 8)
	sess := mux.NewClient()
	st := &tunStack{our: our.To4(), d: sessDial{s: sess}, nat: map[uint32][4]byte{}}
	req := makeEcho(our, dst, 4, 1, []byte("dns"))
	if !st.handleICMP(req) {
		t.Fatal("internet icmp not consumed")
	}
	fs := sess.Pull(64*1024, 80*time.Millisecond)
	if len(fs) == 0 {
		t.Fatal("no dgram")
	}
	host, port, err := mux.DecodeTarget(fs[0].Payload)
	if err != nil {
		t.Fatal(err)
	}
	if host != dst.String() || port != 1 {
		t.Fatalf("dgram %s:%d", host, port)
	}
}

// TestNATDarwinSrcOnOverlayICMP rewrites Darwin dummy src to overlay IP on send.
func TestNATDarwinSrcOnOverlayICMP(t *testing.T) {
	our := net.IPv4(10, 8, 0, 82)
	gw := net.IPv4(10, 8, 0, 1)
	dummy := net.IPv4(169, 254, 8, 1)
	sess := mux.NewClient()
	st := &tunStack{our: our.To4(), d: sessDial{s: sess}, nat: map[uint32][4]byte{}}
	req := makeEcho(dummy, gw, 9, 3, []byte("x"))
	if !st.handleICMP(req) {
		t.Fatal("overlay icmp not consumed")
	}
	fs := sess.Pull(64*1024, 80*time.Millisecond)
	if len(fs) == 0 {
		t.Fatal("no dgram")
	}
	host, port, err := mux.DecodeTarget(fs[0].Payload)
	if err != nil {
		t.Fatal(err)
	}
	if host != gw.String() || port != 1 {
		t.Fatalf("dgram %s:%d", host, port)
	}
	n := 2 + len(host) + 2
	pkt := fs[0].Payload[n:]
	if !net.IP(pkt[12:16]).Equal(our) {
		t.Fatalf("src %s want overlay", net.IP(pkt[12:16]))
	}
	if !net.IP(pkt[16:20]).Equal(gw) {
		t.Fatalf("dst %s", net.IP(pkt[16:20]))
	}
	if checksum(pkt[:20]) != 0 {
		t.Fatal("ip checksum")
	}
}

// TestNATRestoreDummyOnReply restores Darwin dummy dest on the echo-reply into TUN.
func TestNATRestoreDummyOnReply(t *testing.T) {
	our := net.IPv4(10, 8, 0, 82)
	gw := net.IPv4(10, 8, 0, 1)
	dummy := net.IPv4(169, 254, 8, 1)
	ifc := &memTun{}
	st := &tunStack{our: our.To4(), ifc: ifc, nat: map[uint32][4]byte{}}
	req := makeEcho(dummy, gw, 9, 3, []byte("x"))
	nated := st.natOut(req)
	rep, ok := icmpSwap(nated)
	if !ok {
		t.Fatal("swap")
	}
	if !st.onOverlayICMP("10.8.0.1:1", rep) {
		t.Fatal("reply not consumed")
	}
	if len(ifc.w) != 1 {
		t.Fatalf("writes %d", len(ifc.w))
	}
	got := ifc.w[0]
	if !net.IP(got[16:20]).Equal(dummy) {
		t.Fatalf("tun dest %s want dummy", net.IP(got[16:20]))
	}
	if !net.IP(got[12:16]).Equal(gw) {
		t.Fatalf("tun src %s", net.IP(got[12:16]))
	}
}

// memTun records Write packets for ICMP/UDP tests.
type memTun struct{ w [][]byte }

// Read is unused; tests inject packets directly.
func (m *memTun) Read([]byte) (int, error) { return 0, io.EOF }

// Write stores a copy of p.
func (m *memTun) Write(p []byte) (int, error) {
	m.w = append(m.w, append([]byte(nil), p...))
	return len(p), nil
}

// Close is a no-op.
func (m *memTun) Close() error { return nil }

// Name is a stub interface name.
func (m *memTun) Name() string { return "utun-test" }

// sessDial is a transport.Dialer wrapping one mux session.
type sessDial struct{ s *mux.Session }

// Open is unused in ICMP tests.
func (d sessDial) Open(string) (net.Conn, error) { return nil, io.EOF }

// Session returns the stub mux session.
func (d sessDial) Session() *mux.Session { return d.s }

// Done returns a never-closed channel.
func (d sessDial) Done() <-chan struct{} { return make(chan struct{}) }

// Close is a no-op.
func (d sessDial) Close() error { return nil }

var _ transport.Dialer = sessDial{}

// makeEcho builds an IPv4 ICMP echo request for tests.
func makeEcho(src, dst net.IP, id, seq uint16, payload []byte) []byte {
	ihl := 20
	icmpLen := 8 + len(payload)
	pkt := make([]byte, ihl+icmpLen)
	pkt[0] = 0x45
	binary.BigEndian.PutUint16(pkt[2:4], uint16(len(pkt)))
	pkt[8] = 64
	pkt[9] = ipProtoICMP
	copy(pkt[12:16], src.To4())
	copy(pkt[16:20], dst.To4())
	binary.BigEndian.PutUint16(pkt[10:12], checksum(pkt[:ihl]))
	pkt[ihl] = icmpEcho
	binary.BigEndian.PutUint16(pkt[ihl+4:ihl+6], id)
	binary.BigEndian.PutUint16(pkt[ihl+6:ihl+8], seq)
	copy(pkt[ihl+8:], payload)
	binary.BigEndian.PutUint16(pkt[ihl+2:ihl+4], checksum(pkt[ihl:]))
	return pkt
}

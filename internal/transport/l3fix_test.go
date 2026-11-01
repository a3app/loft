package transport

// L3 internet NAT: resent TCP segments, FIN with data, the member's exit
// rules, and checksums after a source rewrite.

import (
	"bytes"
	"encoding/binary"
	"io"
	"net"
	"testing"
	"time"

	"github.com/a3app/loft/internal/egress"
)

// l3Handshake opens a flow to addr and returns the member's next seq and
// the node's next seq, with replies collected on the returned channel.
func l3OpenFlow(t *testing.T, who egress.Who, src net.IP, sport uint16, addr *net.TCPAddr) (func([]byte) error, chan []byte, uint32, uint32) {
	t.Helper()
	replies := make(chan []byte, 64)
	reply := func(p []byte) error {
		select {
		case replies <- append([]byte(nil), p...):
		default:
		}
		return nil
	}
	l3Inet(reply, who, src, l3TCPSyn(src, addr.IP, sport, uint16(addr.Port), 1000))
	select {
	case synack := <-replies:
		ihl := int(synack[0]&0x0f) * 4
		if synack[ihl+13]&tcpRST != 0 {
			return reply, replies, 0, 0
		}
		ackn := be32(synack[ihl+4:ihl+8]) + 1
		l3Inet(reply, who, src, l3TCPAck(src, addr.IP, sport, uint16(addr.Port), 1001, ackn))
		return reply, replies, 1001, ackn
	case <-time.After(2 * time.Second):
		t.Fatal("no answer to SYN")
	}
	return nil, nil, 0, 0
}

func l3TCPDataFIN(src, dst net.IP, sport, dport uint16, seq, ack uint32, payload []byte) []byte {
	pkt := l3TCPData(src, dst, sport, dport, seq, ack, payload)
	pkt[20+13] |= tcpFIN
	put16(pkt[20+16:20+18], 0)
	put16(pkt[20+16:20+18], transportChecksum(src, dst, ipProtoTCP, pkt[20:]))
	return pkt
}

// TestL3TCPResendWrittenOnce: a resent segment is not written to the
// outbound connection twice, a segment beyond a gap is dropped, and data
// that comes with FIN is delivered before the half-close.
func TestL3TCPResendWrittenOnce(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	got := make(chan []byte, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		b, _ := io.ReadAll(c) // until the FIN
		got <- b
	}()
	addr := ln.Addr().(*net.TCPAddr)
	src := net.IPv4(10, 8, 0, 3)
	reply, _, seq, ack := l3OpenFlow(t, egress.Who{}, src, 40100, addr)
	if seq == 0 {
		t.Fatal("dial refused")
	}
	d := func(at uint32, s string) []byte {
		return l3TCPData(src, addr.IP, 40100, uint16(addr.Port), at, ack, []byte(s))
	}
	time.Sleep(50 * time.Millisecond) // let the outbound dial finish
	l3Inet(reply, egress.Who{}, src, d(seq, "abc"))
	l3Inet(reply, egress.Who{}, src, d(seq, "abc"))     // resent
	l3Inet(reply, egress.Who{}, src, d(seq+1, "bcd"))   // overlaps: only "d" is new
	l3Inet(reply, egress.Who{}, src, d(seq+20, "zzzz")) // beyond a gap
	l3Inet(reply, egress.Who{}, src, l3TCPDataFIN(src, addr.IP, 40100, uint16(addr.Port), seq+4, ack, []byte("ef")))
	select {
	case b := <-got:
		if string(b) != "abcdef" {
			t.Fatalf("outbound got %q, want %q", b, "abcdef")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no FIN reached the outbound connection")
	}
}

// TestL3TCPFollowsMemberExitRules: the outbound dial of an L3 flow uses the
// member's exit rules; a member whose rule blocks gets a RST, another member
// on the same node connects.
func TestL3TCPFollowsMemberExitRules(t *testing.T) {
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	cfg, err := egress.Parse("rule member bob via block\ndefault direct\n")
	if err != nil {
		t.Fatal(err)
	}
	r := egress.New(cfg, egress.Options{SysNet: t.TempDir()})
	defer r.Close()
	old := Egress.Load()
	Egress.Store(r)
	t.Cleanup(func() { Egress.Store(old) })

	addr := ln.Addr().(*net.TCPAddr)
	if _, _, seq, _ := l3OpenFlow(t, egress.Who{Member: "bob"}, net.IPv4(10, 8, 0, 4), 40200, addr); seq != 0 {
		t.Fatal("blocked member connected through L3")
	}
	if _, _, seq, _ := l3OpenFlow(t, egress.Who{Member: "alice"}, net.IPv4(10, 8, 0, 5), 40201, addr); seq == 0 {
		t.Fatal("allowed member refused")
	}
}

// TestRewriteSrcFixesL4Checksums: after the source is replaced, the TCP and
// UDP checksums are valid for the new source; UDP without a checksum stays
// without one.
func TestRewriteSrcFixesL4Checksums(t *testing.T) {
	from, to, dst := net.IPv4(169, 254, 8, 1), net.IPv4(10, 8, 0, 9), net.IPv4(10, 8, 0, 1)
	tcp := l3TCPData(from, dst, 1234, 80, 7, 9, []byte("payload"))
	udp := encapsulateIPv4UDP(from, dst, 5353, 53, []byte("query"))
	for name, pkt := range map[string][]byte{"tcp": tcp, "udp": udp} {
		out := rewriteIPv4Src(pkt, to)
		if !net.IP(out[12:16]).Equal(to) {
			t.Fatalf("%s: source not rewritten", name)
		}
		if transportChecksum(to, dst, out[9], zeroL4Sum(out)) != l4Sum(out) {
			t.Fatalf("%s: checksum not valid for the new source", name)
		}
	}
	noSum := append([]byte(nil), udp...)
	put16(noSum[26:28], 0)
	if out := rewriteIPv4Src(noSum, to); out[26] != 0 || out[27] != 0 {
		t.Fatal("UDP without a checksum got one")
	}
}

func l4At(pkt []byte) int {
	if pkt[9] == ipProtoTCP {
		return 20 + 16
	}
	return 20 + 6
}

func l4Sum(pkt []byte) uint16 { return uint16(pkt[l4At(pkt)])<<8 | uint16(pkt[l4At(pkt)+1]) }

func zeroL4Sum(pkt []byte) []byte {
	b := bytes.Clone(pkt[20:])
	at := l4At(pkt) - 20
	b[at], b[at+1] = 0, 0
	return b
}

// TestL3UDPFollowsMemberExitRules: with exit rules configured, L3 UDP goes
// through egress for its member: blocked for one, out and back for another.
func TestL3UDPFollowsMemberExitRules(t *testing.T) {
	pc, err := net.ListenPacket("udp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	go func() {
		buf := make([]byte, 64)
		for {
			n, from, err := pc.ReadFrom(buf)
			if err != nil {
				return
			}
			_, _ = pc.WriteTo(append([]byte("re:"), buf[:n]...), from)
		}
	}()
	cfg, err := egress.Parse("rule member bob via block\ndefault direct\n")
	if err != nil {
		t.Fatal(err)
	}
	r := egress.New(cfg, egress.Options{SysNet: t.TempDir()})
	defer r.Close()
	old := Egress.Load()
	Egress.Store(r)
	t.Cleanup(func() { Egress.Store(old) })

	port := uint16(pc.LocalAddr().(*net.UDPAddr).Port)
	try := func(who egress.Who, src net.IP, sport uint16) bool {
		replies := make(chan []byte, 4)
		reply := func(p []byte) error { replies <- append([]byte(nil), p...); return nil }
		l3Inet(reply, who, src, encapsulateIPv4UDP(src, net.IPv4(127, 0, 0, 1), sport, port, []byte("hi")))
		select {
		case p := <-replies:
			return bytes.HasSuffix(p, []byte("re:hi"))
		case <-time.After(500 * time.Millisecond):
			return false
		}
	}
	if try(egress.Who{Member: "bob"}, net.IPv4(10, 8, 0, 6), 41000) {
		t.Fatal("blocked member's UDP went out")
	}
	if !try(egress.Who{Member: "alice"}, net.IPv4(10, 8, 0, 7), 41001) {
		t.Fatal("allowed member's UDP got no reply")
	}
}

// TestInternetPingFollowsExitRules: a ping to the internet leaves only when
// the member's route is direct, for L3 and for mux datagrams alike.
func TestInternetPingFollowsExitRules(t *testing.T) {
	cfg, err := egress.Parse("rule member bob via block\ndefault direct\n")
	if err != nil {
		t.Fatal(err)
	}
	r := egress.New(cfg, egress.Options{SysNet: t.TempDir()})
	defer r.Close()
	oldEg, oldPing := Egress.Load(), internetICMPPing
	Egress.Store(r)
	t.Cleanup(func() { Egress.Store(oldEg); internetICMPPing = oldPing })
	pinged := make(chan string, 4)
	internetICMPPing = func(dst net.IP, echo []byte) ([]byte, net.IP, error) {
		pinged <- dst.String()
		return nil, nil, io.EOF
	}
	sent := func() bool {
		select {
		case <-pinged:
			return true
		case <-time.After(200 * time.Millisecond):
			return false
		}
	}
	src := net.IPv4(10, 8, 0, 8)
	req := makeEchoPkt(src, net.IPv4(8, 8, 8, 8), []byte("x"))
	nop := func([]byte) error { return nil }
	l3Inet(nop, egress.Who{Member: "bob"}, src, req)
	if sent() {
		t.Fatal("L3 ping of a blocked member left the node")
	}
	l3Inet(nop, egress.Who{Member: "alice"}, src, req)
	if !sent() {
		t.Fatal("L3 ping of an allowed member was not sent")
	}
	if icmpOut(egress.Who{Member: "bob"}, net.IPv4(8, 8, 8, 8)) {
		t.Fatal("mux ping of a blocked member allowed")
	}
}

// l3Collect starts a listener that returns everything one connection sends
// until its FIN.
func l3Collect(t *testing.T) (*net.TCPAddr, chan []byte) {
	t.Helper()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	got := make(chan []byte, 1)
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		b, _ := io.ReadAll(c)
		got <- b
	}()
	return ln.Addr().(*net.TCPAddr), got
}

// lastAck returns the ack number of the newest segment in replies.
func lastAck(replies chan []byte) uint32 {
	var ack uint32
	for {
		select {
		case p := <-replies:
			ihl := int(p[0]&0x0f) * 4
			ack = be32(p[ihl+8 : ihl+12])
		default:
			return ack
		}
	}
}

// TestL3TCPKeepsSegmentsBeyondAGap: a segment that arrives after a missing
// one is kept, not dropped; when the missing one comes, both go out in
// order and one ACK covers them. Dropping it made the member resend its
// whole window after every lost packet, which stalled uploads.
func TestL3TCPKeepsSegmentsBeyondAGap(t *testing.T) {
	addr, got := l3Collect(t)
	src := net.IPv4(10, 8, 0, 4)
	reply, replies, seq, ack := l3OpenFlow(t, egress.Who{}, src, 40200, addr)
	if seq == 0 {
		t.Fatal("dial refused")
	}
	time.Sleep(50 * time.Millisecond)
	d := func(at uint32, s string) []byte {
		return l3TCPData(src, addr.IP, 40200, uint16(addr.Port), at, ack, []byte(s))
	}
	lastAck(replies)
	l3Inet(reply, egress.Who{}, src, d(seq, "abc"))
	l3Inet(reply, egress.Who{}, src, d(seq+6, "ghi")) // "def" is missing
	l3Inet(reply, egress.Who{}, src, d(seq+9, "jk"))
	if a := lastAck(replies); a != seq+3 {
		t.Fatalf("ack beyond the gap = %d, want %d", a-seq, 3)
	}
	l3Inet(reply, egress.Who{}, src, d(seq+3, "def")) // the gap fills
	if a := lastAck(replies); a != seq+11 {
		t.Fatalf("ack after the gap = %d, want %d", a-seq, 11)
	}
	l3Inet(reply, egress.Who{}, src, l3TCPDataFIN(src, addr.IP, 40200, uint16(addr.Port), seq+11, ack, nil))
	select {
	case b := <-got:
		if string(b) != "abcdefghijk" {
			t.Fatalf("outbound got %q", b)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("no FIN reached the outbound connection")
	}
}

// TestL3TCPAdvertisesRealWindow: segments to the member advertise
// l3RcvWin (scaled), not the 8 MB a raw 65535 << 7 meant.
func TestL3TCPAdvertisesRealWindow(t *testing.T) {
	f := &l3TCPFlow{src: net.IPv4(10, 8, 0, 5), dst: net.IPv4(127, 0, 0, 1), sport: 1, dport: 2}
	p := l3TCPPacket(f, tcpACK, nil)
	if w := uint32(binary.BigEndian.Uint16(p[20+14:20+16])) << l3WScale; w != l3RcvWin {
		t.Fatalf("window %d", w)
	}
}

// TestL3TCPNewSynReplacesFlow: a SYN with a new sequence number on the
// addresses of a known flow is a new connection and gets its own SYN-ACK.
func TestL3TCPNewSynReplacesFlow(t *testing.T) {
	// The listener takes one connection; the second dial may be refused,
	// and a RST answering the new SYN counts as an answer too.
	addr, _ := l3Collect(t)
	src := net.IPv4(10, 8, 0, 6)
	reply, replies, seq, _ := l3OpenFlow(t, egress.Who{}, src, 40300, addr)
	if seq == 0 {
		t.Fatal("dial refused")
	}
	lastAck(replies)
	l3Inet(reply, egress.Who{}, src, l3TCPSyn(src, addr.IP, 40300, uint16(addr.Port), 500000))
	// The old flow may still say goodbye (FIN); wait for the new SYN-ACK.
	deadline := time.After(3 * time.Second)
	for {
		select {
		case p := <-replies:
			ihl := int(p[0]&0x0f) * 4
			if p[ihl+13]&(tcpSYN|tcpRST) != 0 && be32(p[ihl+8:ihl+12]) == 500001 {
				return
			}
		case <-deadline:
			t.Fatal("new SYN ignored")
		}
	}
}

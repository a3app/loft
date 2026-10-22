package inbound

// tun_udp_test.go covers mux UDP inject, Darwin dummy NAT, fragments, and checksums.

import (
	"encoding/binary"
	"io"
	"net"
	"strconv"
	"sync/atomic"
	"testing"
	"time"

	"github.com/a3app/loft/internal/mux"
	"github.com/a3app/loft/internal/transport"
	"github.com/a3app/loft/internal/wire"
)

// memWriter records Write bytes for gVisor UDP inject tests.
type memWriter struct{ b []byte }

// Write appends p to m.b.
func (m *memWriter) Write(p []byte) (int, error) {
	m.b = append(m.b, p...)
	return len(p), nil
}

// closeFlag is an io.WriteCloser that records Close.
type closeFlag struct {
	memWriter
	closed atomic.Bool
}

func (c *closeFlag) Close() error { c.closed.Store(true); return nil }

// TestAddUDPEvictsAtCap closes one writer once the map hits maxUDP.
func TestAddUDPEvictsAtCap(t *testing.T) {
	st := &tunStack{}
	var flags []*closeFlag
	for i := 0; i < maxUDP; i++ {
		c := &closeFlag{}
		flags = append(flags, c)
		st.addUDP("x:"+strconv.Itoa(i), c)
	}
	st.addUDP("new:1", &memWriter{})
	closed := 0
	for _, c := range flags {
		if c.closed.Load() {
			closed++
		}
	}
	if closed != 1 {
		t.Fatalf("closed %d", closed)
	}
	n := 0
	for _, list := range st.udp {
		n += len(list)
	}
	if n != maxUDP {
		t.Fatalf("n=%d", n)
	}
}

// TestUDPReplyInjected writes a mux DNS datagram into a registered UDP writer.
func TestUDPReplyInjected(t *testing.T) {
	w := &memWriter{}
	st := &tunStack{udp: make(map[string][]io.Writer)}
	st.addUDP("8.8.8.8:53", w)
	sess := mux.NewClient()
	hookICMP(sess, st)

	pl := append(mux.EncodeTarget("8.8.8.8", 53), []byte("dns-ok")...)
	sess.Push([]wire.Frame{{Cmd: wire.CmdDgram, Payload: pl}})
	if string(w.b) != "dns-ok" {
		t.Fatalf("got %q", w.b)
	}
}

// TestTUNNatDoesNotStarveSOCKSUDP copies a DNS reply to TUN and ReadDgram.
func TestTUNNatDoesNotStarveSOCKSUDP(t *testing.T) {
	our := net.IPv4(10, 8, 0, 82)
	ifc := &memTun{}
	st := &tunStack{
		our: our.To4(),
		ifc: ifc,
		udpNat: map[string][]udpFlow{
			"8.8.8.8:53": {{src: [4]byte{10, 8, 0, 82}, sport: 54321, seen: time.Now()}},
		},
	}
	sess := mux.NewClient()
	hookICMP(sess, st)

	pl := append(mux.EncodeTarget("8.8.8.8", 53), []byte("dns-ok")...)
	sess.Push([]wire.Frame{{Cmd: wire.CmdDgram, Payload: pl}})
	if len(ifc.w) == 0 {
		t.Fatal("tun missed reply")
	}
	got := make(chan struct {
		addr string
		data []byte
		err  error
	}, 1)
	go func() {
		addr, data, err := sess.ReadDgram()
		got <- struct {
			addr string
			data []byte
			err  error
		}{addr, data, err}
	}()
	select {
	case r := <-got:
		if r.err != nil || r.addr != "8.8.8.8:53" || string(r.data) != "dns-ok" {
			t.Fatalf("%s %q %v", r.addr, r.data, r.err)
		}
	case <-time.After(time.Second):
		t.Fatal("socks ReadDgram starved")
	}
}

// TestTUNNatConsumesWhenTunOnly drains DNS replies so mux dgrams (64) do not fill.
func TestTUNNatConsumesWhenTunOnly(t *testing.T) {
	old := tunOnlyDgrams
	tunOnlyDgrams = true
	t.Cleanup(func() { tunOnlyDgrams = old })

	our := net.IPv4(10, 8, 0, 82)
	ifc := &memTun{}
	st := &tunStack{
		our: our.To4(),
		ifc: ifc,
		udpNat: map[string][]udpFlow{
			"8.8.8.8:53": {{src: [4]byte{10, 8, 0, 82}, sport: 54321, seen: time.Now()}},
		},
	}
	sess := mux.NewClient()
	hookICMP(sess, st)
	pl := append(mux.EncodeTarget("8.8.8.8", 53), []byte("dns-ok")...)
	for i := 0; i < 70; i++ {
		sess.Push([]wire.Frame{{Cmd: wire.CmdDgram, Payload: pl}})
	}
	if len(ifc.w) < 70 {
		t.Fatalf("tun writes %d", len(ifc.w))
	}
	done := make(chan struct{})
	go func() {
		_, _, _ = sess.ReadDgram()
		close(done)
	}()
	select {
	case <-done:
		t.Fatal("consumed dgram leaked to ReadDgram")
	case <-time.After(50 * time.Millisecond):
	}
}

// TestUDPReplyUnknownFallsThrough leaves unmatched datagrams for ReadDgram.
func TestUDPReplyUnknownFallsThrough(t *testing.T) {
	st := &tunStack{udp: make(map[string][]io.Writer)}
	sess := mux.NewClient()
	hookICMP(sess, st)

	pl := append(mux.EncodeTarget("1.1.1.1", 53), []byte("x")...)
	sess.Push([]wire.Frame{{Cmd: wire.CmdDgram, Payload: pl}})

	got := make(chan struct {
		addr string
		data []byte
		err  error
	}, 1)
	go func() {
		addr, data, err := sess.ReadDgram()
		got <- struct {
			addr string
			data []byte
			err  error
		}{addr, data, err}
	}()
	select {
	case r := <-got:
		if r.err != nil || r.addr != "1.1.1.1:53" || string(r.data) != "x" {
			t.Fatalf("%s %q %v", r.addr, r.data, r.err)
		}
	case <-time.After(time.Second):
		t.Fatal("dgram not passed through")
	}
}

// TestUDPAndICMPShareFilter delivers ICMP to TUN and UDP to the registered writer.
func TestUDPAndICMPShareFilter(t *testing.T) {
	our := net.IPv4(10, 8, 0, 82)
	ifc := &memTun{}
	w := &memWriter{}
	st := &tunStack{our: our.To4(), ifc: ifc, nat: map[uint32][4]byte{}, udp: make(map[string][]io.Writer)}
	st.addUDP("8.8.8.8:53", w)
	sess := mux.NewClient()
	hookICMP(sess, st)

	echo := makeEcho(our, net.IPv4(8, 8, 8, 8), 1, 1, []byte("p"))
	rep, ok := icmpSwap(echo)
	if !ok {
		t.Fatal("swap")
	}
	icmpPL := append(mux.EncodeTarget("10.8.0.82", 1), rep...)
	udpPL := append(mux.EncodeTarget("8.8.8.8", 53), []byte("dns")...)
	sess.Push([]wire.Frame{
		{Cmd: wire.CmdDgram, Payload: icmpPL},
		{Cmd: wire.CmdDgram, Payload: udpPL},
	})
	if len(ifc.w) != 1 {
		t.Fatalf("icmp writes %d", len(ifc.w))
	}
	if string(w.b) != "dns" {
		t.Fatalf("udp %q", w.b)
	}
}

// TestDelUDPStopsInject does not inject after the writer is unregistered.
func TestDelUDPStopsInject(t *testing.T) {
	w := &memWriter{}
	st := &tunStack{udp: make(map[string][]io.Writer)}
	st.addUDP("8.8.8.8:53", w)
	st.delUDP("8.8.8.8:53", w)
	if st.injectUDP("8.8.8.8:53", []byte("x")) {
		t.Fatal("inject after del")
	}
	if len(w.b) != 0 {
		t.Fatalf("wrote %q", w.b)
	}
}

// TestHandleUDPSendsDgram writes an IPv4 UDP payload as a mux datagram.
func TestHandleUDPSendsDgram(t *testing.T) {
	our := net.IPv4(10, 8, 0, 82)
	dst := net.IPv4(8, 8, 8, 8)
	sess := mux.NewClient()
	st := &tunStack{our: our.To4(), d: sessDial{s: sess}, udpNat: map[string][]udpFlow{}}
	pkt := encodeUDP(our, 54321, dst, 53, []byte("query"))
	if !st.handleUDP(pkt) {
		t.Fatal("udp not consumed")
	}
	fs := sess.Pull(64*1024, 80*time.Millisecond)
	if len(fs) == 0 {
		t.Fatal("no dgram")
	}
	host, port, err := mux.DecodeTarget(fs[0].Payload)
	if err != nil {
		t.Fatal(err)
	}
	if host != dst.String() || port != 53 {
		t.Fatalf("dgram %s:%d", host, port)
	}
	n := 2 + len(host) + 2
	if string(fs[0].Payload[n:]) != "query" {
		t.Fatalf("payload %q", fs[0].Payload[n:])
	}
}

// TestHandleUDPSkipsTCP does not treat TCP as UDP.
func TestHandleUDPSkipsTCP(t *testing.T) {
	pkt := make([]byte, 40)
	pkt[0] = 0x45
	pkt[9] = 6
	binary.BigEndian.PutUint16(pkt[2:4], 40)
	st := &tunStack{}
	if st.handleUDP(pkt) {
		t.Fatal("tcp must not be udp")
	}
}

// TestHandleUDPSkipsFragments leaves MF fragments for gVisor.
func TestHandleUDPSkipsFragments(t *testing.T) {
	our := net.IPv4(10, 8, 0, 82)
	dst := net.IPv4(8, 8, 8, 8)
	pkt := encodeUDP(our, 54321, dst, 53, []byte("query"))
	binary.BigEndian.PutUint16(pkt[6:8], 0x2000) // MF
	st := &tunStack{d: sessDial{s: mux.NewClient()}}
	if st.handleUDP(pkt) {
		t.Fatal("fragment must fall through")
	}
}

// TestUDPReplyReconstructsDarwin rebuilds a reply to Darwin dummy src/sport.
func TestUDPReplyReconstructsDarwin(t *testing.T) {
	our := net.IPv4(10, 8, 0, 82)
	dst := net.IPv4(8, 8, 8, 8)
	dummy := net.IPv4(169, 254, 8, 1)
	ifc := &memTun{}
	sess := mux.NewClient()
	st := &tunStack{our: our.To4(), ifc: ifc, d: sessDial{s: sess}, udpNat: map[string][]udpFlow{}}
	hookICMP(sess, st)
	req := encodeUDP(dummy, 54321, dst, 53, []byte("query"))
	if !st.handleUDP(req) {
		t.Fatal("udp not consumed")
	}
	pl := append(mux.EncodeTarget("8.8.8.8", 53), []byte("answer")...)
	sess.Push([]wire.Frame{{Cmd: wire.CmdDgram, Payload: pl}})
	if len(ifc.w) != 1 {
		t.Fatalf("tun writes %d", len(ifc.w))
	}
	got := ifc.w[0]
	if got[9] != ipProtoUDP {
		t.Fatalf("proto %d", got[9])
	}
	if !net.IP(got[12:16]).Equal(dst) {
		t.Fatalf("tun src %s", net.IP(got[12:16]))
	}
	if !net.IP(got[16:20]).Equal(dummy) {
		t.Fatalf("tun dest %s want dummy", net.IP(got[16:20]))
	}
	if binary.BigEndian.Uint16(got[20:22]) != 53 {
		t.Fatalf("sport %d", binary.BigEndian.Uint16(got[20:22]))
	}
	if binary.BigEndian.Uint16(got[22:24]) != 54321 {
		t.Fatalf("dport %d", binary.BigEndian.Uint16(got[22:24]))
	}
	if string(got[28:]) != "answer" {
		t.Fatalf("payload %q", got[28:])
	}
	if checksum(got[:20]) != 0 {
		t.Fatal("ip checksum")
	}
	wantUDPChecksum(t, got)
}

// TestUDPChecksum checks IP and UDP checksums on encodeUDP.
func TestUDPChecksum(t *testing.T) {
	src := net.IPv4(10, 8, 0, 82)
	dst := net.IPv4(8, 8, 8, 8)
	pkt := encodeUDP(src, 1234, dst, 53, []byte("hi"))
	if checksum(pkt[:20]) != 0 {
		t.Fatal("ip checksum")
	}
	ulen := int(binary.BigEndian.Uint16(pkt[24:26]))
	if ulen != 10 {
		t.Fatalf("ulen %d", ulen)
	}
	wantUDPChecksum(t, pkt)
}

// wantUDPChecksum asserts the stored UDP checksum matches udpChecksum.
func wantUDPChecksum(t *testing.T, pkt []byte) {
	t.Helper()
	ulen := int(binary.BigEndian.Uint16(pkt[24:26]))
	udp := append([]byte(nil), pkt[20:20+ulen]...)
	stored := binary.BigEndian.Uint16(udp[6:8])
	binary.BigEndian.PutUint16(udp[6:8], 0)
	got := udpChecksum(pkt[12:16], pkt[16:20], udp)
	if got != stored {
		t.Fatalf("udp checksum %04x want %04x", got, stored)
	}
}

// TestUDPReplyMappedIPv4 treats ::ffff:8.8.8.8 as IPv4 8.8.8.8 on reply.
func TestUDPReplyMappedIPv4(t *testing.T) {
	ifc := &memTun{}
	sess := mux.NewClient()
	st := &tunStack{ifc: ifc, d: sessDial{s: sess}, udpNat: map[string][]udpFlow{}}
	hookICMP(sess, st)
	req := encodeUDP(net.IPv4(169, 254, 8, 1), 54321, net.IPv4(8, 8, 8, 8), 53, []byte("q"))
	if !st.handleUDP(req) {
		t.Fatal("udp not consumed")
	}
	pl := append(mux.EncodeTarget("::ffff:8.8.8.8", 53), []byte("a")...)
	sess.Push([]wire.Frame{{Cmd: wire.CmdDgram, Payload: pl}})
	if len(ifc.w) != 1 {
		t.Fatalf("tun writes %d", len(ifc.w))
	}
	got := ifc.w[0]
	if !net.IP(got[12:16]).Equal(net.IPv4(8, 8, 8, 8)) {
		t.Fatalf("src %s", net.IP(got[12:16]))
	}
	if !net.IP(got[16:20]).Equal(net.IPv4(169, 254, 8, 1)) {
		t.Fatalf("dst %s", net.IP(got[16:20]))
	}
	if string(got[28:]) != "a" {
		t.Fatalf("payload %q", got[28:])
	}
}

// TestUDPNatTakesPrecedence prefers Darwin NAT rebuild over gVisor inject.
func TestUDPNatTakesPrecedence(t *testing.T) {
	w := &memWriter{}
	ifc := &memTun{}
	st := &tunStack{ifc: ifc, udp: make(map[string][]io.Writer), udpNat: map[string][]udpFlow{}}
	st.addUDP("8.8.8.8:53", w)
	req := encodeUDP(net.IPv4(169, 254, 8, 1), 9, net.IPv4(8, 8, 8, 8), 53, []byte("q"))
	if !st.handleUDP(req) {
		t.Fatal("udp not consumed")
	}
	sess := mux.NewClient()
	hookICMP(sess, st)
	pl := append(mux.EncodeTarget("8.8.8.8", 53), []byte("a")...)
	sess.Push([]wire.Frame{{Cmd: wire.CmdDgram, Payload: pl}})
	if len(ifc.w) != 1 {
		t.Fatalf("nat writes %d", len(ifc.w))
	}
	if len(w.b) != 0 {
		t.Fatalf("gvisor inject %q", w.b)
	}
}

func dnsQuestion(name string, qtype uint16) []byte {
	q := make([]byte, 12)
	binary.BigEndian.PutUint16(q[0:2], 1)
	binary.BigEndian.PutUint16(q[4:6], 1)
	for _, lab := range splitDNSName(name) {
		q = append(q, byte(len(lab)))
		q = append(q, lab...)
	}
	q = append(q, 0)
	var t [4]byte
	binary.BigEndian.PutUint16(t[0:2], qtype)
	binary.BigEndian.PutUint16(t[2:4], 1)
	return append(q, t[:]...)
}

func splitDNSName(name string) [][]byte {
	var out [][]byte
	start := 0
	for i := 0; i <= len(name); i++ {
		if i == len(name) || name[i] == '.' {
			if i > start {
				out = append(out, []byte(name[start:i]))
			}
			start = i + 1
		}
	}
	return out
}

// TestHandleUDPGatewayDNSStubs answers 10.8.0.1:53 off-TUN and does not mux.
func TestHandleUDPGatewayDNSStubs(t *testing.T) {
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()
	go func() {
		buf := make([]byte, 512)
		n, addr, err := pc.ReadFrom(buf)
		if err != nil {
			return
		}
		buf[2] |= 0x80
		_, _ = pc.WriteTo(buf[:n], addr)
	}()
	prevAddr, prevSync := dnsStubAddr, dnsStubSync
	dnsStubAddr = pc.LocalAddr().String()
	dnsStubSync = true
	defer func() {
		dnsStubAddr = prevAddr
		dnsStubSync = prevSync
	}()
	transport.RememberVis(wire.Status{IP: "10.8.0.2", Gateway: "10.8.0.1"})
	defer transport.RememberVis(wire.Status{})

	ifc := &memTun{}
	sess := mux.NewClient()
	st := &tunStack{our: net.IPv4(10, 8, 0, 2).To4(), ifc: ifc, d: sessDial{s: sess}, udpNat: map[string][]udpFlow{}}
	q := dnsQuestion("example.com", 1)
	pkt := encodeUDP(net.IPv4(10, 8, 0, 2), 12345, net.IPv4(10, 8, 0, 1), 53, q)
	if !st.handleUDP(pkt) {
		t.Fatal("udp not consumed")
	}
	if fs := sess.Pull(64*1024, 20*time.Millisecond); len(fs) != 0 {
		t.Fatalf("muxed gateway dns %d", len(fs))
	}
	if len(ifc.w) != 1 {
		t.Fatalf("tun writes %d", len(ifc.w))
	}
	got := ifc.w[0]
	if got[9] != ipProtoUDP {
		t.Fatalf("proto %d", got[9])
	}
	if !net.IP(got[12:16]).Equal(net.IPv4(10, 8, 0, 1)) {
		t.Fatalf("src %s", net.IP(got[12:16]))
	}
	if !net.IP(got[16:20]).Equal(net.IPv4(10, 8, 0, 2)) {
		t.Fatalf("dst %s", net.IP(got[16:20]))
	}
	if got[28+2]&0x80 == 0 {
		t.Fatal("not a dns response")
	}
}

// TestStubDNSAAAANoData skips the upstream for AAAA on an IPv4-only overlay.
func TestStubDNSAAAANoData(t *testing.T) {
	transport.RememberVis(wire.Status{IP: "10.8.0.2"})
	defer transport.RememberVis(wire.Status{})
	q := dnsQuestion("example.com", dnsTypeAAAA)
	ans := stubDNS(q)
	if len(ans) < 12 {
		t.Fatal("short")
	}
	flags := binary.BigEndian.Uint16(ans[2:4])
	if flags&0x8000 == 0 || flags&0xf != 0 {
		t.Fatalf("flags %#x", flags)
	}
	if binary.BigEndian.Uint16(ans[6:8]) != 0 {
		t.Fatal("answers")
	}
}

// TestHandleUDPPublicDNSStillMuxes leaves 8.8.8.8:53 on the mux.
func TestHandleUDPPublicDNSStillMuxes(t *testing.T) {
	transport.RememberVis(wire.Status{IP: "10.8.0.2", Gateway: "10.8.0.1"})
	defer transport.RememberVis(wire.Status{})
	sess := mux.NewClient()
	st := &tunStack{our: net.IPv4(10, 8, 0, 2).To4(), d: sessDial{s: sess}, udpNat: map[string][]udpFlow{}}
	pkt := encodeUDP(net.IPv4(10, 8, 0, 2), 9, net.IPv4(8, 8, 8, 8), 53, []byte("q"))
	if !st.handleUDP(pkt) {
		t.Fatal("udp not consumed")
	}
	fs := sess.Pull(64*1024, 80*time.Millisecond)
	if len(fs) == 0 {
		t.Fatal("no dgram")
	}
}

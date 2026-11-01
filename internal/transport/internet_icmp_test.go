package transport

// Tests for internet ICMP encapsulate, deliver, ident restore, and unbound drop.

import (
	"encoding/binary"
	"net"
	"testing"
	"time"

	"github.com/a3app/loft/internal/mux"
	"github.com/a3app/loft/internal/netconf"
	"github.com/a3app/loft/internal/wire"
)

// TestEncapsulateIPv4 checks proto, addrs, header checksum, and payload copy.
func TestEncapsulateIPv4(t *testing.T) {
	src := net.IPv4(8, 8, 8, 8)
	dst := net.IPv4(10, 8, 0, 5)
	pl := []byte{0, 0, 0, 0, 1, 2, 3, 4}
	pkt := encapsulateIPv4(src, dst, ipProtoICMP, pl)
	if pkt[9] != ipProtoICMP {
		t.Fatalf("proto %d", pkt[9])
	}
	if !net.IP(pkt[12:16]).Equal(src) || !net.IP(pkt[16:20]).Equal(dst) {
		t.Fatalf("addrs %s -> %s", net.IP(pkt[12:16]), net.IP(pkt[16:20]))
	}
	if ipChecksum(pkt[:20]) != 0 {
		t.Fatal("ip checksum")
	}
	if string(pkt[20:]) != string(pl) {
		t.Fatal("payload")
	}
}

// TestDeliverInternetICMP returns a CmdDgram echo-reply to the session overlay IP.
func TestDeliverInternetICMP(t *testing.T) {
	n := &netconf.Network{ID: "n", CIDR: "10.8.0.0/24", Gateway: "10.8.0.1", Peers: netconf.PeersHub, Enabled: true}
	o := NewOverlay(n)
	oldKeys := AuthKeys
	oldSwitch := NetSwitch()
	oldPing := internetICMPPing
	AuthKeys = nil
	SetNetSwitch(o)
	defer func() {
		AuthKeys = oldKeys
		SetNetSwitch(oldSwitch)
		internetICMPPing = oldPing
	}()
	cli := mux.NewClient()
	st := wire.Status{}
	o.Bind(&st, wire.Hello{}, cli, nil)
	if st.IP == "" {
		t.Fatalf("%+v", st)
	}
	internetICMPPing = func(dst net.IP, echo []byte) ([]byte, net.IP, error) {
		if !dst.Equal(net.IPv4(8, 8, 8, 8)) {
			t.Fatalf("ping dst %s", dst)
		}
		out := append([]byte(nil), echo...)
		out[0] = icmpReply
		binaryPutZeroChecksum(out)
		return out, dst, nil
	}
	req := makeEchoPkt(net.ParseIP(st.IP), net.IPv4(8, 8, 8, 8), []byte("dns"))
	DeliverInternetICMP(cli, req)
	fs := cli.Pull(64*1024, 300*time.Millisecond)
	if len(fs) == 0 || fs[0].Cmd != wire.CmdDgram {
		t.Fatalf("internet reply: %+v", fs)
	}
	host, port, err := mux.DecodeTarget(fs[0].Payload)
	if err != nil {
		t.Fatal(err)
	}
	if host != st.IP || port != 1 {
		t.Fatalf("dgram %s:%d", host, port)
	}
	off := 2 + len(host) + 2
	pkt := fs[0].Payload[off:]
	if !net.IP(pkt[12:16]).Equal(net.IPv4(8, 8, 8, 8)) {
		t.Fatalf("src %s", net.IP(pkt[12:16]))
	}
	if !net.IP(pkt[16:20]).Equal(net.ParseIP(st.IP)) {
		t.Fatalf("dst %s", net.IP(pkt[16:20]))
	}
	ihl := int(pkt[0]&0x0f) * 4
	if pkt[ihl] != icmpReply {
		t.Fatalf("type %d", pkt[ihl])
	}
}

// TestRestoreICMPIdentAfterKernelRewrite copies id+seq from the original echo.
func TestRestoreICMPIdentAfterKernelRewrite(t *testing.T) {
	echo := []byte{icmpEcho, 0, 0, 0, 0x12, 0x34, 0x00, 0x07, 'h', 'i'}
	binary.BigEndian.PutUint16(echo[2:4], ipChecksum(echo))
	rewritten := []byte{icmpReply, 0, 0, 0, 0x00, 0x03, 0x00, 0x07, 'h', 'i'}
	binary.BigEndian.PutUint16(rewritten[2:4], ipChecksum(rewritten))
	got := restoreICMPIdent(rewritten, echo)
	if got[0] != icmpReply {
		t.Fatalf("type %d", got[0])
	}
	if binary.BigEndian.Uint16(got[4:6]) != 0x1234 || binary.BigEndian.Uint16(got[6:8]) != 7 {
		t.Fatalf("id/seq %x %x", got[4:6], got[6:8])
	}
	if ipChecksum(got) != 0 {
		t.Fatal("icmp checksum")
	}
}

// TestDeliverInternetICMPRestoresIdent keeps the client's id after a kernel rewrite.
func TestDeliverInternetICMPRestoresIdent(t *testing.T) {
	n := &netconf.Network{ID: "n", CIDR: "10.8.0.0/24", Gateway: "10.8.0.1", Peers: netconf.PeersHub, Enabled: true}
	o := NewOverlay(n)
	oldKeys := AuthKeys
	oldSwitch := NetSwitch()
	oldPing := internetICMPPing
	AuthKeys = nil
	SetNetSwitch(o)
	defer func() {
		AuthKeys = oldKeys
		SetNetSwitch(oldSwitch)
		internetICMPPing = oldPing
	}()
	cli := mux.NewClient()
	st := wire.Status{}
	o.Bind(&st, wire.Hello{}, cli, nil)
	internetICMPPing = func(dst net.IP, echo []byte) ([]byte, net.IP, error) {
		out := append([]byte(nil), echo...)
		out[0] = icmpReply
		out[4], out[5] = 0, 3 // kernel ident
		binaryPutZeroChecksum(out)
		return out, dst, nil
	}
	req := makeEchoPkt(net.ParseIP(st.IP), net.IPv4(8, 8, 8, 8), []byte("dns"))
	ihl := 20
	req[ihl+4], req[ihl+5] = 0x12, 0x34
	req[ihl+6], req[ihl+7] = 0, 9
	binary.BigEndian.PutUint16(req[ihl+2:ihl+4], 0)
	binary.BigEndian.PutUint16(req[ihl+2:ihl+4], ipChecksum(req[ihl:]))
	DeliverInternetICMP(cli, req)
	fs := cli.Pull(64*1024, 300*time.Millisecond)
	if len(fs) == 0 || fs[0].Cmd != wire.CmdDgram {
		t.Fatalf("internet reply: %+v", fs)
	}
	off := 2 + len(st.IP) + 2
	pkt := fs[0].Payload[off:]
	gotIHL := int(pkt[0]&0x0f) * 4
	if binary.BigEndian.Uint16(pkt[gotIHL+4:gotIHL+6]) != 0x1234 {
		t.Fatalf("ident %x", pkt[gotIHL+4:gotIHL+6])
	}
	if binary.BigEndian.Uint16(pkt[gotIHL+6:gotIHL+8]) != 9 {
		t.Fatalf("seq %x", pkt[gotIHL+6:gotIHL+8])
	}
}

// TestDeliverInternetICMPDropsUnbound does not ping the internet for a session with no lease.
func TestDeliverInternetICMPDropsUnbound(t *testing.T) {
	n := &netconf.Network{ID: "n", CIDR: "10.8.0.0/24", Gateway: "10.8.0.1", Peers: netconf.PeersHub, Enabled: true}
	o := NewOverlay(n)
	oldSwitch := NetSwitch()
	oldPing := internetICMPPing
	SetNetSwitch(o)
	called := false
	internetICMPPing = func(net.IP, []byte) ([]byte, net.IP, error) {
		called = true
		return nil, nil, nil
	}
	defer func() {
		SetNetSwitch(oldSwitch)
		internetICMPPing = oldPing
	}()
	cli := mux.NewClient()
	req := makeEchoPkt(net.IPv4(10, 8, 0, 5), net.IPv4(8, 8, 8, 8), []byte("x"))
	DeliverInternetICMP(cli, req)
	time.Sleep(30 * time.Millisecond)
	if called {
		t.Fatal("unbound session pinged internet")
	}
}

// binaryPutZeroChecksum fills the ICMP checksum field after zeroing it.
func binaryPutZeroChecksum(icmp []byte) {
	if len(icmp) < 4 {
		return
	}
	icmp[2], icmp[3] = 0, 0
	s := ipChecksum(icmp)
	icmp[2] = byte(s >> 8)
	icmp[3] = byte(s)
}

// fakeICMPConn plays back a fixed list of received packets, then returns a
// timeout, like a socket whose deadline passed. Only ReadFrom is used.
type fakeICMPConn struct {
	net.PacketConn
	in []fakeICMPPacket
}

type fakeICMPPacket struct {
	from net.IP
	data []byte
}

type fakeTimeout struct{}

func (fakeTimeout) Error() string   { return "i/o timeout" }
func (fakeTimeout) Timeout() bool   { return true }
func (fakeTimeout) Temporary() bool { return true }

func (f *fakeICMPConn) ReadFrom(b []byte) (int, net.Addr, error) {
	if len(f.in) == 0 {
		return 0, nil, fakeTimeout{}
	}
	p := f.in[0]
	f.in = f.in[1:]
	return copy(b, p.data), &net.IPAddr{IP: p.from}, nil
}

// icmpMsg builds an 8-byte ICMP header: type, code 0, id, seq.
func icmpMsg(typ byte, id, seq uint16) []byte {
	b := make([]byte, 8)
	b[0] = typ
	binary.BigEndian.PutUint16(b[4:6], id)
	binary.BigEndian.PutUint16(b[6:8], seq)
	return b
}

// TestReadEchoReplySkipsStrangers: a raw socket sees every ICMP packet of
// the host. Only the reply from dst with our id and seq may come back;
// everything before it must be skipped, not returned.
func TestReadEchoReplySkipsStrangers(t *testing.T) {
	dst := net.IPv4(1, 1, 1, 1).To4()
	echo := icmpMsg(icmpEcho, 0x1234, 7)
	want := icmpMsg(icmpReply, 0x1234, 7)
	c := &fakeICMPConn{in: []fakeICMPPacket{
		{net.IPv4(8, 8, 8, 8), icmpMsg(icmpReply, 0x1234, 7)}, // another address
		{dst, icmpMsg(icmpEcho, 0x9999, 1)},                   // a ping TO us (scanner)
		{dst, icmpMsg(icmpReply, 0x1234, 6)},                  // older seq
		{dst, icmpMsg(icmpReply, 0x4321, 7)},                  // another member's id
		{dst, want},
	}}
	got, src, err := readEchoReply(c, dst, echo, true)
	if err != nil {
		t.Fatal(err)
	}
	if !src.Equal(dst) || string(got) != string(want) {
		t.Fatalf("got % x from %v, want % x", got, src, want)
	}
}

// TestReadEchoReplyPingSocketIgnoresID: the kernel rewrites the identifier
// on a ping socket, so there only the sequence number may be compared.
func TestReadEchoReplyPingSocketIgnoresID(t *testing.T) {
	dst := net.IPv4(1, 1, 1, 1).To4()
	echo := icmpMsg(icmpEcho, 0x1234, 7)
	c := &fakeICMPConn{in: []fakeICMPPacket{{dst, icmpMsg(icmpReply, 0x0042, 7)}}}
	if _, _, err := readEchoReply(c, dst, echo, false); err != nil {
		t.Fatalf("ping socket reply with kernel id refused: %v", err)
	}
	c = &fakeICMPConn{in: []fakeICMPPacket{{dst, icmpMsg(icmpReply, 0x0042, 7)}}}
	if _, _, err := readEchoReply(c, dst, echo, true); err == nil {
		t.Fatal("raw socket accepted a reply with another id")
	}
}

// TestReadEchoReplyStripsIPv4Header: a raw socket may hand over the reply
// with its IPv4 header; the checks must look past it.
func TestReadEchoReplyStripsIPv4Header(t *testing.T) {
	dst := net.IPv4(1, 1, 1, 1).To4()
	echo := icmpMsg(icmpEcho, 1, 1)
	pkt := encapsulateIPv4(dst, net.IPv4(10, 0, 0, 2), ipProtoICMP, icmpMsg(icmpReply, 1, 1))
	c := &fakeICMPConn{in: []fakeICMPPacket{{dst, pkt}}}
	if _, _, err := readEchoReply(c, dst, echo, true); err != nil {
		t.Fatal(err)
	}
}

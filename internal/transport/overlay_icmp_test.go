package transport

// Tests for overlay ICMP gateway, hub isolation, dummy src, mesh, and FilterDgram sync.

import (
	"encoding/binary"
	"net"
	"testing"
	"time"

	"github.com/a3app/loft/internal/mux"
	"github.com/a3app/loft/internal/netconf"
	"github.com/a3app/loft/internal/wire"
)

// TestICMPEchoSwap turns an echo request into a reply with addrs swapped.
func TestICMPEchoSwap(t *testing.T) {
	src := net.IPv4(10, 8, 0, 5)
	dst := net.IPv4(10, 8, 0, 1)
	req := makeEchoPkt(src, dst, []byte("hi"))
	rep, ok := icmpEchoSwap(req)
	if !ok {
		t.Fatal("swap")
	}
	if !net.IP(rep[12:16]).Equal(dst) || !net.IP(rep[16:20]).Equal(src) {
		t.Fatalf("addrs %s -> %s", net.IP(rep[12:16]), net.IP(rep[16:20]))
	}
	ihl := int(rep[0]&0x0f) * 4
	if rep[ihl] != icmpReply {
		t.Fatalf("type %d", rep[ihl])
	}
}

// TestDeliverICMPGateway answers echo to the overlay gateway as CmdDgram dest:1.
func TestDeliverICMPGateway(t *testing.T) {
	n := &netconf.Network{ID: "n", CIDR: "10.8.0.0/24", Gateway: "10.8.0.1", Peers: netconf.PeersHub, Enabled: true}
	o := NewOverlay(n)
	oldKeys := AuthKeys
	AuthKeys = nil
	defer func() { AuthKeys = oldKeys }()
	cli := mux.NewClient()
	st := wire.Status{}
	o.Bind(&st, wire.Hello{}, cli, nil)
	if st.IP == "" {
		t.Fatalf("%+v", st)
	}
	req := makeEchoPkt(net.ParseIP(st.IP), net.ParseIP("10.8.0.1"), []byte("ping"))
	o.DeliverICMP(cli, req)
	fs := cli.Pull(64*1024, 200*time.Millisecond)
	if len(fs) == 0 || fs[0].Cmd != wire.CmdDgram {
		t.Fatalf("gateway reply: %+v", fs)
	}
}

// TestDeliverICMPHubDropsNeighbor does not forward member-to-member ICMP in hub mode.
func TestDeliverICMPHubDropsNeighbor(t *testing.T) {
	n := &netconf.Network{ID: "n", CIDR: "10.8.0.0/24", Gateway: "10.8.0.1", Peers: netconf.PeersHub, Enabled: true}
	o := NewOverlay(n)
	oldKeys := AuthKeys
	AuthKeys = nil
	defer func() { AuthKeys = oldKeys }()
	a, b := mux.NewClient(), mux.NewClient()
	sa, sb := wire.Status{}, wire.Status{}
	o.Bind(&sa, wire.Hello{}, a, nil)
	o.Bind(&sb, wire.Hello{}, b, nil)
	if sa.IP == "" || sb.IP == "" || sa.IP == sb.IP {
		t.Fatalf("ips %s %s", sa.IP, sb.IP)
	}
	req := makeEchoPkt(net.ParseIP(sa.IP), net.ParseIP(sb.IP), []byte("x"))
	o.DeliverICMP(a, req)
	fs := b.Pull(64*1024, 80*time.Millisecond)
	if len(fs) != 0 {
		t.Fatal("hub leaked neighbor icmp")
	}
}

// TestDeliverICMPGatewayDummySrc rewrites Darwin utun 169.254.8.1 to the session overlay IP.
func TestDeliverICMPGatewayDummySrc(t *testing.T) {
	n := &netconf.Network{ID: "n", CIDR: "10.8.0.0/24", Gateway: "10.8.0.1", Peers: netconf.PeersHub, Enabled: true}
	o := NewOverlay(n)
	oldKeys := AuthKeys
	AuthKeys = nil
	defer func() { AuthKeys = oldKeys }()
	cli := mux.NewClient()
	st := wire.Status{}
	o.Bind(&st, wire.Hello{}, cli, nil)
	if st.IP == "" {
		t.Fatalf("%+v", st)
	}
	dummy := net.IPv4(169, 254, 8, 1)
	req := makeEchoPkt(dummy, net.ParseIP("10.8.0.1"), []byte("ping"))
	o.DeliverICMP(cli, req)
	fs := cli.Pull(64*1024, 200*time.Millisecond)
	if len(fs) == 0 || fs[0].Cmd != wire.CmdDgram {
		t.Fatalf("gateway reply: %+v", fs)
	}
	host, port, err := mux.DecodeTarget(fs[0].Payload)
	if err != nil {
		t.Fatal(err)
	}
	if host != st.IP || port != 1 {
		t.Fatalf("dgram %s:%d want %s:1", host, port, st.IP)
	}
	off := 2 + len(host) + 2
	pkt := fs[0].Payload[off:]
	if !net.IP(pkt[12:16]).Equal(net.ParseIP("10.8.0.1")) {
		t.Fatalf("reply src %s", net.IP(pkt[12:16]))
	}
	if !net.IP(pkt[16:20]).Equal(net.ParseIP(st.IP)) {
		t.Fatalf("reply dst %s want overlay %s", net.IP(pkt[16:20]), st.IP)
	}
}

// TestDeliverICMPMeshNeighbor forwards echo to the peer session in mesh mode.
func TestDeliverICMPMeshNeighbor(t *testing.T) {
	n := &netconf.Network{ID: "n", CIDR: "10.8.0.0/24", Gateway: "10.8.0.1", Peers: netconf.PeersMesh, Enabled: true}
	o := NewOverlay(n)
	oldKeys := AuthKeys
	AuthKeys = nil
	defer func() { AuthKeys = oldKeys }()
	a, b := mux.NewClient(), mux.NewClient()
	sa, sb := wire.Status{}, wire.Status{}
	o.Bind(&sa, wire.Hello{}, a, nil)
	o.Bind(&sb, wire.Hello{}, b, nil)
	req := makeEchoPkt(net.ParseIP(sa.IP), net.ParseIP(sb.IP), []byte("x"))
	o.DeliverICMP(a, req)
	fs := b.Pull(64*1024, 200*time.Millisecond)
	if len(fs) == 0 || fs[0].Cmd != wire.CmdDgram {
		t.Fatalf("mesh forward: %+v", fs)
	}
}

// TestHookServerDgramsGatewaySync returns the gateway echo-reply inside the same Push.
func TestHookServerDgramsGatewaySync(t *testing.T) {
	n := &netconf.Network{ID: "n", CIDR: "10.8.0.0/24", Gateway: "10.8.0.1", Peers: netconf.PeersHub, Enabled: true}
	o := NewOverlay(n)
	oldKeys, oldSwitch := AuthKeys, NetSwitch()
	AuthKeys = nil
	SetNetSwitch(o)
	defer func() {
		AuthKeys = oldKeys
		SetNetSwitch(oldSwitch)
	}()

	srv := mux.NewServer()
	hookServerDgrams(srv)
	st := wire.Status{}
	o.Bind(&st, wire.Hello{}, srv, nil)
	if st.IP == "" {
		t.Fatal("no lease")
	}
	req := makeEchoPkt(net.ParseIP(st.IP), net.ParseIP("10.8.0.1"), []byte("ping"))
	pl := append(mux.EncodeTarget("10.8.0.1", 1), req...)
	srv.Push([]wire.Frame{{Cmd: wire.CmdDgram, Payload: pl}})
	fs := srv.Pull(64*1024, 0)
	if len(fs) == 0 || fs[0].Cmd != wire.CmdDgram {
		t.Fatalf("sync gateway reply missing: %+v", fs)
	}
}

// makeEchoPkt builds an IPv4 ICMP echo-request (type 8) from src to dst.
func makeEchoPkt(src, dst net.IP, payload []byte) []byte {
	ihl := 20
	icmpLen := 8 + len(payload)
	pkt := make([]byte, ihl+icmpLen)
	pkt[0] = 0x45
	binary.BigEndian.PutUint16(pkt[2:4], uint16(len(pkt)))
	pkt[8] = 64
	pkt[9] = ipProtoICMP
	copy(pkt[12:16], src.To4())
	copy(pkt[16:20], dst.To4())
	binary.BigEndian.PutUint16(pkt[10:12], ipChecksum(pkt[:ihl]))
	pkt[ihl] = icmpEcho
	copy(pkt[ihl+8:], payload)
	binary.BigEndian.PutUint16(pkt[ihl+2:ihl+4], ipChecksum(pkt[ihl:]))
	return pkt
}

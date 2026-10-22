package transport

import (
	"net"
	"testing"
	"time"

	"github.com/a3app/loft/internal/netconf"
	"github.com/a3app/loft/internal/wire"
)

type memL3 struct{ ch chan []byte }

func (m *memL3) WriteIP(p []byte) error {
	select {
	case m.ch <- append([]byte(nil), p...):
	default:
	}
	return nil
}

func TestDeliverL3Gateway(t *testing.T) {
	n := &netconf.Network{ID: "n", CIDR: "10.8.0.0/24", Gateway: "10.8.0.1", Peers: netconf.PeersHub, Enabled: true}
	o := NewOverlay(n)
	oldKeys := AuthKeys
	AuthKeys = nil
	defer func() { AuthKeys = oldKeys }()
	sink := &memL3{ch: make(chan []byte, 4)}
	st := wire.Status{}
	o.BindL3(&st, wire.Hello{}, sink, nil)
	if st.IP == "" {
		t.Fatalf("%+v", st)
	}
	req := makeEchoPkt(net.ParseIP(st.IP), net.ParseIP("10.8.0.1"), []byte("ping"))
	o.DeliverL3(sink, req)
	select {
	case got := <-sink.ch:
		ihl := int(got[0]&0x0f) * 4
		if got[ihl] != icmpReply {
			t.Fatalf("type %d", got[ihl])
		}
	case <-time.After(time.Second):
		t.Fatal("timeout")
	}
}

func TestDeliverL3HubDropsNeighbor(t *testing.T) {
	n := &netconf.Network{ID: "n", CIDR: "10.8.0.0/24", Gateway: "10.8.0.1", Peers: netconf.PeersHub, Enabled: true}
	o := NewOverlay(n)
	oldKeys := AuthKeys
	AuthKeys = nil
	defer func() { AuthKeys = oldKeys }()
	a, b := &memL3{ch: make(chan []byte, 4)}, &memL3{ch: make(chan []byte, 4)}
	sa, sb := wire.Status{}, wire.Status{}
	o.BindL3(&sa, wire.Hello{}, a, nil)
	o.BindL3(&sb, wire.Hello{}, b, nil)
	req := makeEchoPkt(net.ParseIP(sa.IP), net.ParseIP(sb.IP), []byte("x"))
	o.DeliverL3(a, req)
	select {
	case <-b.ch:
		t.Fatal("hub leaked neighbor")
	case <-time.After(80 * time.Millisecond):
	}
}

func TestWriteL3Lookup(t *testing.T) {
	n := &netconf.Network{ID: "n", CIDR: "10.8.0.0/24", Gateway: "10.8.0.1", Peers: netconf.PeersHub, Enabled: true}
	o := NewOverlay(n)
	oldKeys := AuthKeys
	AuthKeys = nil
	defer func() { AuthKeys = oldKeys }()
	a, b := &memL3{ch: make(chan []byte, 4)}, &memL3{ch: make(chan []byte, 4)}
	st := wire.Status{}
	o.BindL3(&st, wire.Hello{}, a, nil)
	if st.IP == "" {
		t.Fatal("ip")
	}
	pkt := make([]byte, 20)
	pkt[0] = 0x45
	if err := o.WriteL3(st.IP, pkt); err != nil {
		t.Fatal(err)
	}
	select {
	case <-a.ch:
	case <-time.After(time.Second):
		t.Fatal("no write")
	}
	o.DetachL3(a)
	if err := o.WriteL3(st.IP, pkt); err == nil {
		t.Fatal("detached")
	}
	st2 := wire.Status{}
	o.BindL3(&st2, wire.Hello{}, b, nil)
	if err := o.WriteL3(st2.IP, pkt); err != nil {
		t.Fatal(err)
	}
	select {
	case <-b.ch:
	case <-time.After(time.Second):
		t.Fatal("rebind missed")
	}
}

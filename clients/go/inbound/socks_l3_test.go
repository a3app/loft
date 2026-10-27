package inbound

import (
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/a3app/loft/internal/mux"
	"github.com/a3app/loft/internal/transport"
	"github.com/a3app/loft/internal/wire"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
)

type stubPump struct {
	in     chan []byte
	closed chan struct{}
	once   sync.Once
}

func newStubPump() *stubPump {
	return &stubPump{in: make(chan []byte, 8), closed: make(chan struct{})}
}

func (s *stubPump) WriteIP([]byte) error {
	select {
	case <-s.closed:
		return io.ErrClosedPipe
	default:
		return nil
	}
}

func (s *stubPump) ReadIP() ([]byte, error) {
	select {
	case <-s.closed:
		return nil, io.EOF
	case p := <-s.in:
		return p, nil
	}
}

func (s *stubPump) Open(string) (net.Conn, error) { return nil, io.ErrClosedPipe }
func (s *stubPump) Session() *mux.Session         { return nil }
func (s *stubPump) Done() <-chan struct{}         { return s.closed }
func (s *stubPump) Close() error {
	s.once.Do(func() { close(s.closed) })
	return nil
}

func TestL3NetSwapKeepsStack(t *testing.T) {
	transport.RememberVis(wire.Status{IP: "10.8.0.2", Gateway: "10.8.0.1"})
	a := newStubPump()
	n, err := NewL3Net(a, a)
	if err != nil {
		t.Fatal(err)
	}
	defer n.Close()
	b := newStubPump()
	n.Swap(b, b)
	select {
	case <-n.Done():
		t.Fatal("stack died")
	default:
	}
	pkt := make([]byte, 20)
	pkt[0] = 0x45
	select {
	case b.in <- pkt:
	case <-time.After(time.Second):
		t.Fatal("in")
	}
	deadline := time.After(time.Second)
	for {
		if _, d := n.ConnBytes(); d >= 20 {
			return
		}
		select {
		case <-deadline:
			t.Fatal("no inbound after swap")
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestL3NetSwapFollowsOverlayIP(t *testing.T) {
	transport.RememberVis(wire.Status{IP: "10.8.0.2", Gateway: "10.8.0.1"})
	a := newStubPump()
	n, err := NewL3Net(a, a)
	if err != nil {
		t.Fatal(err)
	}
	defer n.Close()
	// The node restarted and gave this member another address.
	transport.RememberVis(wire.Status{IP: "10.8.0.3", Gateway: "10.8.0.1"})
	b := newStubPump()
	n.Swap(b, b)
	if got := n.ourAddr().String(); got != "10.8.0.3" {
		t.Fatalf("our = %s, want 10.8.0.3", got)
	}
	// NIC 0: with a NIC given, gVisor answers yes for any IPv4 address.
	if n.stk.CheckLocalAddress(0, ipv4.ProtocolNumber, tcpip.AddrFrom4([4]byte{10, 8, 0, 3})) == 0 {
		t.Fatal("stack does not own the new address")
	}
	if n.stk.CheckLocalAddress(0, ipv4.ProtocolNumber, tcpip.AddrFrom4([4]byte{10, 8, 0, 2})) != 0 {
		t.Fatal("stack still owns the old address")
	}
}

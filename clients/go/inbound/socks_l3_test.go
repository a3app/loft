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

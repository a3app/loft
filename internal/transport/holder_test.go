package transport

// Holder Open waits for a Supervise swap instead of failing SOCKS immediately.

import (
	"io"
	"net"
	"testing"
	"time"

	"github.com/a3app/loft/internal/mux"
)

// stubDialer is a Dialer with a programmable Open.
type stubDialer struct {
	open func(string) (net.Conn, error)
	done chan struct{}
}

func (s *stubDialer) Open(target string) (net.Conn, error) {
	if s.open != nil {
		return s.open(target)
	}
	return nil, net.ErrClosed
}
func (s *stubDialer) Session() *mux.Session { return nil }
func (s *stubDialer) Done() <-chan struct{} {
	if s.done == nil {
		ch := make(chan struct{})
		close(ch)
		return ch
	}
	return s.done
}
func (s *stubDialer) Close() error {
	if s.done != nil {
		select {
		case <-s.done:
		default:
			close(s.done)
		}
	}
	return nil
}

// TestHolderOpenWaitsForSwap retries ErrClosed until Set installs a live dialer.
func TestHolderOpenWaitsForSwap(t *testing.T) {
	h := &Holder{}
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	go func() {
		time.Sleep(80 * time.Millisecond)
		h.Set(&stubDialer{
			open: func(string) (net.Conn, error) { return a, nil },
			done: make(chan struct{}),
		})
	}()
	c, err := h.Open("example.com:443")
	if err != nil {
		t.Fatal(err)
	}
	if c != a {
		t.Fatal("wrong conn")
	}
}

// TestHolderOpenRefusedDoesNotWait returns a real Open error immediately.
func TestHolderOpenRefusedDoesNotWait(t *testing.T) {
	h := &Holder{}
	h.Set(&stubDialer{
		open: func(string) (net.Conn, error) { return nil, io.ErrClosedPipe },
		done: make(chan struct{}),
	})
	t0 := time.Now()
	_, err := h.Open("example.com:443")
	if err != io.ErrClosedPipe {
		t.Fatalf("%v", err)
	}
	if time.Since(t0) > 200*time.Millisecond {
		t.Fatalf("waited %s", time.Since(t0))
	}
}

package transport

// Holder swaps the live dialer without tearing SOCKS/TUN (metro / LTE).

import (
	"errors"
	"net"
	"sync"
	"time"

	"github.com/a3app/loft/internal/mux"
)

// holdOpenWait is how long Open retries while Supervise swaps the front
// after a path change. With cap hold, existing streams stay on the mux.
const holdOpenWait = 8 * time.Second

// Holder swaps the live dialer without tearing down SOCKS/TUN (metro).
type Holder struct {
	mu sync.RWMutex
	d  Dialer
}

// Set installs d and closes the previous dialer.
func (h *Holder) Set(d Dialer) {
	h.mu.Lock()
	old := h.d
	h.d = d
	h.mu.Unlock()
	if old != nil {
		_ = old.Close()
	}
}

// get returns the current dialer.
func (h *Holder) get() Dialer {
	h.mu.RLock()
	defer h.mu.RUnlock()
	return h.d
}

// holdRetry is a dead front (path change / Leave), not a refused target.
func holdRetry(err error) bool {
	return errors.Is(err, mux.ErrClosed) || errors.Is(err, net.ErrClosed)
}

// Open tries P2P on a matching peer hint, else the live dialer.
// While Supervise redials after a path change it waits holdOpenWait.
// Live streams stay on the parked mux when cap hold is on.
func (h *Holder) Open(target string) (net.Conn, error) {
	if c := tryP2P(target); c != nil {
		return c, nil
	}
	deadline := time.Now().Add(holdOpenWait)
	var last error
	for {
		d := h.get()
		if d != nil {
			c, err := d.Open(target)
			if err == nil {
				return c, nil
			}
			last = err
			if !holdRetry(err) {
				return nil, err
			}
		} else {
			last = net.ErrClosed
		}
		if !time.Now().Before(deadline) {
			if last == nil {
				last = net.ErrClosed
			}
			return nil, last
		}
		time.Sleep(25 * time.Millisecond)
	}
}

// TripSummary is GET pad/bulk/up timing when the live dialer is a GET front.
func (h *Holder) TripSummary() string {
	d := h.get()
	if t, ok := d.(interface{ TripSummary() string }); ok {
		return t.TripSummary()
	}
	return ""
}

// Session is the live mux session, or nil while reconnecting.
func (h *Holder) Session() *mux.Session {
	d := h.get()
	if d == nil {
		return nil
	}
	return d.Session()
}

// Bytes sums mux and, when present, front ConnBytes.
func (h *Holder) Bytes() (up, down uint64) {
	d := h.get()
	if d == nil {
		return
	}
	if s := d.Session(); s != nil {
		up, down = s.Bytes()
	}
	if c, ok := d.(interface{ ConnBytes() (uint64, uint64) }); ok {
		u2, d2 := c.ConnBytes()
		up += u2
		down += d2
	}
	return
}

// Done never closes: swapping the inner dialer must not tear SOCKS/TUN.
func (h *Holder) Done() <-chan struct{} {
	ch := make(chan struct{})
	return ch
}

// Close clears the live dialer and closes it.
func (h *Holder) Close() error {
	h.mu.Lock()
	d := h.d
	h.d = nil
	h.mu.Unlock()
	if d != nil {
		return d.Close()
	}
	return nil
}

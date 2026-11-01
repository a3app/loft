package xtls

// recordGate sits under the outer TLS conn (REALITY or uTLS). Each Read
// returns at most one TLS record, so the TLS stack never pulls the bytes
// that follow it.
//
// Xray's Vision command direct (0x02) means the peer has stopped writing
// TLS records and is copying the inner stream onto this socket. Those
// bytes must not sit inside uTLS's rawInput, or the splice reader skips
// them and the inner TLS MAC fails. Release hands back anything already
// taken from the socket and the raw conn itself.

import (
	"io"
	"net"
	"sync"
)

// Max TLS ciphertext we will treat as one record. Larger, or a header
// that is not TLS, falls through as a plain read.
const recordMax = 16384 + 256

// RecordGate is the socket wrapper Dial paths use when Vision may splice.
type RecordGate struct {
	net.Conn
	mu     sync.Mutex
	buf    []byte
	left   int
	passed bool
}

// NewRecordGate wraps raw. Close and deadlines go to raw.
func NewRecordGate(raw net.Conn) *RecordGate {
	return &RecordGate{Conn: raw}
}

// Read returns the next TLS record, possibly in pieces. A short read is
// the end of that record; the following record stays in the kernel.
func (g *RecordGate) Read(p []byte) (int, error) {
	g.mu.Lock()
	defer g.mu.Unlock()
	if len(p) == 0 {
		return 0, nil
	}
	if g.passed {
		return g.readRest(p)
	}
	if g.left == 0 {
		if err := g.fill(5); err != nil {
			return g.give(p, err)
		}
		n := int(g.buf[3])<<8 | int(g.buf[4])
		if g.buf[1] != 0x03 || n > recordMax {
			g.passed = true
			return g.readRest(p)
		}
		if err := g.fill(5 + n); err != nil {
			return g.give(p, err)
		}
		g.left = 5 + n
	}
	return g.give(p, nil)
}

// Release stops framing. head is bytes already pulled off the socket and
// not yet returned from Read. raw is the connection under the gate.
func (g *RecordGate) Release() (head []byte, raw net.Conn) {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.passed = true
	head = g.buf
	g.buf = nil
	g.left = 0
	return head, g.Conn
}

func (g *RecordGate) fill(n int) error {
	for len(g.buf) < n {
		need := n - len(g.buf)
		tmp := make([]byte, need)
		nr, err := g.Conn.Read(tmp)
		if nr > 0 {
			g.buf = append(g.buf, tmp[:nr]...)
		}
		if err != nil {
			return err
		}
		if nr == 0 {
			return io.ErrUnexpectedEOF
		}
	}
	return nil
}

func (g *RecordGate) give(p []byte, err error) (int, error) {
	n := len(g.buf)
	if g.left > 0 && n > g.left {
		n = g.left
	}
	if n > len(p) {
		n = len(p)
	}
	copy(p, g.buf[:n])
	g.buf = g.buf[n:]
	if g.left > 0 {
		g.left -= n
	}
	if n > 0 && err != nil && len(g.buf) > 0 {
		return n, nil
	}
	return n, err
}

func (g *RecordGate) readRest(p []byte) (int, error) {
	if len(g.buf) > 0 {
		n := copy(p, g.buf)
		g.buf = g.buf[n:]
		return n, nil
	}
	return g.Conn.Read(p)
}

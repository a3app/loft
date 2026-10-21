package inbound

// SocksGate is a SOCKS5 listen that can rebind without tearing the mux Dialer.

import (
	"net"
	"strings"
	"sync"

	"github.com/a3app/loft/internal/transport"
)

// SocksGate holds the current SOCKS5 listener. Set replaces it; a bind
// error leaves the previous listener running.
type SocksGate struct {
	mu sync.Mutex
	ln net.Listener
	d  transport.Dialer
}

// NewSocksGate serves SOCKS5 through d. No listen until Set.
func NewSocksGate(d transport.Dialer) *SocksGate {
	return &SocksGate{d: d}
}

// SocksOff is true when addr is empty, off, false, 0, or "-".
func SocksOff(addr string) bool {
	switch strings.ToLower(strings.TrimSpace(addr)) {
	case "", "off", "false", "0", "-":
		return true
	default:
		return false
	}
}

// Set binds addr (host:port). Empty/off closes the listener. On bind
// failure the previous listen stays up.
func (g *SocksGate) Set(addr string) (bound string, err error) {
	if g == nil {
		return "", nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	addr = strings.TrimSpace(addr)
	if SocksOff(addr) {
		g.closeLocked()
		return "", nil
	}
	if g.ln != nil && sameSOCKSAddr(g.ln.Addr().String(), addr) {
		return g.ln.Addr().String(), nil
	}
	ln, err := ListenSOCKS5(addr, g.d)
	if err != nil {
		return g.addrLocked(), err
	}
	g.closeLocked()
	g.ln = ln
	return ln.Addr().String(), nil
}

// Addr is the current listen address, or empty.
func (g *SocksGate) Addr() string {
	if g == nil {
		return ""
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.addrLocked()
}

// Close drops the listener. In-flight SOCKS clients keep the Dialer.
func (g *SocksGate) Close() error {
	if g == nil {
		return nil
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	g.closeLocked()
	return nil
}

func (g *SocksGate) closeLocked() {
	if g.ln == nil {
		return
	}
	_ = g.ln.Close()
	g.ln = nil
}

func (g *SocksGate) addrLocked() string {
	if g.ln == nil {
		return ""
	}
	return g.ln.Addr().String()
}

func sameSOCKSAddr(got, want string) bool {
	if got == want {
		return true
	}
	_, gp, err1 := net.SplitHostPort(got)
	_, wp, err2 := net.SplitHostPort(want)
	if err1 != nil || err2 != nil || wp == "0" {
		return false
	}
	return gp == wp && hostOfSOCKS(got) == hostOfSOCKS(want)
}

func hostOfSOCKS(addr string) string {
	h, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return h
}

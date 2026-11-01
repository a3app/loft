package transport

// The sticky UDP source table stays bounded under forged sources.

import (
	"fmt"
	"net"
	"testing"
	"time"
)

func TestStickyTableBounded(t *testing.T) {
	s := &stickyUDP{src: map[string]stickySrc{}}
	ip := net.IPv4(192, 0, 2, 1)
	old := time.Now().Add(-2 * stickyTTL)
	for i := 0; i < stickyMax; i++ {
		s.src[fmt.Sprintf("198.51.100.%d:%d", i%250, i)] = stickySrc{ip: ip, at: old}
	}
	s.sweepLocked(time.Now())
	if len(s.src) != 0 {
		t.Fatalf("stale entries kept: %d", len(s.src))
	}

	now := time.Now()
	for i := 0; i < stickyMax; i++ {
		s.src[fmt.Sprintf("203.0.113.%d:%d", i%250, i)] = stickySrc{ip: ip, at: now}
	}
	s.sweepLocked(now)
	if len(s.src) >= stickyMax {
		t.Fatalf("full table of fresh entries not reset: %d", len(s.src))
	}
}

func TestStickyLookupExpires(t *testing.T) {
	ip := net.IPv4(192, 0, 2, 1)
	addr := &net.UDPAddr{IP: net.IPv4(198, 51, 100, 7), Port: 4433}
	s := &stickyUDP{src: map[string]stickySrc{addr.String(): {ip: ip, at: time.Now()}}}
	if got := s.lookup(addr); !got.Equal(ip) {
		t.Fatalf("fresh entry: %v", got)
	}
	s.src[addr.String()] = stickySrc{ip: ip, at: time.Now().Add(-2 * stickyTTL)}
	if got := s.lookup(addr); got != nil {
		t.Fatalf("stale entry used: %v", got)
	}
}

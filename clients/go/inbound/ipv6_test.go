package inbound

// ipv6_test.go covers TUN IPv4/IPv6 classify with and without overlay IPv6.

import (
	"testing"

	"github.com/a3app/loft/internal/transport"
	"github.com/a3app/loft/internal/wire"
)

// TestClassifyTUNIPv4Only accepts IPv4 and drops IPv6 when no overlay IPv6 is set.
func TestClassifyTUNIPv4Only(t *testing.T) {
	transport.RememberVis(wire.Status{IP: "10.8.0.2"})
	v4 := make([]byte, 20)
	v4[0] = 0x45
	if classifyTUN(v4) != tunIPv4 {
		t.Fatal("ipv4")
	}
	v6 := make([]byte, 40)
	v6[0] = 0x60
	if classifyTUN(v6) != tunDrop {
		t.Fatal("ipv6 without overlay must drop")
	}
	if classifyTUN(nil) != tunDrop || classifyTUN(v4[:10]) != tunDrop {
		t.Fatal("short")
	}
}

// TestClassifyTUNIPv6WhenConfigured accepts IPv6 after Status.IP6 is assigned.
func TestClassifyTUNIPv6WhenConfigured(t *testing.T) {
	transport.RememberVis(wire.Status{IP: "10.8.0.2", IP6: "fd00::2"})
	t.Cleanup(func() { transport.RememberVis(wire.Status{IP: "10.8.0.2"}) })
	v6 := make([]byte, 40)
	v6[0] = 0x60
	if classifyTUN(v6) != tunIPv6 {
		t.Fatal("want tunIPv6 when overlay IPv6 is assigned")
	}
	if !overlayIPv6Configured() {
		t.Fatal("configured")
	}
}

// TestOverlayIPv6EmptyByDefault checks OverlayIPv6 is empty with only IPv4 Status.IP.
func TestOverlayIPv6EmptyByDefault(t *testing.T) {
	transport.RememberVis(wire.Status{IP: "10.8.0.2"})
	if overlayIPv6Configured() || transport.OverlayIPv6() != "" {
		t.Fatal(transport.OverlayIPv6())
	}
}

package inbound

// tun_addr_test.go covers OverlayCIDR prefix taken from tun_addr vs Status.IP.

import "testing"

// TestOverlayCIDRUsesStatusIPAndTunMask keeps Status.IP and the tun_addr mask.
func TestOverlayCIDRUsesStatusIPAndTunMask(t *testing.T) {
	got := OverlayCIDR("10.8.0.17", "10.7.0.2/24")
	if got != "10.8.0.17/24" {
		t.Fatalf("%s", got)
	}
	got = OverlayCIDR("10.8.0.17", "10.8.0.0/16")
	if got != "10.8.0.17/16" {
		t.Fatalf("%s", got)
	}
	got = OverlayCIDR("10.8.0.17/24", "10.7.0.2/16")
	if got != "10.8.0.17/24" {
		t.Fatalf("%s", got)
	}
	got = OverlayCIDR("", "10.7.0.2/24")
	if got != "10.7.0.2/24" {
		t.Fatalf("%s", got)
	}
}

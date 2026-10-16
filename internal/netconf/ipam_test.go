package netconf

// Overlay pool bounds, last-octet /24 shorthand, and NextIP exhaustion.

import "testing"

// TestNextIPHonoursPool assigns first then last pool address, then errPool.
func TestNextIPHonoursPool(t *testing.T) {
	n := &Network{
		ID: "n", CIDR: "10.8.0.0/24", Gateway: "10.8.0.1",
		PoolFrom: "10.8.0.10", PoolTo: "10.8.0.12",
	}
	ip, err := n.NextIP()
	if err != nil || ip != "10.8.0.10" {
		t.Fatalf("first pool %s %v", ip, err)
	}
	n.Members = []Member{{IP: "10.8.0.10"}, {IP: "10.8.0.11"}}
	ip, err = n.NextIP()
	if err != nil || ip != "10.8.0.12" {
		t.Fatalf("last pool %s %v", ip, err)
	}
	n.Members = append(n.Members, Member{IP: "10.8.0.12"})
	if _, err := n.NextIP(); err != errPool {
		t.Fatalf("exhausted: %v", err)
	}
}

// TestNextIPPoolLastOctet expands pool 10 12 on /24 to 10.8.0.10–12.
func TestNextIPPoolLastOctet(t *testing.T) {
	n := &Network{ID: "n", CIDR: "10.8.0.0/24", Gateway: "10.8.0.1", PoolFrom: "10", PoolTo: "12"}
	if err := n.Validate(); err != nil {
		t.Fatal(err)
	}
	if n.PoolFrom != "10.8.0.10" || n.PoolTo != "10.8.0.12" {
		t.Fatalf("canonical %s %s", n.PoolFrom, n.PoolTo)
	}
	ip, err := n.NextIP()
	if err != nil || ip != "10.8.0.10" {
		t.Fatalf("got %s %v", ip, err)
	}
}

// TestValidatePool requires both ends, from ≤ to, and CIDR membership.
func TestValidatePool(t *testing.T) {
	ok := &Network{ID: "n", CIDR: "10.8.0.0/24", Gateway: "10.8.0.1"}
	if err := ok.Validate(); err != nil {
		t.Fatal(err)
	}
	one := &Network{ID: "n", CIDR: "10.8.0.0/24", PoolFrom: "10.8.0.10"}
	if err := one.Validate(); err == nil {
		t.Fatal("one end accepted")
	}
	order := &Network{ID: "n", CIDR: "10.8.0.0/24", PoolFrom: "10.8.0.20", PoolTo: "10.8.0.10"}
	if err := order.Validate(); err == nil {
		t.Fatal("reversed pool accepted")
	}
	out := &Network{ID: "n", CIDR: "10.8.0.0/24", PoolFrom: "10.9.0.10", PoolTo: "10.9.0.20"}
	if err := out.Validate(); err == nil {
		t.Fatal("pool outside cidr accepted")
	}
}

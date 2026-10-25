package netconf

import (
	"strings"
	"testing"
)

func staffNet() *Network {
	on := true
	return &Network{
		ID: "n", CIDR: "10.8.0.0/24", Gateway: "10.8.0.1",
		PoolFrom: "10.8.0.10", PoolTo: "10.8.0.220", Enabled: true,
		VLANs: []VLAN{{
			ID: 10, Name: "staff", CIDR: "10.9.0.0/24", Gateway: "10.9.0.1",
			PoolFrom: "10.9.0.10", PoolTo: "10.9.0.200",
			DNS: DNS{Enabled: &on, Servers: []string{"10.9.0.1"}},
		}},
		Members: []Member{
			{ID: "alice", Authorized: true, VLAN: 10, IP: "10.9.0.14"},
			{ID: "bob", Authorized: true},
		},
	}
}

func TestSegOwnL3(t *testing.T) {
	n := staffNet()
	if err := n.Validate(); err != nil {
		t.Fatal(err)
	}
	s := n.Seg(10)
	if s.CIDR != "10.9.0.0/24" || s.Gateway != "10.9.0.1" || s.PoolFrom != "10.9.0.10" {
		t.Fatalf("%+v", s)
	}
	if !n.ContainsIP(mustIP("10.9.0.50")) || !n.ContainsIP(mustIP("10.8.0.50")) {
		t.Fatal("contains")
	}
	if n.ContainsIP(mustIP("10.7.0.1")) {
		t.Fatal("foreign")
	}
	if !n.IsGateway("10.9.0.1") || !n.IsGateway("10.8.0.1") || n.IsGateway("10.9.0.14") {
		t.Fatal("gateways")
	}
	got := s.DNSServers()
	if len(got) != 1 || got[0] != "10.9.0.1" {
		t.Fatalf("dns %v", got)
	}
}

func TestSegEmptyCIDRInheritsParent(t *testing.T) {
	n := &Network{ID: "n", CIDR: "10.8.0.0/24", Gateway: "10.8.0.1", VLANs: []VLAN{{ID: 20, Name: "l2"}}}
	if err := n.Validate(); err != nil {
		t.Fatal(err)
	}
	s := n.Seg(20)
	if s.CIDR != n.CIDR || s.Gateway != n.Gateway {
		t.Fatalf("inherit %+v", s)
	}
}

func TestSegsRejectOverlap(t *testing.T) {
	n := &Network{ID: "n", CIDR: "10.8.0.0/16", Gateway: "10.8.0.1", VLANs: []VLAN{{ID: 10, CIDR: "10.8.10.0/24"}}}
	if err := n.Validate(); err == nil || !strings.Contains(err.Error(), "overlap") {
		t.Fatalf("nested: %v", err)
	}
	n.CIDR = "10.8.0.0/24"
	n.VLANs[0].CIDR = "10.9.0.0/24"
	n.VLANs[0].Gateway = "10.9.0.1"
	if err := n.Validate(); err != nil {
		t.Fatal(err)
	}
	n.VLANs[0].Gateway = "10.8.0.1"
	if err := n.Validate(); err == nil {
		t.Fatal("gateway outside cidr")
	}
}

func TestNextIPInVLAN(t *testing.T) {
	n := staffNet()
	if err := n.Validate(); err != nil {
		t.Fatal(err)
	}
	ip, err := n.NextIPIn(10)
	if err != nil || ip != "10.9.0.10" {
		t.Fatalf("vlan pool %s %v", ip, err)
	}
	ip, err = n.NextIP()
	if err != nil || ip != "10.8.0.10" {
		t.Fatalf("default pool %s %v", ip, err)
	}
}

func TestAdmitVLANPoolAndSticky(t *testing.T) {
	n := staffNet()
	if err := n.Validate(); err != nil {
		t.Fatal(err)
	}
	r := NewRegistry(n)
	ip, _, code := r.Admit("alice", "phone")
	if !code.OK() || ip != "10.9.0.14" {
		t.Fatalf("sticky %s %s", ip, code)
	}
	ip, _, code = r.Admit("bob", "x")
	if !code.OK() || ip != "10.8.0.10" {
		t.Fatalf("default %s %s", ip, code)
	}
	n.Members[0].IP = "10.8.0.2" // sticky outside the VLAN CIDR
	r = NewRegistry(n)
	ip, _, code = r.Admit("alice", "phone")
	if !code.OK() || ip != "10.9.0.10" {
		t.Fatalf("dropped sticky %s %s", ip, code)
	}
}

func TestSetNetworkDropsLeaseOutsideSegment(t *testing.T) {
	n := staffNet()
	if err := n.Validate(); err != nil {
		t.Fatal(err)
	}
	r := NewRegistry(n)
	if _, _, code := r.Admit("alice", "phone"); !code.OK() {
		t.Fatal(code)
	}
	n2 := staffNet()
	n2.VLANs[0].CIDR = "10.10.0.0/24"
	n2.VLANs[0].Gateway = "10.10.0.1"
	n2.VLANs[0].PoolFrom, n2.VLANs[0].PoolTo = "10.10.0.10", "10.10.0.200"
	if err := n2.Validate(); err != nil {
		t.Fatal(err)
	}
	r.SetNetwork(n2)
	if _, _, code := r.Admit("alice", "phone"); !code.OK() {
		t.Fatal(code)
	}
}

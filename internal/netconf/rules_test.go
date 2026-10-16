package netconf

// Default Peers (hub isolation), VLAN guest ACL, and NextIP skipping gateway.

import (
	"net"
	"testing"
)

// ptr is an int pointer for Rule VLAN fields.
func ptr(v int) *int { return &v }

// TestHubIsolatesClients drops member-to-member and allows ToGW.
func TestHubIsolatesClients(t *testing.T) {
	n := &Network{ID: "n", CIDR: "10.8.0.0/24", Gateway: "10.8.0.1", Peers: PeersHub}
	if err := n.Validate(); err != nil {
		t.Fatal(err)
	}
	client := Packet{
		Src: net.ParseIP("10.8.0.2"), Dst: net.ParseIP("10.8.0.3"),
		Proto: "tcp", Dport: 80,
	}
	if n.Allow(client) {
		t.Fatal("hub must drop member-to-member")
	}
	toGW := Packet{
		Src: net.ParseIP("10.8.0.2"), Dst: net.ParseIP("10.8.0.1"),
		Proto: "tcp", Dport: 443, ToGW: true,
	}
	if !n.Allow(toGW) {
		t.Fatal("hub must accept traffic to the gateway")
	}
}

// TestVLANMeshAndGuestACL is staff mesh, guest drop to staff, HTTPS to gateway.
func TestVLANMeshAndGuestACL(t *testing.T) {
	n := &Network{
		ID: "n", CIDR: "10.8.0.0/23", Gateway: "10.8.0.1", Peers: PeersVLAN,
		VLANs: []VLAN{
			{ID: 10, Name: "staff"},
			{ID: 20, Name: "guests", Peers: PeersHub},
		},
		Rules: []Rule{
			{Action: "accept", SrcVLAN: ptr(20), Proto: "tcp", Dport: []int{443}, Dst: "10.8.0.1/32"},
			{Action: "drop", SrcVLAN: ptr(20), DstVLAN: ptr(10)},
			{Action: "drop", SrcVLAN: ptr(20)},
		},
	}
	if err := n.Validate(); err != nil {
		t.Fatal(err)
	}
	staff := Packet{SrcVLAN: 10, DstVLAN: 10, Src: net.ParseIP("10.8.0.2"), Dst: net.ParseIP("10.8.0.3"), Proto: "tcp", Dport: 22}
	if !n.Allow(staff) {
		t.Fatal("staff vlan is mesh")
	}
	guestToStaff := Packet{SrcVLAN: 20, DstVLAN: 10, Src: net.ParseIP("10.8.1.2"), Dst: net.ParseIP("10.8.0.2"), Proto: "tcp", Dport: 22}
	if n.Allow(guestToStaff) {
		t.Fatal("guest must not reach staff")
	}
	guestHTTPS := Packet{SrcVLAN: 20, Src: net.ParseIP("10.8.1.2"), Dst: net.ParseIP("10.8.0.1"), Proto: "tcp", Dport: 443, ToGW: true}
	if !n.Allow(guestHTTPS) {
		t.Fatal("guest https to gateway")
	}
	guestSSH := Packet{SrcVLAN: 20, Src: net.ParseIP("10.8.1.2"), Dst: net.ParseIP("1.1.1.1"), Proto: "tcp", Dport: 22}
	if n.Allow(guestSSH) {
		t.Fatal("guest catch-all drop")
	}
}

// TestNextIPSkipsGateway does not assign the gateway or a sticky member IP.
func TestNextIPSkipsGateway(t *testing.T) {
	n := &Network{
		ID: "n", CIDR: "10.8.0.0/29", Gateway: "10.8.0.1",
		Members: []Member{{ID: "a", IP: "10.8.0.2"}},
	}
	ip, err := n.NextIP()
	if err != nil {
		t.Fatal(err)
	}
	if ip != "10.8.0.3" {
		t.Fatalf("got %s", ip)
	}
}

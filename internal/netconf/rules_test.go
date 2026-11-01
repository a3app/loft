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

// TestTagACLAndDefaultDeny is ops→db:5432, default deny, gateway still open.
func TestTagACLAndDefaultDeny(t *testing.T) {
	n := &Network{
		ID: "n", CIDR: "10.8.0.0/24", Gateway: "10.8.0.1", Peers: PeersMesh, ACLDefault: "drop",
		Rules: []Rule{
			{Action: "accept", Proto: "tcp", SrcTag: "ops", DstTag: "db", Dport: []int{5432}},
		},
	}
	if err := n.Validate(); err != nil {
		t.Fatal(err)
	}
	ok := Packet{
		Src: net.ParseIP("10.8.0.2"), Dst: net.ParseIP("10.8.0.5"),
		SrcTags: []string{"ops"}, DstTags: []string{"db"},
		Proto: "tcp", Dport: 5432,
	}
	if !n.Allow(ok) {
		t.Fatal("ops to db:5432")
	}
	other := ok
	other.Dport = 22
	if n.Allow(other) {
		t.Fatal("ssh under default deny")
	}
	guest := Packet{
		Src: net.ParseIP("10.8.0.9"), Dst: net.ParseIP("10.8.0.5"),
		SrcTags: []string{"guest"}, DstTags: []string{"db"},
		Proto: "tcp", Dport: 5432,
	}
	if n.Allow(guest) {
		t.Fatal("guest to db")
	}
	gw := Packet{
		Src: net.ParseIP("10.8.0.2"), Dst: net.ParseIP("10.8.0.1"),
		SrcTags: []string{"ops"}, Proto: "udp", Dport: 53, ToGW: true,
	}
	if !n.Allow(gw) {
		t.Fatal("gateway under default deny")
	}
}

// TestRoutedCIDRAllowedOnHub is lab LAN behind a member: hub still forwards it.
func TestRoutedCIDRAllowedOnHub(t *testing.T) {
	n := &Network{ID: "n", CIDR: "10.8.0.0/24", Gateway: "10.8.0.1", Peers: PeersHub}
	if err := n.Validate(); err != nil {
		t.Fatal(err)
	}
	lan := Packet{
		Src: net.ParseIP("10.8.0.2"), Dst: net.ParseIP("192.168.10.5"),
		Proto: "tcp", Dport: 22, Routed: true,
	}
	if !n.Allow(lan) {
		t.Fatal("hub routed")
	}
	n.ACLDefault = "drop"
	if n.Allow(lan) {
		t.Fatal("default deny routed")
	}
}

// TestRouteViaLongestPrefix picks the member overlay IP, not the gateway.
func TestRouteViaLongestPrefix(t *testing.T) {
	n := &Network{
		ID: "n", CIDR: "10.8.0.0/24", Gateway: "10.8.0.1",
		Routes: []Route{
			{Dst: "10.0.0.0/8", Via: "10.8.0.1"},
			{Dst: "192.168.10.0/24", Via: "10.8.0.5"},
			{Dst: "192.168.10.0/25", Via: "10.8.0.6"},
		},
	}
	if err := n.Validate(); err != nil {
		t.Fatal(err)
	}
	if g := n.RouteVia(net.ParseIP("10.1.2.3")); g != "" {
		t.Fatalf("gateway via %q", g)
	}
	if g := n.RouteVia(net.ParseIP("192.168.10.200")); g != "10.8.0.5" {
		t.Fatalf("24 %q", g)
	}
	if g := n.RouteVia(net.ParseIP("192.168.10.1")); g != "10.8.0.6" {
		t.Fatalf("25 %q", g)
	}
}

// TestRouteViaMemberAndTag resolves sticky IP from member id or tag.
func TestRouteViaMemberAndTag(t *testing.T) {
	n := &Network{
		ID: "n", CIDR: "10.8.0.0/24", Gateway: "10.8.0.1",
		Members: []Member{
			{ID: "lab", IP: "10.8.0.5", Tags: []string{"lab"}},
			{ID: "ops", IP: "10.8.0.6", Tags: []string{"ops"}},
		},
		Routes: []Route{
			{Dst: "192.168.10.0/24", Member: "lab"},
			{Dst: "10.20.0.0/16", Tag: "ops"},
		},
	}
	if err := n.Validate(); err != nil {
		t.Fatal(err)
	}
	if g := n.RouteVia(net.ParseIP("192.168.10.9")); g != "10.8.0.5" {
		t.Fatalf("member %q", g)
	}
	if g := n.RouteVia(net.ParseIP("10.20.1.1")); g != "10.8.0.6" {
		t.Fatalf("tag %q", g)
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

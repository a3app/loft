package netconf

// Parse/render of switch-style network.conf, Cisco ACL lines, and YAML fallback.

import (
	"net"
	"strings"
	"testing"
)

// TestParseACLLine covers permit tcp host/eq and deny ip any vlan SRC DST.
func TestParseACLLine(t *testing.T) {
	r, err := ParseACLLine([]string{"permit", "tcp", "host", "10.8.0.2", "host", "10.8.0.5", "eq", "22"})
	if err != nil {
		t.Fatal(err)
	}
	if r.Action != "accept" || r.Proto != "tcp" || r.Src != "10.8.0.2" || r.Dst != "10.8.0.5" || len(r.Dport) != 1 || r.Dport[0] != 22 {
		t.Fatalf("%+v", r)
	}
	line := RenderACLLine(r)
	if line != "permit tcp host 10.8.0.2 host 10.8.0.5 eq 22" {
		t.Fatal(line)
	}
	r, err = ParseACLLine([]string{"deny", "ip", "any", "any", "vlan", "20", "10"})
	if err != nil {
		t.Fatal(err)
	}
	if r.Action != "drop" || r.SrcVLAN == nil || *r.SrcVLAN != 20 || r.DstVLAN == nil || *r.DstVLAN != 10 {
		t.Fatalf("%+v", r)
	}
	r, err = ParseACLLine([]string{"permit", "tcp", "tag", "ops", "tag", "db", "eq", "5432"})
	if err != nil {
		t.Fatal(err)
	}
	if r.Action != "accept" || r.SrcTag != "ops" || r.DstTag != "db" || len(r.Dport) != 1 || r.Dport[0] != 5432 {
		t.Fatalf("%+v", r)
	}
	if RenderACLLine(r) != "permit tcp tag ops tag db eq 5432" {
		t.Fatal(RenderACLLine(r))
	}
}

// TestParseCommandsRoundTrip parses a full overlay, then Parse(Render) keeps keys/routes/VLANs.
func TestParseCommandsRoundTrip(t *testing.T) {
	src := `
network loft
name loft
enable
ipif overlay 10.8.0.0/24
  gateway 10.8.0.1
  pool 10.8.0.10 10.8.0.220
  mtu 1280
  peers hub
  p2p disable
  dns enable
  dns 10.8.0.1
  dns upstream https://1.1.1.1/dns-query
  dns upstream tls://1.1.1.1
  listen 0.0.0.0 443 front ws
create vlan 10 name staff
config vlan 10 cidr 10.9.0.0/24
config vlan 10 gateway 10.9.0.1
config vlan 10 pool 10.9.0.10 10.9.0.200
config vlan 10 dns enable
config vlan 10 dns 10.9.0.1
config vlan 10 peers mesh
create member operator
config member operator key_sha256 deadbeef
config member operator multi enable 8
config member operator tags ops
enable member operator
ip route 10.9.0.0/24 10.8.0.1 description office
ip access-list overlay
  default deny
  permit tcp tag ops tag db eq 5432
  deny tcp 10.8.0.0/24 any eq 22 80
session handshake_ms 10000
session idle_ms 120000
session rate_bps 20000000
config member operator rate_bps 2000000
`
	n, err := ParseCommands(src)
	if err != nil {
		t.Fatal(err)
	}
	if n.ID != "loft" || n.CIDR != "10.8.0.0/24" || n.Gateway != "10.8.0.1" {
		t.Fatalf("scalars %+v", n)
	}
	if n.PoolFrom != "10.8.0.10" || n.P2P == nil || *n.P2P {
		t.Fatalf("pool/p2p %+v", n)
	}
	if len(n.Listen) != 1 || n.Listen[0].Front != "ws" {
		t.Fatalf("listen %+v", n.Listen)
	}
	if len(n.Members) != 1 || n.Members[0].ID != "operator" || !n.Members[0].Authorized {
		t.Fatalf("members %+v", n.Members)
	}
	if n.ACLDefault != "drop" || len(n.Rules) != 2 || n.Rules[0].SrcTag != "ops" || n.Rules[1].Action != "drop" {
		t.Fatalf("rules %+v default %q", n.Rules, n.ACLDefault)
	}
	if len(n.Members[0].Tags) != 1 || n.Members[0].Tags[0] != "ops" {
		t.Fatalf("tags %+v", n.Members[0].Tags)
	}
	if n.Session.RateBPS != 20_000_000 || n.Members[0].Session.RateBPS != 2_000_000 {
		t.Fatalf("rate net %d member %d", n.Session.RateBPS, n.Members[0].Session.RateBPS)
	}
	again, err := ParseCommands(RenderCommands(*n))
	if err != nil {
		t.Fatal(err)
	}
	if again.Members[0].KeySHA256 != "deadbeef" {
		t.Fatalf("key dropped:\n%s", RenderCommands(*n))
	}
	if again.Session.RateBPS != 20_000_000 || again.Members[0].Session.RateBPS != 2_000_000 {
		t.Fatalf("rate roundtrip net %d member %d", again.Session.RateBPS, again.Members[0].Session.RateBPS)
	}
	if again.Routes[0].Note != "office" || again.VLANs[0].Name != "staff" || again.ACLDefault != "drop" {
		t.Fatalf("roundtrip %+v", again)
	}
	v := again.VLANs[0]
	if v.CIDR != "10.9.0.0/24" || v.Gateway != "10.9.0.1" || v.PoolFrom != "10.9.0.10" || v.PoolTo != "10.9.0.200" {
		t.Fatalf("vlan l3 %+v", v)
	}
	if v.DNS.Enabled == nil || !*v.DNS.Enabled || len(v.DNS.Servers) != 1 || v.DNS.Servers[0] != "10.9.0.1" {
		t.Fatalf("vlan dns %+v", v.DNS)
	}
	if !strings.Contains(RenderCommands(*n), "permit") && !strings.Contains(RenderCommands(*n), "deny") {
		t.Fatal("acl missing")
	}
}

// TestParseRouteMemberTagAndTTL keeps ip route member/tag and session ttl_ms.
func TestParseRouteMemberTagAndTTL(t *testing.T) {
	src := `
network loft
ipif overlay 10.8.0.0/24
  gateway 10.8.0.1
create member lab
config member lab ipaddress 10.8.0.5
config member lab tags lab
enable member lab
ip route 192.168.10.0/24 member lab description lab lan
ip route 10.20.0.0/16 tag lab
session ttl_ms 86400000
`
	n, err := ParseCommands(src)
	if err != nil {
		t.Fatal(err)
	}
	if n.Session.TTLMS != 86400000 {
		t.Fatalf("ttl %d", n.Session.TTLMS)
	}
	if len(n.Routes) != 2 || n.Routes[0].Member != "lab" || n.Routes[1].Tag != "lab" {
		t.Fatalf("routes %+v", n.Routes)
	}
	if n.RouteVia(mustIP("192.168.10.9")) != "10.8.0.5" {
		t.Fatalf("member hop %q", n.RouteVia(mustIP("192.168.10.9")))
	}
	if n.RouteVia(mustIP("10.20.1.1")) != "10.8.0.5" {
		t.Fatalf("tag hop %q", n.RouteVia(mustIP("10.20.1.1")))
	}
	out := RenderCommands(*n)
	if !strings.Contains(out, "ip route 192.168.10.0/24 member lab") || !strings.Contains(out, "ip route 10.20.0.0/16 tag lab") || !strings.Contains(out, "session ttl_ms 86400000") {
		t.Fatal(out)
	}
	again, err := ParseCommands(out)
	if err != nil {
		t.Fatal(err)
	}
	if again.Routes[0].Member != "lab" || again.Routes[1].Tag != "lab" || again.Session.TTLMS != 86400000 {
		t.Fatalf("roundtrip %+v sess %+v", again.Routes, again.Session)
	}
}

func mustIP(s string) net.IP {
	ip := net.ParseIP(s)
	if ip == nil {
		panic(s)
	}
	return ip
}

// TestParseYAMLFallback accepts legacy YAML id/cidr/gateway.
func TestParseYAMLFallback(t *testing.T) {
	n, err := Parse([]byte("id: loft\ncidr: 10.8.0.0/24\ngateway: 10.8.0.1\nenabled: true\n"))
	if err != nil {
		t.Fatal(err)
	}
	if n.ID != "loft" || n.CIDR != "10.8.0.0/24" {
		t.Fatalf("%+v", n)
	}
}

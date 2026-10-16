package netconf

// Parse/render of switch-style network.conf, Cisco ACL lines, and YAML fallback.

import (
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
config vlan 10 cidr 10.8.10.0/24
config vlan 10 peers mesh
create member operator
config member operator key_sha256 deadbeef
config member operator multi enable 8
enable member operator
ip route 10.9.0.0/24 10.8.0.1 description office
ip access-list overlay
  deny tcp 10.8.0.0/24 any eq 22 80
session handshake_ms 10000
session idle_ms 120000
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
	if len(n.Rules) != 1 || n.Rules[0].Action != "drop" || n.Rules[0].Dport[0] != 22 {
		t.Fatalf("rules %+v", n.Rules)
	}
	again, err := ParseCommands(RenderCommands(*n))
	if err != nil {
		t.Fatal(err)
	}
	if again.Members[0].KeySHA256 != "deadbeef" {
		t.Fatalf("key dropped:\n%s", RenderCommands(*n))
	}
	if again.Routes[0].Note != "office" || again.VLANs[0].Name != "staff" {
		t.Fatalf("roundtrip %+v", again)
	}
	if !strings.Contains(RenderCommands(*n), "permit") && !strings.Contains(RenderCommands(*n), "deny") {
		t.Fatal("acl missing")
	}
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

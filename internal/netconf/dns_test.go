package netconf

// DNSServers default (gateway), DNS off, custom servers, and CheckUpstream.

import "testing"

// TestDNSServersDefaultGateway uses the gateway and Cloudflare DoH when DNS is empty.
func TestDNSServersDefaultGateway(t *testing.T) {
	n := &Network{ID: "n", CIDR: "10.8.0.0/24", Gateway: "10.8.0.1"}
	if err := n.Validate(); err != nil {
		t.Fatal(err)
	}
	got := n.DNSServers()
	if len(got) != 1 || got[0] != "10.8.0.1" {
		t.Fatalf("%v", got)
	}
	if n.DNSUpstream()[0] != "https://1.1.1.1/dns-query" {
		t.Fatalf("%v", n.DNSUpstream())
	}
}

// TestDNSOff yields no DNSServers when Enabled is false.
func TestDNSOff(t *testing.T) {
	off := false
	n := &Network{ID: "n", CIDR: "10.8.0.0/24", Gateway: "10.8.0.1", DNS: DNS{Enabled: &off}}
	if err := n.Validate(); err != nil {
		t.Fatal(err)
	}
	if n.DNSServers() != nil {
		t.Fatalf("%v", n.DNSServers())
	}
}

// TestDNSCustomServers keeps configured resolver IPs and a tls upstream.
func TestDNSCustomServers(t *testing.T) {
	n := &Network{
		ID: "n", CIDR: "10.8.0.0/24", Gateway: "10.8.0.1",
		DNS: DNS{Servers: []string{"1.1.1.1", "8.8.8.8"}, Upstream: []string{"tls://1.1.1.1"}},
	}
	if err := n.Validate(); err != nil {
		t.Fatal(err)
	}
	got := n.DNSServers()
	if len(got) != 2 || got[0] != "1.1.1.1" || got[1] != "8.8.8.8" {
		t.Fatalf("%v", got)
	}
}

// TestDNSRejectsBadServer rejects a hostname as a DNS server (want an IP).
func TestDNSRejectsBadServer(t *testing.T) {
	n := &Network{ID: "n", CIDR: "10.8.0.0/24", Gateway: "10.8.0.1", DNS: DNS{Servers: []string{"example.com"}}}
	if err := n.Validate(); err == nil {
		t.Fatal("expected error")
	}
}

// TestCheckUpstream accepts DoH/DoT/DoQ/plain IP and rejects http://.
func TestCheckUpstream(t *testing.T) {
	ok := []string{"https://1.1.1.1/dns-query", "tls://1.1.1.1", "quic://1.1.1.1", "1.1.1.1", "8.8.8.8:53"}
	for _, s := range ok {
		if err := CheckUpstream(s); err != nil {
			t.Fatalf("%s: %v", s, err)
		}
	}
	if err := CheckUpstream("http://1.1.1.1/dns-query"); err == nil {
		t.Fatal("http")
	}
}

// parse_test.go covers loft-probe target parsing, expand-all, and the default suite.
package main

import "testing"

// TestParseSpec checks host, host:port, URL, and -proto overrides.
func TestParseSpec(t *testing.T) {
	cases := []struct {
		raw, proto, wantP, host string
		port                    int
	}{
		{"8.8.8.8", "auto", "icmp", "8.8.8.8", 0},
		{"8.8.8.8:53", "auto", "dns", "8.8.8.8", 53},
		{"1.1.1.1:443", "auto", "https", "1.1.1.1", 443},
		{"example.com:22", "auto", "tcp", "example.com", 22},
		{"example.com", "auto", "https", "example.com", 443},
		{"icmp://8.8.8.8", "auto", "icmp", "8.8.8.8", 0},
		{"tcp://1.1.1.1:80", "auto", "tcp", "1.1.1.1", 80},
		{"udp://8.8.8.8:53", "auto", "udp", "8.8.8.8", 53},
		{"dns://1.1.1.1", "auto", "dns", "1.1.1.1", 53},
		{"http://example.com/foo", "auto", "http", "example.com", 80},
		{"https://ifconfig.me/ip", "auto", "https", "ifconfig.me", 443},
		{"8.8.8.8", "tcp", "tcp", "8.8.8.8", 80},
		{"example.com", "icmp", "icmp", "example.com", 0},
	}
	for _, c := range cases {
		sp, err := parseSpec(c.raw, c.proto)
		if err != nil {
			t.Fatalf("%s proto=%s: %v", c.raw, c.proto, err)
		}
		if sp.proto != c.wantP || sp.host != c.host || sp.port != c.port {
			t.Fatalf("%s proto=%s → %+v want proto=%s host=%s port=%d", c.raw, c.proto, sp, c.wantP, c.host, c.port)
		}
	}
}

// TestExpandAll expects six probes including icmp and dns.
func TestExpandAll(t *testing.T) {
	got := expandAll("1.1.1.1")
	if len(got) != 6 {
		t.Fatalf("len %d", len(got))
	}
	if got[0].proto != "icmp" || got[3].proto != "dns" {
		t.Fatalf("%+v", got)
	}
}

// TestDefaultSuite checks the no-args suite has at least five specs.
func TestDefaultSuite(t *testing.T) {
	s := defaultSuite()
	if len(s) < 5 {
		t.Fatal(len(s))
	}
}

// TestCollectSpecsSuite covers auto suite, one icmp dest, and -proto all.
func TestCollectSpecsSuite(t *testing.T) {
	got, err := collectSpecs(nil, "auto")
	if err != nil || len(got) != len(defaultSuite()) {
		t.Fatalf("%d %v", len(got), err)
	}
	got, err = collectSpecs([]string{"8.8.8.8"}, "icmp")
	if err != nil || len(got) != 1 || got[0].proto != "icmp" {
		t.Fatalf("%+v %v", got, err)
	}
	got, err = collectSpecs([]string{"1.1.1.1"}, "all")
	if err != nil || len(got) != 6 {
		t.Fatalf("%d %v", len(got), err)
	}
}

package mobile

import (
	"testing"

	"github.com/a3app/loft/internal/config"
)

func TestFrontHostsAndLiteralIPs(t *testing.T) {
	cfg := &config.ClientConfig{
		Outbounds: []config.Outbound{
			{URL: "https://1.2.3.4:443/x"},
			{URL: "wss://1.2.3.4/ws"},
			{URL: "https://[2001:db8::1]:8443"},
		},
	}
	hosts := FrontHosts(cfg)
	if len(hosts) != 2 {
		t.Fatalf("%v", hosts)
	}
	ips := ResolveFrontIPs(hosts)
	if len(ips) != 2 || ips[0] != "1.2.3.4" || ips[1] != "2001:db8::1" {
		t.Fatalf("ips %v", ips)
	}
}

func TestFrontHostsIncludesEdge(t *testing.T) {
	cfg := &config.ClientConfig{
		EdgeURL: "https://cdn.example.com:8443/foo",
		Outbounds: []config.Outbound{
			{URL: "https://node.example.com/static"},
		},
	}
	hosts := FrontHosts(cfg)
	if len(hosts) != 2 || hosts[0] != "cdn.example.com" || hosts[1] != "node.example.com" {
		t.Fatalf("%v", hosts)
	}
}

func TestFrontHostsAfterPinEdge(t *testing.T) {
	y := "kind: loft\ntoken: lt_x\nedge: https://cdn.example.com\noutbounds:\n  - name: n\n    type: get\n    url: https://node.example.com\n    path: /static/js\n"
	cfg, _, err := decodeFile([]byte(y))
	if err != nil {
		t.Fatal(err)
	}
	hosts := FrontHosts(cfg)
	var cdn, node bool
	for _, h := range hosts {
		if h == "cdn.example.com" {
			cdn = true
		}
		if h == "node.example.com" {
			node = true
		}
	}
	if !cdn || !node {
		t.Fatalf("%v", hosts)
	}
}

func TestHostOf(t *testing.T) {
	cases := map[string]string{
		"https://edge.example.com:8443/foo?x=1": "edge.example.com",
		"wss://edge.example.com/ws":             "edge.example.com",
		"1.2.3.4":                               "1.2.3.4",
		"[2001:db8::1]:443":                     "2001:db8::1",
		"trojan://secret@de1.example.com:443":   "de1.example.com",
		"vless://uuid@1.2.3.4:8443":             "1.2.3.4",
		"":                                      "",
	}
	for in, want := range cases {
		if got := hostOf(in); got != want {
			t.Fatalf("%q: %q want %q", in, got, want)
		}
	}
}

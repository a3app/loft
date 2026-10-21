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

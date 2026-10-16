package main

import "testing"

// TestOutboundForYandex pins 16 KiB GET Cookie budget and verifies TLS.
func TestOutboundForYandex(t *testing.T) {
	o := outboundFor("https://cdn.example", "get", "/static/js", "yandex", true)
	if o.CDN != "yandex" || o.HeaderBudget != 16<<10 || o.Insecure || o.Inflight != 16 {
		t.Fatalf("%+v", o)
	}
	g := outboundFor("https://loft.example:444", "get", "/static/js", "", true)
	off := outboundFor("https://loft.example:444", "get", "/static/js", "", false)
	if off.UseMux() {
		t.Fatalf("mux off: %+v", off)
	}
	if g.CDN != "" || g.HeaderBudget != 16<<10 || !g.Insecure {
		t.Fatalf("%+v", g)
	}
}

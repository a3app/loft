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
	if g.H2MaxAgeMS != 0 {
		t.Fatalf("h2 default %d", g.H2MaxAgeMS)
	}
	outboundH2MaxAgeMS = 60000
	t.Cleanup(func() { outboundH2MaxAgeMS = 0 })
	aged := outboundFor("https://loft.example:444", "get", "/static/js", "", true)
	if aged.H2MaxAgeMS != 60000 {
		t.Fatalf("h2_max_age_ms %d", aged.H2MaxAgeMS)
	}
}

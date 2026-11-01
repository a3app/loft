package main

import (
	"strings"
	"testing"
)

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
	benchDecoy = "origin.example"
	benchPBK = "aabb"
	t.Cleanup(func() { benchDecoy, benchPBK = "", "" })
	masked := outboundFor("https://loft.example:444", "quic", "", "", true)
	if masked.Decoy != "origin.example" || masked.MaskPub != "aabb" || masked.Type != "quic" {
		t.Fatalf("mask fields %+v", masked)
	}
}

func TestSplitTypesAll(t *testing.T) {
	all := splitTypes("all")
	if len(all) != 5 || all[0] != "get" || all[4] != "h3" {
		t.Fatalf("all %v", all)
	}
	list := splitTypes("h2,quic,h2,nope")
	if strings.Join(list, ",") != "h2,quic" {
		t.Fatalf("list %v", list)
	}
	if got := splitTypes(""); len(got) != 2 || got[0] != "ws" || got[1] != "get" {
		t.Fatalf("empty %v", got)
	}
	cfg := benchConfig("tok", "https://loft.example:444", "/static/v3/build", "", true, all)
	if len(cfg.Outbounds) != 5 || cfg.Outbounds[2].Type != "h2" || cfg.Outbounds[2].Path != "/api/v1/tunnel" {
		t.Fatalf("cfg %+v", cfg.Outbounds)
	}
	turned := orderForRotate([]string{"get", "ws", "h2"}, "h2", 1)
	if strings.Join(turned, ",") != "get,h2,ws" {
		t.Fatalf("rotate %v", turned)
	}
	for _, kind := range []string{"h2", "quic", "h3", "l3"} {
		got, err := dialOrder(kind, "https://loft.example:444")
		if err != nil || len(got) != 1 || got[0] != kind {
			t.Fatalf("%s -> %v %v", kind, got, err)
		}
	}
}

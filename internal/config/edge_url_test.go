package config

import "testing"

func TestParseEdgeURL(t *testing.T) {
	origin, path, err := ParseEdgeURL("https://cdn.example.com/static/v3/build")
	if err != nil || origin != "https://cdn.example.com" || path != "/static/v3/build" {
		t.Fatalf("%q %q %v", origin, path, err)
	}
	origin, path, err = ParseEdgeURL("cdn.example.com")
	if err != nil || origin != "https://cdn.example.com" || path != "" {
		t.Fatalf("bare %q %q %v", origin, path, err)
	}
	if _, _, err := ParseEdgeURL("tight"); err == nil {
		t.Fatal("preset")
	}
	if o, p, err := ParseEdgeURL(""); err != nil || o != "" || p != "" {
		t.Fatalf("empty %q %q %v", o, p, err)
	}
}

func TestPinEdgeGETOrder(t *testing.T) {
	off := false
	cfg := &ClientConfig{Outbounds: []Outbound{
		{Name: "n", Type: "get", URL: "https://loft.example:444", Path: "/static/v3/build", CDN: "yandex", Inflight: 8, Mux: &off},
		{Name: "n-ws", Type: "ws", URL: "wss://loft.example:444/api/v1/sync"},
		{Name: "n-h2", Type: "h2", URL: "https://loft.example:444", Path: "/api/v1/tunnel"},
	}}
	if err := PinEdgeGET(cfg, "https://cdn.example/cover.js"); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Outbounds) != 4 {
		t.Fatalf("%d", len(cfg.Outbounds))
	}
	e := cfg.Outbounds[0]
	if e.Type != "get" || e.URL != "https://cdn.example" || e.Path != "/cover.js" || e.CDN != "yandex" || e.Inflight != 8 {
		t.Fatalf("edge %+v", e)
	}
	if e.Mux == nil || *e.Mux {
		t.Fatal("mux")
	}
	if cfg.Outbounds[1].URL != "https://loft.example:444" || cfg.Outbounds[1].Type != "get" {
		t.Fatalf("node %+v", cfg.Outbounds[1])
	}
	if cfg.Outbounds[2].URL != "wss://loft.example:444/api/v1/sync" {
		t.Fatalf("ws %+v", cfg.Outbounds[2])
	}
	if err := PinEdgeGET(cfg, cfg.EdgeURL); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Outbounds) != 4 || cfg.Outbounds[0].URL != "https://cdn.example" {
		t.Fatalf("dup %+v", cfg.Outbounds)
	}
}

func TestPinEdgeGETSkipsWithoutGET(t *testing.T) {
	cfg := &ClientConfig{Outbounds: []Outbound{
		{Name: "n", Type: "h2", URL: "https://loft.example:444", Path: "/api/v1/tunnel"},
	}}
	if err := PinEdgeGET(cfg, "https://cdn.example"); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Outbounds) != 1 || cfg.Outbounds[0].Type != "h2" || cfg.EdgeURL != "" {
		t.Fatalf("%+v %q", cfg.Outbounds, cfg.EdgeURL)
	}
}

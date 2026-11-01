package main

import (
	"strings"
	"testing"

	"github.com/a3app/loft/internal/config"
)

func TestAddEnabledFrontsAfterGET(t *testing.T) {
	cfg := frontConfig("tok", "https://cdn.example", "get", "/static/v3/build", true, "")
	cfg.Fronts = []string{"get", "ws", "h2", "quic", "h3"}
	addEnabledFronts(cfg)
	var got []string
	for _, o := range cfg.Outbounds {
		got = append(got, o.Type)
	}
	want := []string{"get", "ws", "h2", "quic", "h3"}
	if len(got) != len(want) {
		t.Fatalf("%v", got)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("%v", got)
		}
	}
}

func TestAddEnabledFrontsSkipsMuxOff(t *testing.T) {
	cfg := frontConfig("tok", "https://cdn.example", "get", "/static/v3/build", false, "")
	cfg.Fronts = []string{"get", "ws", "h2"}
	addEnabledFronts(cfg)
	if len(cfg.Outbounds) != 1 || cfg.Outbounds[0].Type != "get" {
		t.Fatalf("%v", cfg.Outbounds)
	}
}

func TestAddEnabledFrontsKeepsPrimaryFirst(t *testing.T) {
	cfg := frontConfig("tok", "https://node.example", "h2", "/api/v1/tunnel", true, "")
	cfg.Fronts = []string{"h2", "quic"}
	addEnabledFronts(cfg)
	if len(cfg.Outbounds) != 2 || cfg.Outbounds[0].Type != "h2" || cfg.Outbounds[1].Type != "quic" {
		t.Fatalf("%v", cfg.Outbounds)
	}
}

func TestPinEdgeGETKeepsNodeFronts(t *testing.T) {
	cfg := frontConfig("tok", "https://loft.example:444", "get", "/static/v3/build", true, "")
	cfg.Fronts = []string{"get", "ws", "h2", "quic", "h3"}
	addEnabledFronts(cfg)
	if err := config.PinEdgeGET(cfg, "https://cdn.example/cover.js"); err != nil {
		t.Fatal(err)
	}
	if len(cfg.Outbounds) != 6 {
		t.Fatalf("%v", cfg.Outbounds)
	}
	e := cfg.Outbounds[0]
	if e.Type != "get" || e.URL != "https://cdn.example" || e.Path != "/cover.js" || e.CDN != "" || e.Inflight != 8 {
		t.Fatalf("edge %+v", e)
	}
	n := cfg.Outbounds[1]
	if n.Type != "get" || n.URL != "https://loft.example:444" || n.Path != "/static/v3/build" {
		t.Fatalf("node %+v", n)
	}
	for _, o := range cfg.Outbounds[2:] {
		if strings.Contains(o.URL, "cdn.example") {
			t.Fatalf("%s %+v", o.Type, o)
		}
		if !strings.Contains(o.URL, "loft.example:444") {
			t.Fatalf("%s %+v", o.Type, o)
		}
	}
}

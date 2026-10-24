package main

import "testing"

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

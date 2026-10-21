package testcfg

import (
	"testing"
)

func TestOverlayKeepsBase(t *testing.T) {
	base := defaults()
	got := overlay(base, Set{Front: "node.example.net"})
	if got.Front != "node.example.net" {
		t.Fatal(got.Front)
	}
	if got.CDN != base.CDN || got.Decoy != base.Decoy || got.FrontPort != base.FrontPort {
		t.Fatalf("%+v", got)
	}
}

func TestParentAndWildcard(t *testing.T) {
	if parent("loft.example.com") != "example.com" {
		t.Fatal(parent("loft.example.com"))
	}
	if parent("cdn.stand.example") != "stand.example" {
		t.Fatal(parent("cdn.stand.example"))
	}
}

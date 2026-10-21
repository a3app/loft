package cdn

// Named edge profile lookup.

import (
	"testing"
	"time"
)

// TestLookup resolves yandex/cloudflare and rejects unknown vendor names.
func TestLookup(t *testing.T) {
	p, err := Lookup("yandex")
	if err != nil || p.Front != "get" || p.HeaderBudget != 16<<10 || p.UplinkHeader != "Cookie" || p.TripTimeout != 6*time.Second {
		t.Fatalf("%+v %v", p, err)
	}
	p, err = Lookup("cloudflare")
	if err != nil || p.Front != "ws" {
		t.Fatalf("%+v %v", p, err)
	}
	if _, err := Lookup("akamai-unknown"); err == nil {
		t.Fatal("expected error")
	}
}

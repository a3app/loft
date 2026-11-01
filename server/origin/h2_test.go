package origin

import "testing"

import "github.com/a3app/loft/internal/wire"

func TestAdvertisedH2(t *testing.T) {
	if got := advertisedH2(0, wire.EdgeTight); got != 4 {
		t.Fatalf("tight unset %d", got)
	}
	if got := advertisedH2(0, wire.EdgePlain); got != 0 {
		t.Fatalf("plain unset %d", got)
	}
	if got := advertisedH2(1, wire.EdgeTight); got != 0 {
		t.Fatalf("explicit one %d", got)
	}
	if got := advertisedH2(3, wire.EdgePlain); got != 3 {
		t.Fatalf("three %d", got)
	}
	if got := advertisedH2(9, wire.EdgeTight); got != 4 {
		t.Fatalf("cap %d", got)
	}
}

//go:build (ios || android || darwin) && !hiae

package crypto

import (
	"bytes"
	"testing"

	"github.com/a3app/loft/internal/wire"
)

func TestHiAENotBuilt(t *testing.T) {
	if HiAEEnabled() || InnerOK(wire.InnerHiAE) {
		t.Fatal("hiae compiled in without -tags hiae")
	}
	key := bytes.Repeat([]byte{3}, KeySize)
	BindInner(key, wire.InnerHiAE)
	if InnerOf(key) != wire.InnerChaCha {
		t.Fatalf("bound %d", InnerOf(key))
	}
}

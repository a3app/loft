//go:build (ios || android || darwin) && !reel256

package crypto

import (
	"bytes"
	"testing"

	"github.com/a3app/loft/internal/wire"
)

func TestReelNotBuilt(t *testing.T) {
	if ReelEnabled() || InnerOK(wire.InnerReel) {
		t.Fatal("reel256 compiled in without -tags reel256")
	}
	key := bytes.Repeat([]byte{8}, KeySize)
	BindInner(key, wire.InnerReel)
	if InnerOf(key) != wire.InnerChaCha {
		t.Fatalf("bound %d", InnerOf(key))
	}
}

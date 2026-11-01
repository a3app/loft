package mux

// Fuzz Push of decoded mux batches.

import (
	"testing"

	"github.com/a3app/loft/internal/wire"
)

// FuzzPushGarbage feeds any DecodeBatch-valid frames into a server session.
func FuzzPushGarbage(f *testing.F) {
	f.Add([]byte{0x00, 0x02, 2, 0, 0, 0, 1, 'a', 'b'})
	f.Fuzz(func(t *testing.T, b []byte) {
		s := NewServer()
		defer s.Close()
		frames, err := wire.DecodeBatch(b)
		if err != nil {
			return
		}
		s.Push(frames)
	})
}

package crypto

import (
	"crypto/aes"
	"crypto/cipher"
	"sync"

	"github.com/a3app/loft/internal/wire"
)

// innerModes is the data cipher for a session key ("inner" = the frame
// encryption inside TLS). Handshake Seal of Status
// happens before BindInner, so that blob stays ChaCha20-Poly1305.
// LOFT_STAND_INNER=clear still forces clear for every key.
var innerModes sync.Map // string(key) -> byte

// BindInner selects the data cipher used by later Seal/Open on key.
// InnerClear and InnerAES are stored. Any other id, including ChaCha, clears
// the entry so Seal stays on ChaCha20-Poly1305.
func BindInner(key []byte, id byte) {
	if len(key) == 0 {
		return
	}
	_, _ = keyState(key) // so sweepIdleKeys also forgets this entry
	switch id {
	case wire.InnerClear, wire.InnerAES:
		innerModes.Store(string(key), id)
	default:
		innerModes.Delete(string(key))
	}
}

// InnerOf is the data cipher Seal/Open will use for key.
func InnerOf(key []byte) byte { return dataInner(key) }

// dataInner is InnerOf: the stand override first, then BindInner, else ChaCha.
func dataInner(key []byte) byte {
	if standClear() {
		return wire.InnerClear
	}
	if v, ok := innerModes.Load(string(key)); ok {
		if id, ok := v.(byte); ok {
			return id
		}
	}
	return wire.InnerChaCha
}

func newAESGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	return cipher.NewGCM(block)
}

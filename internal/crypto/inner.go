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
//
// chachaAEADs / aesAEADs keep one stdlib AEAD per session key. Seal and Open
// on those objects are concurrent-safe. Swept with sealers.
var (
	innerModes  sync.Map // string(key) -> byte
	chachaAEADs sync.Map // string(key) -> cipher.AEAD
	aesAEADs    sync.Map // string(key) -> cipher.AEAD
)

func cachedAEAD(m *sync.Map, key []byte, newFn func([]byte) (cipher.AEAD, error)) (cipher.AEAD, error) {
	k := string(key)
	if v, ok := m.Load(k); ok {
		return v.(cipher.AEAD), nil
	}
	a, err := newFn(key)
	if err != nil {
		return nil, err
	}
	actual, _ := m.LoadOrStore(k, a)
	return actual.(cipher.AEAD), nil
}

// HiAEEnabled is true when this binary compiled the HiAE inner cipher.
// iOS, Android and macOS loft-front need -tags hiae; linux/windows have it.
func HiAEEnabled() bool { return hiaeBuilt }

// aeadNonce16 maps loft's 12-byte record nonce to a 16-byte Init nonce:
// four zero bytes then n12. HiAE Init uses this.
func aeadNonce16(n12 []byte) []byte {
	n := make([]byte, 16)
	copy(n[4:], n12)
	return n
}

// InnerOK is whether Seal/Open can bind id. ChaCha, AES and clear always;
// HiAE when HiAEEnabled.
func InnerOK(id byte) bool {
	switch id {
	case wire.InnerClear, wire.InnerChaCha, wire.InnerAES:
		return true
	case wire.InnerHiAE:
		return hiaeBuilt
	default:
		return false
	}
}

// BindInner selects the data cipher used by later Seal/Open on key.
// InnerClear, InnerAES and InnerHiAE (when built) are stored.
// Any other id, including ChaCha, clears the entry so Seal stays on ChaCha20-Poly1305.
func BindInner(key []byte, id byte) {
	if len(key) == 0 {
		return
	}
	_, _ = keyState(key) // so sweepIdleKeys also forgets this entry
	switch id {
	case wire.InnerClear, wire.InnerAES:
		innerModes.Store(string(key), id)
	case wire.InnerHiAE:
		bindHiAE(key)
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
	return cachedAEAD(&aesAEADs, key, func(k []byte) (cipher.AEAD, error) {
		block, err := aes.NewCipher(k)
		if err != nil {
			return nil, err
		}
		return cipher.NewGCM(block)
	})
}

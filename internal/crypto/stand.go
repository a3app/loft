package crypto

import (
	"crypto/cipher"
	"os"

	"golang.org/x/crypto/chacha20poly1305"
)

// Stand measurement switches. Production always caches one ChaCha/AES AEAD
// per session key. LOFT_STAND_INNER=clear skips the inner AEAD (both peers).
// "cache" is accepted and is the same as unset. Do not set clear on :444.

func standInner() string { return os.Getenv("LOFT_STAND_INNER") }

func standClear() bool { return standInner() == "clear" }

// standAEAD is the ChaCha20-Poly1305 constructor used by Seal and Open.
func standAEAD(key []byte) (cipher.AEAD, error) {
	return cachedAEAD(&chachaAEADs, key, chacha20poly1305.New)
}

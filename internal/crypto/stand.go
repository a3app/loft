package crypto

import (
	"crypto/cipher"
	"os"
	"sync"

	"golang.org/x/crypto/chacha20poly1305"
)

// Stand measurement switches. Unset is the production Seal path.
// LOFT_STAND_INNER=cache keeps one ChaCha AEAD per key.
// LOFT_STAND_INNER=clear skips the inner AEAD. Both peers must set clear.
// Do not set these on the live :444 node.

var standAEADCache sync.Map

func standInner() string { return os.Getenv("LOFT_STAND_INNER") }

func standClear() bool { return standInner() == "clear" }

func standAEAD(key []byte) (cipher.AEAD, error) {
	if standInner() != "cache" {
		return chacha20poly1305.New(key)
	}
	k := string(key)
	if v, ok := standAEADCache.Load(k); ok {
		return v.(cipher.AEAD), nil
	}
	a, err := chacha20poly1305.New(key)
	if err != nil {
		return nil, err
	}
	actual, _ := standAEADCache.LoadOrStore(k, a)
	return actual.(cipher.AEAD), nil
}

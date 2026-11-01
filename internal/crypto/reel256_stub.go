//go:build (ios || android || darwin) && !reel256

package crypto

// iOS / Android / macOS loft-front omit Reel-256 unless built with -tags reel256.
// Linux node and Windows client compile the real cipher in (reel256.go).

func bindReel(key []byte) { innerModes.Delete(string(key)) }

func sealReel(key, plaintext, aad []byte) ([]byte, error) {
	return sealWith(key, plaintext, aad, standAEAD)
}

func openReel(key, blob, aad []byte) ([]byte, error) {
	return openWith(key, blob, aad, standAEAD)
}

const reelBuilt = false

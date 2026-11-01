//go:build (ios || android || darwin) && !hiae

package crypto

// iOS / Android / macOS loft-front omit HiAE unless built with -tags hiae.
// Linux node and Windows client compile the real cipher in (hiae.go).

func bindHiAE(key []byte) { innerModes.Delete(string(key)) }

func sealHiAE(key, plaintext, aad []byte) ([]byte, error) {
	return sealWith(key, plaintext, aad, standAEAD)
}

func openHiAE(key, blob, aad []byte) ([]byte, error) {
	return openWith(key, blob, aad, standAEAD)
}

const hiaeBuilt = false

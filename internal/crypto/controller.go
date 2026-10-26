package crypto

// Controller Ed25519 seed and signatures over roster, lists, and fleet slices.
//
// The controller is the main node of a fleet. It signs everything that
// clients and relays must trust but that travels through other hands (a
// relay, a CDN, a public URL): the roster of entry points, the routing
// lists, and each relay's settings (slice). Clients check the signature,
// so a relay cannot forge any of them.

import (
	"crypto/ed25519"
	"fmt"
	"io"
	"os"

	"github.com/a3app/loft/internal/wire"
)

// LoadControllerKey returns the controller's Ed25519 signing key: the seed in
// path, or on first start a random seed written there (mode 0600) so the
// key, and the controller_pub clients pin, survive restarts. The seed is not
// derived from anything: a key derived from LOFT_TOKEN would be known to
// whoever knows the token (every client in "auth shared" mode), and one
// derived from a fixed string would be the same on every node. Empty path
// gives a fresh key that lasts only for this process.
func LoadControllerKey(path string) (ed25519.PrivateKey, error) {
	if path != "" {
		if b, err := os.ReadFile(path); err == nil && len(b) >= ed25519.SeedSize {
			return ed25519.NewKeyFromSeed(b[:ed25519.SeedSize]), nil
		}
	}
	seed := make([]byte, ed25519.SeedSize)
	if _, err := io.ReadFull(entropy, seed); err != nil {
		return nil, err
	}
	if path != "" {
		if err := os.WriteFile(path, seed, 0o600); err != nil {
			return nil, fmt.Errorf("controller key: %w", err)
		}
	}
	return ed25519.NewKeyFromSeed(seed), nil
}

// SignRoster sets Pub and Sig over r.Body.
func SignRoster(priv ed25519.PrivateKey, r wire.Roster) wire.Roster {
	pub, ok := priv.Public().(ed25519.PublicKey)
	if !ok {
		return r
	}
	r.Pub = append([]byte(nil), pub...)
	r.Sig = ed25519.Sign(priv, r.Body())
	return r
}

// SignLists sets Pub and Sig over l.Body (same key as the roster).
func SignLists(priv ed25519.PrivateKey, l wire.Lists) wire.Lists {
	pub, ok := priv.Public().(ed25519.PublicKey)
	if !ok {
		return l
	}
	l.Pub = append([]byte(nil), pub...)
	l.Sig = ed25519.Sign(priv, l.Body())
	return l
}

// SignSlice sets Pub and Sig over s.Body.
func SignSlice(priv ed25519.PrivateKey, s wire.Slice) wire.Slice {
	pub, ok := priv.Public().(ed25519.PublicKey)
	if !ok {
		return s
	}
	s.Pub = append([]byte(nil), pub...)
	s.Sig = ed25519.Sign(priv, s.Body())
	return s
}

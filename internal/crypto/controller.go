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
	"time"

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

// SignKeyRoll is the document by which old hands trust to newPub.
func SignKeyRoll(old ed25519.PrivateKey, newPub ed25519.PublicKey, serial uint64, notAfter time.Time) wire.KeyRoll {
	k := wire.KeyRoll{
		Serial:   serial,
		NotAfter: notAfter.Unix(),
		Old:      append([]byte(nil), old.Public().(ed25519.PublicKey)...),
		New:      append([]byte(nil), newPub...),
	}
	k.Sig = ed25519.Sign(old, k.Body())
	return k
}

// RollControllerKey replaces the controller key in seedPath with a new random
// one and writes to rollPath a KeyRoll, signed by the old key, that clients
// and relays accept until now+window. The old seed is not kept: the roll is
// all that is needed to move pinned clients over. A previous roll's serial
// is continued so clients can refuse an older roll.
func RollControllerKey(seedPath, rollPath string, window time.Duration) (wire.KeyRoll, error) {
	b, err := os.ReadFile(seedPath)
	if err != nil || len(b) < ed25519.SeedSize {
		return wire.KeyRoll{}, fmt.Errorf("controller key: no seed in %s", seedPath)
	}
	old := ed25519.NewKeyFromSeed(b[:ed25519.SeedSize])
	seed := make([]byte, ed25519.SeedSize)
	if _, err := io.ReadFull(entropy, seed); err != nil {
		return wire.KeyRoll{}, err
	}
	next := ed25519.NewKeyFromSeed(seed)
	serial := uint64(1)
	if prev, err := os.ReadFile(rollPath); err == nil {
		if k, err := wire.DecodeKeyRoll(prev); err == nil {
			serial = k.Serial + 1
		}
	}
	k := SignKeyRoll(old, next.Public().(ed25519.PublicKey), serial, time.Now().Add(window))
	if err := writeFileAtomic(rollPath, k.Encode(), 0o600); err != nil {
		return wire.KeyRoll{}, fmt.Errorf("key roll: %w", err)
	}
	if err := writeFileAtomic(seedPath, seed, 0o600); err != nil {
		return wire.KeyRoll{}, fmt.Errorf("controller key: %w", err)
	}
	return k, nil
}

// LoadKeyRoll returns the encoded roll in path while it is valid for pub
// (signed by its old key, pointing at pub, not expired); nil otherwise.
func LoadKeyRoll(path string, pub ed25519.PublicKey) []byte {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	k, err := wire.DecodeKeyRoll(b)
	if err != nil || string(k.New) != string(pub) || k.Verify(time.Now()) != nil {
		return nil
	}
	return b
}

// writeFileAtomic writes through a temp file in the same directory.
func writeFileAtomic(path string, b []byte, mode os.FileMode) error {
	tmp := path + ".new"
	if err := os.WriteFile(tmp, b, mode); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

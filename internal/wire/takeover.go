package wire

// Takeover: a standby has become the controller.
//
// A promoted standby holds the controller's own key, so clients and relays
// cannot tell it from the old controller by signatures alone. If the old
// controller comes back, both would run as controllers side by side. To
// stop that, every promotion signs a Takeover with a higher Epoch. Relays
// keep the highest Takeover they have seen and hand it to every controller
// they reach; a controller that sees a valid Takeover with a higher epoch
// than its own knows it has been replaced and stops.

import (
	"crypto/ed25519"
	"encoding/binary"
	"errors"
)

// takeoverMagic starts the signed body, so a Takeover signature can never
// be taken for any other controller signature.
const takeoverMagic = "lft-take"

// Takeover says: from At (unix seconds) the controller is the standby By,
// generation Epoch. Sig is by the controller key over Body.
type Takeover struct {
	Epoch uint64
	At    int64
	By    string
	Sig   []byte
}

// Body is "lft-take" || u64be epoch || u64be at || u8 len || by.
func (t Takeover) Body() []byte {
	b := append([]byte(takeoverMagic), make([]byte, 16)...)
	binary.BigEndian.PutUint64(b[len(takeoverMagic):], t.Epoch)
	binary.BigEndian.PutUint64(b[len(takeoverMagic)+8:], uint64(t.At))
	by := t.By
	if len(by) > 255 {
		by = by[:255]
	}
	b = append(b, byte(len(by)))
	return append(b, by...)
}

// Encode is Body || sig[64].
func (t Takeover) Encode() []byte { return append(t.Body(), t.Sig...) }

// DecodeTakeover parses Encode. The signature is not checked here (Verify).
func DecodeTakeover(b []byte) (Takeover, error) {
	n := len(takeoverMagic)
	if len(b) < n+17+64 || string(b[:n]) != takeoverMagic {
		return Takeover{}, errors.New("wire: takeover form")
	}
	l := int(b[n+16])
	if len(b) != n+17+l+64 {
		return Takeover{}, errors.New("wire: takeover form")
	}
	return Takeover{
		Epoch: binary.BigEndian.Uint64(b[n:]),
		At:    int64(binary.BigEndian.Uint64(b[n+8:])),
		By:    string(b[n+17 : n+17+l]),
		Sig:   append([]byte(nil), b[n+17+l:]...),
	}, nil
}

// Sign fills Sig with the controller key.
func (t *Takeover) Sign(priv ed25519.PrivateKey) { t.Sig = ed25519.Sign(priv, t.Body()) }

// Verify checks the signature by the controller key pub.
func (t Takeover) Verify(pub []byte) error {
	if len(pub) != ed25519.PublicKeySize || len(t.Sig) != ed25519.SignatureSize || !ed25519.Verify(ed25519.PublicKey(pub), t.Body(), t.Sig) {
		return errors.New("wire: takeover sig")
	}
	return nil
}

package wire

// KeyRoll: the controller's old signing key hands trust to a new one.
//
// Clients and relays pin the controller's Ed25519 public key and accept a
// roster, lists or slice only under it. To change that key without
// reissuing every client, the controller signs one KeyRoll with its old key:
// "from now on, trust this new key". A client or relay that still pins the
// old key and sees a valid, unexpired KeyRoll pins the new key instead. The
// old key is not needed afterwards: the roll is signed once, when the key
// changes, and published until NotAfter.

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/binary"
	"errors"
	"time"
)

// keyRollMagic starts the signed body, so a KeyRoll signature can never be
// taken for a roster, lists or slice signature.
const keyRollMagic = "lft-roll"

// KeyRoll is old → new, signed by old. Serial grows with every change;
// NotAfter (unix seconds) is when the controller stops publishing it.
type KeyRoll struct {
	Serial   uint64
	NotAfter int64
	Old      []byte // 32-byte Ed25519 public key that signs
	New      []byte // 32-byte Ed25519 public key to trust next
	Sig      []byte // 64-byte Ed25519 signature by Old over Body
}

// KeyRollSize is the encoded length: body ("lft-roll", serial, not_after, two keys) and signature.
const KeyRollSize = len(keyRollMagic) + 8 + 8 + 32 + 32 + 64

// Body is "lft-roll" || u64be serial || u64be not_after || old[32] || new[32].
func (k KeyRoll) Body() []byte {
	b := make([]byte, 0, KeyRollSize-64)
	b = append(b, keyRollMagic...)
	b = binary.BigEndian.AppendUint64(b, k.Serial)
	b = binary.BigEndian.AppendUint64(b, uint64(k.NotAfter))
	b = append(b, k.Old...)
	return append(b, k.New...)
}

// Encode is Body || sig[64].
func (k KeyRoll) Encode() []byte { return append(k.Body(), k.Sig...) }

// DecodeKeyRoll parses Encode. The signature is not checked here (Verify).
func DecodeKeyRoll(b []byte) (KeyRoll, error) {
	if len(b) != KeyRollSize || string(b[:len(keyRollMagic)]) != keyRollMagic {
		return KeyRoll{}, errors.New("wire: key roll form")
	}
	b = b[len(keyRollMagic):]
	k := KeyRoll{
		Serial:   binary.BigEndian.Uint64(b[0:8]),
		NotAfter: int64(binary.BigEndian.Uint64(b[8:16])),
		Old:      append([]byte(nil), b[16:48]...),
		New:      append([]byte(nil), b[48:80]...),
		Sig:      append([]byte(nil), b[80:144]...),
	}
	return k, nil
}

// Verify checks the signature by Old and that the roll has not expired.
func (k KeyRoll) Verify(now time.Time) error {
	if len(k.Old) != ed25519.PublicKeySize || len(k.New) != ed25519.PublicKeySize || len(k.Sig) != ed25519.SignatureSize {
		return errors.New("wire: key roll key")
	}
	if string(k.Old) == string(k.New) {
		return errors.New("wire: key roll to the same key")
	}
	if !ed25519.Verify(ed25519.PublicKey(k.Old), k.Body(), k.Sig) {
		return errors.New("wire: key roll sig")
	}
	if now.Unix() > k.NotAfter {
		return errors.New("wire: key roll expired")
	}
	return nil
}

// RollField is the manifest form of an encoded KeyRoll (base64url, no padding).
func RollField(raw []byte) string {
	if len(raw) == 0 {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

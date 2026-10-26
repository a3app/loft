package wire

// Witness: a relay tells a standby controller whether it reaches the
// controller.
//
// A standby that may promote itself must not do so just because its own link
// to the controller broke. So every relay sends each standby a short report
// every few seconds: "I do (not) reach the controller". The report is signed
// with the relay's own key (the standby has the controller's relays.keys), so
// nobody else can vote, and it carries the time, so an old one cannot be
// replayed later.

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"time"
)

const witnessMagic = "lft-wit"

// Witness is one report: relay ID, when, and whether it reaches the controller.
type Witness struct {
	ID   string
	At   int64 // unix seconds
	Sees bool
}

func (w Witness) body() []byte {
	b := append([]byte(witnessMagic), putStr(w.ID)...)
	b = binary.BigEndian.AppendUint64(b, uint64(w.At))
	if w.Sees {
		return append(b, 1)
	}
	return append(b, 0)
}

// EncodeWitness is "lft-wit" || u8 id_len || id || u64be at || u8 sees ||
// HMAC-SHA256(relay key, everything before)[0:16].
func EncodeWitness(w Witness, key []byte) []byte {
	b := w.body()
	m := hmac.New(sha256.New, key)
	m.Write(b)
	return append(b, m.Sum(nil)[:16]...)
}

// WitnessID reads the relay ID of a report, to find its key.
func WitnessID(b []byte) (string, error) {
	if len(b) < len(witnessMagic)+1 || string(b[:len(witnessMagic)]) != witnessMagic {
		return "", errors.New("wire: witness form")
	}
	n := int(b[len(witnessMagic)])
	if len(b) < len(witnessMagic)+1+n {
		return "", errors.New("wire: witness form")
	}
	return string(b[len(witnessMagic)+1 : len(witnessMagic)+1+n]), nil
}

// DecodeWitness checks the HMAC under key and that the report is no more
// than maxAge away from now.
func DecodeWitness(b, key []byte, now time.Time, maxAge time.Duration) (Witness, error) {
	id, err := WitnessID(b)
	if err != nil {
		return Witness{}, err
	}
	off := len(witnessMagic) + 1 + len(id)
	if len(b) != off+8+1+16 {
		return Witness{}, errors.New("wire: witness form")
	}
	w := Witness{ID: id, At: int64(binary.BigEndian.Uint64(b[off : off+8])), Sees: b[off+8] == 1}
	m := hmac.New(sha256.New, key)
	m.Write(b[:off+9])
	if !hmac.Equal(m.Sum(nil)[:16], b[off+9:]) {
		return Witness{}, errors.New("wire: witness mac")
	}
	if d := now.Sub(time.Unix(w.At, 0)); d > maxAge || d < -maxAge {
		return Witness{}, errors.New("wire: witness too old")
	}
	return w, nil
}

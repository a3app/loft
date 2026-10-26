package wire

// Controller-signed roster of live fronts, plus reserved hub/ctl mux targets.
//
// The roster tells a client every place it can connect to (the controller
// and its relays). The client keeps it and fails over to another entry when
// its current one stops working. The controller signs it with Ed25519, so
// a relay cannot add itself or others to the list.

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
)

// Reserved mux Open targets on a relay trunk: hub:1 HTTP/1.1, hub:2 HTTP/2, ctl:1 fleet.
const (
	HubHost     = "hub"
	HubHTTPPort = "1"
	HubH2Port   = "2"
	CtlHost     = "ctl"
	CtlPort     = "1"
)

// RelayInfo is one advertised front. The controller signs the roster; a
// relay cannot mint entries.
type RelayInfo struct {
	ID      string
	URL     string
	Decoy   string
	Fronts  []string
	Drain   bool
	Weight  byte   // 1–255; 0 encodes as 100
	Path    string // get/ws/h2 path; empty = node default
	MaskPub []byte // 32-byte mask pub for this front; empty = none
}

// Roster is the controller's signed list of live fronts.
type Roster struct {
	Serial uint64
	Relays []RelayInfo
	Pub    []byte // Ed25519 public, 32 bytes
	Sig    []byte
}

// Body is the signed fields without pub/sig. At most 255 relays; weight 0 encodes as 100.
func (r Roster) Body() []byte {
	n := len(r.Relays)
	if n > 255 {
		n = 255
	}
	var b []byte
	var ser [8]byte
	binary.BigEndian.PutUint64(ser[:], r.Serial)
	b = append(b, ser[:]...)
	b = append(b, byte(n))
	for i := 0; i < n; i++ {
		e := r.Relays[i]
		b = append(b, putStr(e.ID)...)
		b = append(b, putLong(e.URL)...)
		b = append(b, putStr(e.Decoy)...)
		fn := len(e.Fronts)
		if fn > 16 {
			fn = 16
		}
		b = append(b, byte(fn))
		for j := 0; j < fn; j++ {
			b = append(b, putStr(e.Fronts[j])...)
		}
		if e.Drain {
			b = append(b, 1)
		} else {
			b = append(b, 0)
		}
		w := e.Weight
		if w == 0 {
			w = 100
		}
		b = append(b, w)
		b = append(b, putStr(e.Path)...)
		ml := len(e.MaskPub)
		if ml > 32 {
			ml = 32
		}
		b = append(b, byte(ml))
		b = append(b, e.MaskPub[:ml]...)
	}
	return b
}

// Encode is Body || u8 pub_len || pub || u16be sig_len || sig.
func (r Roster) Encode() []byte {
	body := r.Body()
	pub := r.Pub
	if len(pub) > 32 {
		pub = pub[:32]
	}
	sig := r.Sig
	if len(sig) > 64 {
		sig = sig[:64]
	}
	out := append([]byte{}, body...)
	out = append(out, byte(len(pub)))
	out = append(out, pub...)
	var sl [2]byte
	binary.BigEndian.PutUint16(sl[:], uint16(len(sig)))
	out = append(out, sl[:]...)
	return append(out, sig...)
}

// DecodeRoster reads a signed roster. Trailing pub/sig and newer fields may be omitted.
func DecodeRoster(b []byte) (Roster, error) {
	var r Roster
	if len(b) < 9 {
		return r, ErrShort
	}
	r.Serial = binary.BigEndian.Uint64(b[:8])
	n := int(b[8])
	off := 9
	for i := 0; i < n; i++ {
		id, k := getStr(b[off:])
		if k == 0 {
			return r, ErrShort
		}
		off += k
		url, k, err := getLong(b[off:])
		if err != nil {
			return r, err
		}
		off += k
		decoy, k := getStr(b[off:])
		off += k
		if off >= len(b) {
			return r, ErrShort
		}
		fn := int(b[off])
		off++
		var fronts []string
		for j := 0; j < fn; j++ {
			f, k := getStr(b[off:])
			off += k
			if f != "" {
				fronts = append(fronts, f)
			}
		}
		if off >= len(b) {
			return r, ErrShort
		}
		drain := b[off] != 0
		off++
		w := byte(100)
		if off < len(b) {
			w = b[off]
			off++
			if w == 0 {
				w = 100
			}
		}
		var path string
		var mask []byte
		if off < len(b) {
			path, k = getStr(b[off:])
			off += k
		}
		if off < len(b) {
			ml := int(b[off])
			off++
			if ml > 0 && off+ml <= len(b) {
				mask = append([]byte(nil), b[off:off+ml]...)
				off += ml
			}
		}
		r.Relays = append(r.Relays, RelayInfo{ID: id, URL: url, Decoy: decoy, Fronts: fronts, Drain: drain, Weight: w, Path: path, MaskPub: mask})
	}
	if off >= len(b) {
		return r, nil
	}
	pl := int(b[off])
	off++
	if pl > 0 {
		if off+pl > len(b) {
			return r, ErrShort
		}
		r.Pub = append([]byte(nil), b[off:off+pl]...)
		off += pl
	}
	if off+2 <= len(b) {
		sl := int(binary.BigEndian.Uint16(b[off : off+2]))
		off += 2
		if sl > 0 && off+sl <= len(b) {
			r.Sig = append([]byte(nil), b[off:off+sl]...)
		}
	}
	return r, nil
}

// Verify checks Ed25519 over Body with a 32-byte pub and 64-byte sig.
func (r Roster) Verify() error {
	if len(r.Pub) != ed25519.PublicKeySize || len(r.Sig) != ed25519.SignatureSize {
		return errors.New("wire: roster key")
	}
	if !ed25519.Verify(ed25519.PublicKey(r.Pub), r.Body(), r.Sig) {
		return errors.New("wire: roster sig")
	}
	return nil
}

// putLong is u16be length || bytes, at most 255 (URLs and list values).
func putLong(s string) []byte {
	b := []byte(s)
	if len(b) > 255 {
		b = b[:255]
	}
	out := make([]byte, 2+len(b))
	binary.BigEndian.PutUint16(out[:2], uint16(len(b)))
	copy(out[2:], b)
	return out
}

// getLong reads u16be length || bytes.
func getLong(b []byte) (string, int, error) {
	if len(b) < 2 {
		return "", 0, ErrShort
	}
	n := int(binary.BigEndian.Uint16(b[:2]))
	if 2+n > len(b) {
		return "", 0, ErrShort
	}
	return string(b[2 : 2+n]), 2 + n, nil
}

// Manifest is the public cover document: looks like a frontend bundle
// pointer, payload is base64url(Roster.Encode()). No member secrets.
// The same shape carries Lists (see Lists.ManifestJSON).
type Manifest struct {
	V int    `json:"v"`
	P string `json:"p"`
}

// ManifestJSON is the public cover document: {"v":1,"p":"<base64url(Roster.Encode())>"}.
func (r Roster) ManifestJSON() ([]byte, error) {
	m := Manifest{V: 1, P: base64.RawURLEncoding.EncodeToString(r.Encode())}
	return json.Marshal(m)
}

// ParseManifest accepts JSON Manifest or a raw Encode blob.
func ParseManifest(b []byte) (Roster, error) {
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return DecodeRoster(b)
	}
	if m.P == "" {
		return Roster{}, errors.New("wire: empty manifest")
	}
	raw, err := base64.RawURLEncoding.DecodeString(m.P)
	if err != nil {
		raw, err = base64.URLEncoding.DecodeString(m.P)
		if err != nil {
			return Roster{}, err
		}
	}
	return DecodeRoster(raw)
}

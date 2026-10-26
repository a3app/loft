package wire

// Fleet control on mux Open ctl:1 (relay trunk only): enrol, beat, slice, drain.
//
// A fleet is one controller node plus relay nodes (extra entry points that
// forward to the controller). A relay connects to the controller like a
// member, then opens the service stream ctl:1 and talks this protocol:
//   - Enrol: the relay asks to be published (EnrolOK or EnrolNo back);
//   - Beat: periodic heartbeat with load and connection count;
//   - Slice: the controller's signed settings for this relay;
//   - Drain: stop taking new clients (for maintenance), or resume.

import (
	"crypto/ed25519"
	"encoding/binary"
	"errors"
	"time"
)

// Control messages on mux Open ctl:1 (relay trunk only).
const (
	FltEnrolReq byte = 1
	FltEnrolOK  byte = 2
	FltEnrolNo  byte = 3
	FltBeat     byte = 4
	FltSlice    byte = 5
	FltDrain    byte = 6
	FltKeyRoll  byte = 7 // the controller's key changes: an encoded KeyRoll, before enrol_ok
)

// EnrolNo reason codes: unknown id, bad form, duplicate, disabled.
const (
	EnrolUnknown  uint16 = 1
	EnrolForm     uint16 = 2
	EnrolDup      uint16 = 3
	EnrolDisabled uint16 = 4
)

// EncodeFlt is u8 type || body (the outer u32be length is the length-prefixed wrapper).
func EncodeFlt(kind byte, body []byte) []byte {
	out := make([]byte, 1, 1+len(body))
	out[0] = kind
	return append(out, body...)
}

// DecodeFlt splits u8 type || body.
func DecodeFlt(b []byte) (kind byte, body []byte, err error) {
	if len(b) < 1 {
		return 0, nil, ErrShort
	}
	return b[0], b[1:], nil
}

// EnrolReq is a relay asking the controller to publish it.
type EnrolReq struct {
	ID     string
	URL    string
	Decoy  string
	Fronts []string
}

// Encode is id, url, decoy, then at most 16 front names.
func (e EnrolReq) Encode() []byte {
	var b []byte
	b = append(b, putStr(e.ID)...)
	b = append(b, putLong(e.URL)...)
	b = append(b, putStr(e.Decoy)...)
	n := len(e.Fronts)
	if n > 16 {
		n = 16
	}
	b = append(b, byte(n))
	for i := 0; i < n; i++ {
		b = append(b, putStr(e.Fronts[i])...)
	}
	return b
}

// DecodeEnrolReq reads an EnrolReq. Empty id or url is an error.
func DecodeEnrolReq(b []byte) (EnrolReq, error) {
	var e EnrolReq
	id, k := getStr(b)
	if k == 0 {
		return e, ErrShort
	}
	off := k
	url, k, err := getLong(b[off:])
	if err != nil {
		return e, err
	}
	off += k
	decoy, k := getStr(b[off:])
	off += k
	if off >= len(b) {
		return e, ErrShort
	}
	n := int(b[off])
	off++
	e.ID, e.URL, e.Decoy = id, url, decoy
	for i := 0; i < n && off < len(b); i++ {
		f, k := getStr(b[off:])
		off += k
		if f != "" {
			e.Fronts = append(e.Fronts, f)
		}
	}
	if e.ID == "" || e.URL == "" {
		return e, errors.New("wire: enrol id/url")
	}
	return e, nil
}

// EncodeEnrolNo is u16be code || reason (u8 length, at most 64 bytes).
func EncodeEnrolNo(code uint16, reason string) []byte {
	var c [2]byte
	binary.BigEndian.PutUint16(c[:], code)
	return append(c[:], putStr(reason)...)
}

// DecodeEnrolNo reads an EnrolNo. A short blob is EnrolForm with empty reason.
func DecodeEnrolNo(b []byte) (uint16, string) {
	if len(b) < 2 {
		return EnrolForm, ""
	}
	code := binary.BigEndian.Uint16(b[:2])
	s, _ := getStr(b[2:])
	return code, s
}

// Beat is a relay heartbeat: load, live connections, program name, uptime.
type Beat struct {
	Load   byte
	Conns  uint32
	Prog   string
	Uptime uint32 // seconds
}

// Encode is load u8, conns u32be, program, uptime_s u32be.
func (b Beat) Encode() []byte {
	var n [4]byte
	binary.BigEndian.PutUint32(n[:], b.Conns)
	out := []byte{b.Load}
	out = append(out, n[:]...)
	out = append(out, putStr(b.Prog)...)
	binary.BigEndian.PutUint32(n[:], b.Uptime)
	return append(out, n[:]...)
}

// DecodeBeat reads a Beat. Trailing uptime is optional for older relays.
func DecodeBeat(b []byte) (Beat, error) {
	var x Beat
	if len(b) < 5 {
		return x, ErrShort
	}
	x.Load = b[0]
	x.Conns = binary.BigEndian.Uint32(b[1:5])
	s, k := getStr(b[5:])
	x.Prog = s
	off := 5 + k
	if off+4 <= len(b) {
		x.Uptime = binary.BigEndian.Uint32(b[off : off+4])
	}
	return x, nil
}

// Slice is the controller's signed config for one relay.
type Slice struct {
	Serial  uint64
	ID      string
	URL     string
	Decoy   string
	Fronts  []string
	Drain   bool
	Weight  byte
	BeatMS  uint32
	Expires int64
	Pub     []byte
	Sig     []byte
}

// Body is the signed slice fields without pub/sig. Weight 0 encodes as 100.
func (s Slice) Body() []byte {
	var ser [8]byte
	binary.BigEndian.PutUint64(ser[:], s.Serial)
	b := append([]byte{}, ser[:]...)
	b = append(b, putStr(s.ID)...)
	b = append(b, putLong(s.URL)...)
	b = append(b, putStr(s.Decoy)...)
	n := len(s.Fronts)
	if n > 16 {
		n = 16
	}
	b = append(b, byte(n))
	for i := 0; i < n; i++ {
		b = append(b, putStr(s.Fronts[i])...)
	}
	if s.Drain {
		b = append(b, 1)
	} else {
		b = append(b, 0)
	}
	w := s.Weight
	if w == 0 {
		w = 100
	}
	b = append(b, w)
	var u [4]byte
	binary.BigEndian.PutUint32(u[:], s.BeatMS)
	b = append(b, u[:]...)
	var exp [8]byte
	binary.BigEndian.PutUint64(exp[:], uint64(s.Expires))
	return append(b, exp[:]...)
}

// Encode is Body || u8 pub_len || pub || u16be sig_len || sig.
func (s Slice) Encode() []byte {
	body := s.Body()
	pub, sig := s.Pub, s.Sig
	if len(pub) > 32 {
		pub = pub[:32]
	}
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

// DecodeSlice reads a signed slice. Trailing pub/sig may be omitted.
func DecodeSlice(b []byte) (Slice, error) {
	var s Slice
	if len(b) < 8 {
		return s, ErrShort
	}
	s.Serial = binary.BigEndian.Uint64(b[:8])
	off := 8
	id, k := getStr(b[off:])
	off += k
	url, k, err := getLong(b[off:])
	if err != nil {
		return s, err
	}
	off += k
	decoy, k := getStr(b[off:])
	off += k
	if off >= len(b) {
		return s, ErrShort
	}
	n := int(b[off])
	off++
	s.ID, s.URL, s.Decoy = id, url, decoy
	for i := 0; i < n && off < len(b); i++ {
		f, k := getStr(b[off:])
		off += k
		if f != "" {
			s.Fronts = append(s.Fronts, f)
		}
	}
	if off >= len(b) {
		return s, ErrShort
	}
	s.Drain = b[off] != 0
	off++
	if off >= len(b) {
		return s, ErrShort
	}
	s.Weight = b[off]
	if s.Weight == 0 {
		s.Weight = 100
	}
	off++
	if off+4 > len(b) {
		return s, ErrShort
	}
	s.BeatMS = binary.BigEndian.Uint32(b[off : off+4])
	off += 4
	if off+8 > len(b) {
		return s, ErrShort
	}
	s.Expires = int64(binary.BigEndian.Uint64(b[off : off+8]))
	off += 8
	if off >= len(b) {
		return s, nil
	}
	pl := int(b[off])
	off++
	if pl > 0 {
		if off+pl > len(b) {
			return s, ErrShort
		}
		s.Pub = append([]byte(nil), b[off:off+pl]...)
		off += pl
	}
	if off+2 <= len(b) {
		sl := int(binary.BigEndian.Uint16(b[off : off+2]))
		off += 2
		if sl > 0 && off+sl <= len(b) {
			s.Sig = append([]byte(nil), b[off:off+sl]...)
		}
	}
	return s, nil
}

// Verify checks Ed25519 over Body and rejects a past Expires.
func (s Slice) Verify() error {
	if len(s.Pub) != ed25519.PublicKeySize || len(s.Sig) != ed25519.SignatureSize {
		return errors.New("wire: slice key")
	}
	if !ed25519.Verify(ed25519.PublicKey(s.Pub), s.Body(), s.Sig) {
		return errors.New("wire: slice sig")
	}
	if s.Expires > 0 && time.Now().Unix() > s.Expires {
		return errors.New("wire: slice expired")
	}
	return nil
}

// EncodeDrain is id || u8 (1 = drain).
func EncodeDrain(id string, drain bool) []byte {
	d := byte(0)
	if drain {
		d = 1
	}
	return append(putStr(id), d)
}

// DecodeDrain reads id and drain flag.
func DecodeDrain(b []byte) (id string, drain bool) {
	id, k := getStr(b)
	if k < len(b) {
		drain = b[k] != 0
	}
	return id, drain
}

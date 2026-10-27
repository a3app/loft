package wire

// Signed named address lists. Names have no protocol meaning; the client maps them to direct / via / drop.
//
// The controller signs the lists with Ed25519, so a relay or a CDN in the
// middle cannot change which traffic goes direct. Status carries only the
// serial and hash; the client downloads the full blob when they change.

import (
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"net"
	"strings"
)

// Address-list entry kinds: exact domain, suffix, CIDR.
// 4–6 are profile metadata and never match a host or an IP (§10.3).
const (
	KindExact  byte = 1
	KindSuffix byte = 2
	KindCIDR   byte = 3
	KindDir    byte = 4 // via | direct | drop
	KindLabel  byte = 5 // display name
	KindDNS    byte = 6 // local resolver for direct sets: IP or DoH URL
)

// Operator direction strings carried in kind 4.
const (
	DirVia    = "via"
	DirDirect = "direct"
	DirDrop   = "drop"
)

// Addr is one list entry: kind plus value (domain, suffix, or CIDR).
type Addr struct {
	Kind byte
	Val  string
}

// AddrList is one named set of addresses.
type AddrList struct {
	Name    string
	Entries []Addr
}

// Lists is a signed set of named address lists. Names have no protocol
// meaning; the client maps them to direct / via / drop.
type Lists struct {
	Serial uint64
	Sets   []AddrList
	Pub    []byte
	Sig    []byte
}

// ParseAddr classifies a YAML/operator string: CIDR, leading . or *., else exact.
func ParseAddr(s string) Addr {
	s = strings.TrimSpace(s)
	if s == "" {
		return Addr{}
	}
	if _, _, err := net.ParseCIDR(s); err == nil {
		return Addr{Kind: KindCIDR, Val: s}
	}
	if strings.HasPrefix(s, ".") || strings.HasPrefix(s, "*.") {
		s = strings.TrimPrefix(s, "*")
		return Addr{Kind: KindSuffix, Val: strings.ToLower(strings.TrimPrefix(s, "."))}
	}
	return Addr{Kind: KindExact, Val: strings.ToLower(s)}
}

// ParseListEntry classifies a lists.conf value: @dir/@label/@dns/@name
// metadata (kinds 4–6), else ParseAddr. Unknown @keys are empty.
func ParseListEntry(s string) Addr {
	s = strings.TrimSpace(s)
	if s == "" {
		return Addr{}
	}
	if strings.HasPrefix(s, "@") {
		key, val, _ := strings.Cut(s[1:], " ")
		key = strings.ToLower(strings.TrimSpace(key))
		val = strings.TrimSpace(val)
		switch key {
		case "dir":
			d := strings.ToLower(val)
			if d == DirVia || d == DirDirect || d == DirDrop {
				return Addr{Kind: KindDir, Val: d}
			}
		case "label", "name":
			if val != "" {
				return Addr{Kind: KindLabel, Val: val}
			}
		case "dns":
			if val != "" {
				return Addr{Kind: KindDNS, Val: val}
			}
		}
		return Addr{}
	}
	return ParseAddr(s)
}

// MatchHost is case-insensitive exact or suffix match. CIDR never matches a host.
func (a Addr) MatchHost(host string) bool {
	host = strings.ToLower(strings.TrimSpace(host))
	if i := strings.IndexByte(host, ':'); i >= 0 && net.ParseIP(host) == nil {
		host = host[:i]
	}
	switch a.Kind {
	case KindExact:
		return host == a.Val
	case KindSuffix:
		return host == a.Val || strings.HasSuffix(host, "."+a.Val)
	default:
		return false
	}
}

// MatchIP is CIDR containment. Exact and suffix never match an IP.
func (a Addr) MatchIP(ip net.IP) bool {
	if a.Kind != KindCIDR || ip == nil {
		return false
	}
	_, n, err := net.ParseCIDR(a.Val)
	if err != nil {
		return false
	}
	return n.Contains(ip)
}

// Body is the signed fields: serial, at most 255 sets, 65535 entries each.
func (l Lists) Body() []byte {
	n := len(l.Sets)
	if n > 255 {
		n = 255
	}
	var ser [8]byte
	binary.BigEndian.PutUint64(ser[:], l.Serial)
	b := append([]byte{}, ser[:]...)
	b = append(b, byte(n))
	for i := 0; i < n; i++ {
		s := l.Sets[i]
		b = append(b, putStr(s.Name)...)
		en := len(s.Entries)
		if en > 0xffff {
			en = 0xffff
		}
		var ne [2]byte
		binary.BigEndian.PutUint16(ne[:], uint16(en))
		b = append(b, ne[:]...)
		for j := 0; j < en; j++ {
			e := s.Entries[j]
			b = append(b, e.Kind)
			b = append(b, putLong(e.Val)...)
		}
	}
	return b
}

// Encode is Body || u8 pub_len || pub || u16be sig_len || sig.
func (l Lists) Encode() []byte {
	body := l.Body()
	pub, sig := l.Pub, l.Sig
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

// Hash is SHA-256 of Encode, carried in Status lists_hash.
func (l Lists) Hash() [32]byte { return sha256.Sum256(l.Encode()) }

// DecodeLists reads a signed lists blob. Trailing pub/sig may be omitted.
func DecodeLists(b []byte) (Lists, error) {
	var l Lists
	if len(b) < 9 {
		return l, ErrShort
	}
	l.Serial = binary.BigEndian.Uint64(b[:8])
	n := int(b[8])
	off := 9
	for i := 0; i < n; i++ {
		name, k := getStr(b[off:])
		if k == 0 {
			return l, ErrShort
		}
		off += k
		if off+2 > len(b) {
			return l, ErrShort
		}
		en := int(binary.BigEndian.Uint16(b[off : off+2]))
		off += 2
		al := AddrList{Name: name}
		for j := 0; j < en; j++ {
			if off >= len(b) {
				return l, ErrShort
			}
			kind := b[off]
			off++
			val, k, err := getLong(b[off:])
			if err != nil {
				return l, err
			}
			off += k
			al.Entries = append(al.Entries, Addr{Kind: kind, Val: val})
		}
		l.Sets = append(l.Sets, al)
	}
	if off >= len(b) {
		return l, nil
	}
	pl := int(b[off])
	off++
	if pl > 0 {
		if off+pl > len(b) {
			return l, ErrShort
		}
		l.Pub = append([]byte(nil), b[off:off+pl]...)
		off += pl
	}
	if off+2 <= len(b) {
		sl := int(binary.BigEndian.Uint16(b[off : off+2]))
		off += 2
		if sl > 0 && off+sl <= len(b) {
			l.Sig = append([]byte(nil), b[off:off+sl]...)
		}
	}
	return l, nil
}

// Verify checks Ed25519 over Body with a 32-byte pub and 64-byte sig.
func (l Lists) Verify() error {
	if len(l.Pub) != ed25519.PublicKeySize || len(l.Sig) != ed25519.SignatureSize {
		return errors.New("wire: lists key")
	}
	if !ed25519.Verify(ed25519.PublicKey(l.Pub), l.Body(), l.Sig) {
		return errors.New("wire: lists sig")
	}
	return nil
}

// ManifestJSON is the public cover document: {"v":1,"p":"<base64url>"}.
func (l Lists) ManifestJSON() ([]byte, error) {
	m := Manifest{V: 1, P: base64.RawURLEncoding.EncodeToString(l.Encode())}
	return json.Marshal(m)
}

// ParseListsManifest accepts JSON Manifest or a raw Encode blob.
func ParseListsManifest(b []byte) (Lists, error) {
	var m Manifest
	if err := json.Unmarshal(b, &m); err != nil {
		return DecodeLists(b)
	}
	if m.P == "" {
		return Lists{}, errors.New("wire: empty lists manifest")
	}
	raw, err := base64.RawURLEncoding.DecodeString(m.P)
	if err != nil {
		raw, err = base64.URLEncoding.DecodeString(m.P)
		if err != nil {
			return Lists{}, err
		}
	}
	return DecodeLists(raw)
}

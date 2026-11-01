package transport

// QUIC mark: how a decoy node tells a loft QUIC Initial from a browser's.
//
// On TCP the loft mark sits in the TLS session_id (hellopeek.go). QUIC has
// no such field, so the mark is split over two values a client chooses
// freely: the source connection ID (SCID) and the TLS client_random. The
// Initial is the first QUIC packet of a connection; it carries the
// ClientHello, encrypted with keys anyone can derive, so the node can read
// both values before it answers (initialHello below).
//
// The client picks its own source connection ID and TLS client_random, and
// both are readable in the Initial (its keys come from the public DCID, RFC
// 9001, section 5.2). client_random carries an ephemeral X25519 public and the
// 20-byte SCID is HKDF over ECDH(that ephemeral, node mask_pub): v3, which
// needs the node private to check. A client without mask_pub uses v2, HKDF
// under its member key. Short-header packets are routed by the node's own
// connection IDs, which carry an HMAC (serverCIDs), so NAT rebinding keeps
// the flow on loft.

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"sync"

	"github.com/quic-go/quic-go"
	"golang.org/x/crypto/curve25519"
)

const (
	quicMarkLen  = 20 // client SCID: the longest QUIC allows
	serverCIDLen = 12
	serverCIDTag = 6
)

// quicMark is the SCID for client_random under key (an ECDH secret or a member key).
func quicMark(v string, key, random []byte) []byte {
	out, err := hkdf.Key(sha256.New, key, random, "loft-quic-"+v, quicMarkLen)
	if err != nil {
		panic(err)
	}
	return out
}

// newQUICMark is a client's client_random and SCID: v3 under maskPub when
// it is known, else v2 under the member key.
// For v3 the client_random is the ephemeral X25519 public itself, with its
// top bit randomized, so the node can recover the public and redo the ECDH.
func newQUICMark(token, maskPub []byte) (random, scid []byte, err error) {
	random = make([]byte, 32)
	if len(maskPub) != 32 {
		if _, err := rand.Read(random); err != nil {
			return nil, nil, err
		}
		return random, quicMark("v2", token, random), nil
	}
	priv := make([]byte, 33)
	if _, err := rand.Read(priv); err != nil {
		return nil, nil, err
	}
	pub, err := curve25519.X25519(priv[:32], curve25519.Basepoint)
	if err != nil {
		return nil, nil, err
	}
	shared, err := curve25519.X25519(priv[:32], maskPub)
	if err != nil {
		return nil, nil, err
	}
	copy(random, pub)
	// An X25519 public never sets the top bit; a random one does half the time.
	random[31] |= priv[32] & 0x80
	return random, quicMark("v3", shared, random), nil
}

// verifyQUICMark reports whether scid marks random: v3 under the node
// mask key, then v2 under a member key (as verifyAny for TCP).
func verifyQUICMark(nodeToken, random, scid []byte) bool {
	return quicMarked(nodeToken, random, scid, true)
}

// quicMarked is verifyQUICMark. allowV2 is false on a mask node: a stolen
// member key must not mark the Initial.
func quicMarked(nodeToken, random, scid []byte, allowV2 bool) bool {
	if len(random) != 32 || len(scid) != quicMarkLen {
		return false
	}
	if len(nodeToken) > 0 {
		pub := append([]byte(nil), random...)
		pub[31] &= 0x7f
		if shared, err := curve25519.X25519(maskPrivFor(nodeToken), pub); err == nil && hmac.Equal(scid, quicMark("v3", shared, random)) {
			return true
		}
	}
	if !allowV2 {
		return false
	}
	for _, tok := range clientTokens(nodeToken) {
		if hmac.Equal(scid, quicMark("v2", tok, random)) {
			return true
		}
	}
	return false
}

var maskPrivCache sync.Map // token → X25519 private

// maskPrivFor is maskX25519Priv(token), cached: it runs for every Initial.
func maskPrivFor(token []byte) []byte {
	if v, ok := maskPrivCache.Load(string(token)); ok {
		return v.([]byte)
	}
	p := maskX25519Priv(token)
	maskPrivCache.Store(string(token), p)
	return p
}

// markRand is tls.Config.Rand for one dial: client_random first, then
// crypto/rand. crypto/tls reads client_random before anything else.
type markRand struct {
	mu    sync.Mutex
	first []byte
}

func (r *markRand) Read(b []byte) (int, error) {
	r.mu.Lock()
	first := r.first
	r.first = nil
	r.mu.Unlock()
	if first == nil {
		return rand.Read(b)
	}
	n := copy(b, first)
	if n < len(b) {
		if _, err := rand.Read(b[n:]); err != nil {
			return n, err
		}
	}
	return len(b), nil
}

// markCIDs is the client's connection ID generator: the mark first, random after.
// quic-go asks for the first ID when it builds the Initial, so that one
// carries the mark.
type markCIDs struct {
	mu    sync.Mutex
	first []byte
}

func (g *markCIDs) GenerateConnectionID() (quic.ConnectionID, error) {
	g.mu.Lock()
	first := g.first
	g.first = nil
	g.mu.Unlock()
	if first == nil {
		first = make([]byte, quicMarkLen)
		if _, err := rand.Read(first); err != nil {
			return quic.ConnectionID{}, err
		}
	}
	return quic.ConnectionIDFromBytes(first), nil
}

func (g *markCIDs) ConnectionIDLen() int { return quicMarkLen }

// serverCIDs are the node's connection IDs: random bytes and their HMAC, so
// a short-header packet from a new address is known as loft without state.
type serverCIDs struct{ key []byte }

// newServerCIDs derives the HMAC key for the node's connection IDs from token.
func newServerCIDs(token []byte) *serverCIDs {
	key, err := hkdf.Key(sha256.New, token, nil, "loft-quic-cid", 32)
	if err != nil {
		panic(err)
	}
	return &serverCIDs{key: key}
}

// tag is the truncated HMAC that fills the last serverCIDTag bytes of an ID.
func (s *serverCIDs) tag(r []byte) []byte {
	m := hmac.New(sha256.New, s.key)
	m.Write(r)
	return m.Sum(nil)[:serverCIDTag]
}

func (s *serverCIDs) GenerateConnectionID() (quic.ConnectionID, error) {
	b := make([]byte, serverCIDLen)
	if _, err := rand.Read(b[:serverCIDLen-serverCIDTag]); err != nil {
		return quic.ConnectionID{}, err
	}
	copy(b[serverCIDLen-serverCIDTag:], s.tag(b[:serverCIDLen-serverCIDTag]))
	return quic.ConnectionIDFromBytes(b), nil
}

func (s *serverCIDs) ConnectionIDLen() int { return serverCIDLen }

// owns reports whether a short-header packet is addressed to one of ours.
func (s *serverCIDs) owns(pkt []byte) bool {
	if len(pkt) < 1+serverCIDLen || pkt[0]&0x80 != 0 {
		return false
	}
	id := pkt[1 : 1+serverCIDLen]
	return hmac.Equal(id[serverCIDLen-serverCIDTag:], s.tag(id[:serverCIDLen-serverCIDTag]))
}

// QUIC version 1 Initial salt (RFC 9001, section 5.2).
var quicV1Salt = []byte{0x38, 0x76, 0x2c, 0xf7, 0xf5, 0x59, 0x34, 0xb3, 0x4d, 0x17, 0x9a, 0xe6, 0xa4, 0xc8, 0x0c, 0xad, 0xcc, 0xbb, 0x7f, 0x0a}

var (
	errNotInitial = errors.New("not a QUIC v1 Initial")
	errNoHello    = errors.New("Initial without the start of the ClientHello")
)

// initialHello reads a client QUIC v1 Initial: its source connection ID and
// the ClientHello client_random. errNoHello means a valid Initial whose
// first CRYPTO bytes are in another packet (that one was lost or reordered).
// It parses the long header, derives the Initial keys from the destination
// connection ID, removes header protection to learn the packet number, and
// decrypts the payload; it does not keep any state.
func initialHello(pkt []byte) (scid, random []byte, err error) {
	if len(pkt) < 7 || pkt[0]&0xf0 != 0xc0 || binary.BigEndian.Uint32(pkt[1:5]) != 1 {
		return nil, nil, errNotInitial
	}
	p := 5
	dl := int(pkt[p])
	p++
	if dl > 20 || p+dl >= len(pkt) {
		return nil, nil, errNotInitial
	}
	dcid := pkt[p : p+dl]
	p += dl
	sl := int(pkt[p])
	p++
	if sl > 20 || p+sl > len(pkt) {
		return nil, nil, errNotInitial
	}
	scid = pkt[p : p+sl]
	p += sl
	tl, n := quicVarint(pkt[p:])
	if n == 0 || uint64(len(pkt)-p-n) < tl {
		return nil, nil, errNotInitial
	}
	p += n + int(tl)
	ln, n := quicVarint(pkt[p:])
	if n == 0 {
		return nil, nil, errNotInitial
	}
	p += n
	if ln < 20 || uint64(len(pkt)-p) < ln || p+4+16 > len(pkt) {
		return nil, nil, errNotInitial
	}
	end := p + int(ln)

	secret := hkdfExtract(quicV1Salt, dcid)
	client := hkdfExpandLabel(secret, "client in", nil, 32)
	key := hkdfExpandLabel(client, "quic key", nil, 16)
	iv := hkdfExpandLabel(client, "quic iv", nil, 12)
	hp := hkdfExpandLabel(client, "quic hp", nil, 16)

	hb, err := aes.NewCipher(hp)
	if err != nil {
		return nil, nil, err
	}
	mask := make([]byte, 16)
	hb.Encrypt(mask, pkt[p+4:p+20])
	first := pkt[0] ^ mask[0]&0x0f
	pnLen := int(first&0x03) + 1
	hdr := append([]byte(nil), pkt[:p+pnLen]...)
	hdr[0] = first
	var pn uint64
	for i := 0; i < pnLen; i++ {
		hdr[p+i] ^= mask[1+i]
		pn = pn<<8 | uint64(hdr[p+i])
	}
	nonce := append([]byte(nil), iv...)
	for i := 0; i < 8; i++ {
		nonce[11-i] ^= byte(pn >> (8 * i))
	}
	kb, err := aes.NewCipher(key)
	if err != nil {
		return nil, nil, err
	}
	aead, err := cipher.NewGCM(kb)
	if err != nil {
		return nil, nil, err
	}
	plain, err := aead.Open(nil, nonce, pkt[p+pnLen:end], hdr)
	if err != nil {
		return nil, nil, errNotInitial
	}
	hello, err := cryptoAtZero(plain)
	if err != nil {
		return scid, nil, err
	}
	// Handshake header (type 1, length 3), legacy_version 2, random 32.
	if len(hello) < 38 || hello[0] != 1 {
		return scid, nil, errNotInitial
	}
	return scid, hello[6:38], nil
}

// cryptoAtZero returns the data of the CRYPTO frame at offset 0. Initial
// packets carry only PADDING, PING, ACK, CRYPTO and CONNECTION_CLOSE.
func cryptoAtZero(b []byte) ([]byte, error) {
	for len(b) > 0 {
		t := b[0]
		switch {
		case t == 0x00 || t == 0x01:
			b = b[1:]
		case t == 0x02 || t == 0x03:
			b = b[1:]
			var n int
			var v uint64
			// largest, delay, range count, first range
			fields := make([]uint64, 4)
			for i := range fields {
				if v, n = quicVarint(b); n == 0 {
					return nil, errNotInitial
				}
				fields[i] = v
				b = b[n:]
			}
			for i := uint64(0); i < fields[2]*2; i++ {
				if _, n = quicVarint(b); n == 0 {
					return nil, errNotInitial
				}
				b = b[n:]
			}
			if t == 0x03 {
				for i := 0; i < 3; i++ {
					if _, n = quicVarint(b); n == 0 {
						return nil, errNotInitial
					}
					b = b[n:]
				}
			}
		case t == 0x06:
			off, n := quicVarint(b[1:])
			if n == 0 {
				return nil, errNotInitial
			}
			b = b[1+n:]
			ln, n := quicVarint(b)
			if n == 0 || uint64(len(b)-n) < ln {
				return nil, errNotInitial
			}
			data := b[n : n+int(ln)]
			if off == 0 {
				return data, nil
			}
			b = b[n+int(ln):]
		default:
			return nil, errNoHello
		}
	}
	return nil, errNoHello
}

// quicVarint is a RFC 9000 (section 16) variable-length integer and its size (0 if short).
func quicVarint(b []byte) (uint64, int) {
	if len(b) == 0 {
		return 0, 0
	}
	n := 1 << (b[0] >> 6)
	if len(b) < n {
		return 0, 0
	}
	v := uint64(b[0] & 0x3f)
	for i := 1; i < n; i++ {
		v = v<<8 | uint64(b[i])
	}
	return v, n
}

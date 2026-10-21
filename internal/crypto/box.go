// Package crypto seals mux with ChaCha20-Poly1305, proves membership with
// clockless HMAC (0x02) bound to a client X25519 pub, and derives the
// session key from the DH secret (HKDF info "loft-v1").
//
// The node still accepts the 1.0.16 proof and 0x03 tickets (Legacy).
// Drop that path in 1.1. Clients of this tree always send 0x02+eph.
package crypto

import (
	"crypto/ecdh"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/hkdf"
)

// entropy is crypto/rand; tests swap it to force MakeTicket / nonce-prefix failure.
var entropy io.Reader = rand.Reader

// AEAD and auth sizes. Seal is nonce[12] || ChaCha20-Poly1305 (RFC 8439).
// Handshake Status is eph_n[32] || Seal (UnwrapHello / WrapHello).
const (
	NonceSize  = 12
	KeySize    = 32
	AuthSize   = 16
	EphSize    = 32
	ProofLen   = 1 + 16 + EphSize + AuthSize // tag + nonce + eph_c + mac
	proofLenV1 = 1 + 16 + AuthSize           // 1.0.16: tag + nonce + mac
)

// Auth blob tags: 0x02 clockless proof. 0x03 is a 1.0.16 resume ticket;
// FinishAuth still accepts it as Legacy until 1.1.
const (
	tagAuth   byte = 0x02 // nonce + eph_c + HMAC(key, "lft-a2"||nonce||eph_c)
	tagTicket byte = 0x03 // 1.0.16 ticket; HKDF(lt_, ticket nonce)
)

// ErrAuth is a failed proof or AEAD open.
var ErrAuth = fmt.Errorf("crypto: auth failed")

// DeriveKey is HKDF-SHA256(ikm, salt=nonce, info="loft-v1", L=32).
// Session keys pass ikm = X25519 shared secret.
func DeriveKey(ikm, nonce []byte) ([]byte, error) {
	r := hkdf.New(sha256.New, ikm, nonce, []byte("loft-v1"))
	key := make([]byte, KeySize)
	if _, err := io.ReadFull(r, key); err != nil {
		return nil, err
	}
	return key, nil
}

// SessionKey is HKDF(X25519(sk, peer), nonce).
func SessionKey(sk *ecdh.PrivateKey, peerPub, nonce []byte) ([]byte, error) {
	if sk == nil || len(peerPub) != EphSize {
		return nil, ErrAuth
	}
	pk, err := ecdh.X25519().NewPublicKey(peerPub)
	if err != nil {
		return nil, err
	}
	ss, err := sk.ECDH(pk)
	if err != nil {
		return nil, err
	}
	return DeriveKey(ss, nonce)
}

// WrapHello prefixes the node's X25519 pub onto a sealed Status.
func WrapHello(ephN, sealed []byte) []byte {
	out := make([]byte, 0, EphSize+len(sealed))
	out = append(out, ephN...)
	out = append(out, sealed...)
	return out
}

// UnwrapHello splits eph_n[32] || Seal from the first node message.
func UnwrapHello(raw []byte) (ephN, sealed []byte, err error) {
	if len(raw) < EphSize+NonceSize+16 {
		return nil, nil, ErrAuth
	}
	return raw[:EphSize], raw[EphSize:], nil
}

// OpenHello derives the session key from the Status prefix and opens Seal.
func OpenHello(sk *ecdh.PrivateKey, nonce, raw, aad []byte) (key, plain []byte, err error) {
	ephN, sealed, err := UnwrapHello(raw)
	if err != nil {
		return nil, nil, err
	}
	key, err = SessionKey(sk, ephN, nonce)
	if err != nil {
		return nil, nil, err
	}
	plain, err = Open(key, sealed, aad)
	if err != nil {
		return nil, nil, err
	}
	return key, plain, nil
}

// sealState is one session key's nonce generator. Wire is still nonce[12]||ct
// (loft/1.0). Prefix is random per process+key. Counter continues across
// GET Park on the same key (skip-handshake hold). A new 0x02 is a new key.
type sealState struct {
	prefix uint32
	n      atomic.Uint64
}

var sealers sync.Map // string(key) -> *sealState

// nextNonce is 4-byte prefix || 8-byte big-endian counter, unique per key.
func nextNonce(key []byte) ([]byte, error) {
	k := string(key)
	v, ok := sealers.Load(k)
	if !ok {
		var pb [4]byte
		if _, err := io.ReadFull(entropy, pb[:]); err != nil {
			return nil, err
		}
		st := &sealState{prefix: binary.BigEndian.Uint32(pb[:])}
		actual, _ := sealers.LoadOrStore(k, st)
		v = actual
	}
	st := v.(*sealState)
	n := st.n.Add(1)
	nonce := make([]byte, NonceSize)
	binary.BigEndian.PutUint32(nonce[:4], st.prefix)
	binary.BigEndian.PutUint64(nonce[4:], n)
	return nonce, nil
}

// Seal is nonce[12] || ChaCha20-Poly1305(key, nonce, plaintext, aad).
// Nonce is unique per key (prefix+counter), not a fresh 96-bit random.
func Seal(key, plaintext, aad []byte) ([]byte, error) {
	aead, err := chacha20poly1305.New(key)
	if err != nil {
		return nil, err
	}
	nonce, err := nextNonce(key)
	if err != nil {
		return nil, err
	}
	ct := aead.Seal(nil, nonce, plaintext, aad)
	out := make([]byte, 0, NonceSize+len(ct))
	out = append(out, nonce...)
	out = append(out, ct...)
	return out, nil
}

// Open decrypts a Seal blob. GET uses AAD = session id; other fronts use empty AAD.
func Open(key, blob, aad []byte) ([]byte, error) {
	if len(blob) < NonceSize+aeadOverhead() {
		return nil, ErrAuth
	}
	aead, err := chacha20poly1305.New(key)
	if err != nil {
		return nil, err
	}
	nonce := blob[:NonceSize]
	return aead.Open(nil, nonce, blob[NonceSize:], aad)
}

// aeadOverhead is the Poly1305 tag size.
func aeadOverhead() int { return 16 }

// mac16 is HMAC-SHA256 truncated to 16 bytes.
func mac16(key, data []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write(data)
	return m.Sum(nil)[:AuthSize]
}

// authMAC is HMAC-SHA256(lt_, "lft-a2" || nonce || eph_c)[:16].
func authMAC(token, nonce, ephC []byte) []byte {
	m := hmac.New(sha256.New, token)
	m.Write([]byte("lft-a2"))
	m.Write(nonce)
	m.Write(ephC)
	return m.Sum(nil)[:AuthSize]
}

// MakeAuth is clockless: nonce || eph_c || HMAC(lt_, "lft-a2"||nonce||eph_c).
// The caller keeps sk until OpenHello on the node's Status prefix.
func MakeAuth(token []byte) (blob, nonce []byte, sk *ecdh.PrivateKey, err error) {
	nonce = make([]byte, 16)
	if _, err = io.ReadFull(entropy, nonce); err != nil {
		return nil, nil, nil, err
	}
	sk, err = ecdh.X25519().GenerateKey(entropy)
	if err != nil {
		return nil, nil, nil, err
	}
	ephC := sk.PublicKey().Bytes()
	blob = make([]byte, 0, ProofLen)
	blob = append(blob, tagAuth)
	blob = append(blob, nonce...)
	blob = append(blob, ephC...)
	blob = append(blob, authMAC(token, nonce, ephC)...)
	return blob, nonce, sk, nil
}

// Auth is a verified handshake: member token, Hello tail, session key.
type Auth struct {
	Token  []byte
	Nonce  []byte
	Rest   []byte
	EphN   []byte
	Key    []byte
	Replay bool // same nonce+eph_c inside TTL (GET Cookie retry)
	Legacy bool // 1.0.16 proof or 0x03 ticket; first message is Seal without eph_n
}

type hsRec struct {
	ephC []byte
	ephN []byte
	key  []byte
	t    time.Time
}

// Nonces remembers 0x02 handshakes so a GET Cookie retry is idempotent
// and a different eph_c on the same nonce is refused. Expiry is the node's clock.
type Nonces struct {
	mu  sync.Mutex
	hs  map[string]*hsRec
	ttl time.Duration
}

// NewNonces is a 10-minute handshake cache on the node's clock.
func NewNonces() *Nonces {
	return &Nonces{hs: map[string]*hsRec{}, ttl: 10 * time.Minute}
}

func (n *Nonces) sweepLocked(now time.Time) {
	if n == nil || len(n.hs) <= 50_000 {
		return
	}
	for id, rec := range n.hs {
		if now.Sub(rec.t) >= n.ttl {
			delete(n.hs, id)
		}
	}
}

func mintHandshake(ephC []byte, nonce []byte) (ephN, key []byte, err error) {
	sk, err := ecdh.X25519().GenerateKey(entropy)
	if err != nil {
		return nil, nil, err
	}
	key, err = SessionKey(sk, ephC, nonce)
	if err != nil {
		return nil, nil, err
	}
	return sk.PublicKey().Bytes(), key, nil
}

func (n *Nonces) establish(nonce, ephC []byte) (ephN, key []byte, replay bool, err error) {
	if n == nil {
		ephN, key, err = mintHandshake(ephC, nonce)
		return ephN, key, false, err
	}
	k := string(nonce)
	now := time.Now()
	n.mu.Lock()
	defer n.mu.Unlock()
	n.sweepLocked(now)
	if rec, ok := n.hs[k]; ok && now.Sub(rec.t) < n.ttl {
		if !hmac.Equal(rec.ephC, ephC) {
			return nil, nil, false, ErrAuth
		}
		return rec.ephN, rec.key, true, nil
	}
	ephN, key, err = mintHandshake(ephC, nonce)
	if err != nil {
		return nil, nil, false, err
	}
	n.hs[k] = &hsRec{
		ephC: append([]byte(nil), ephC...),
		ephN: append([]byte(nil), ephN...),
		key:  append([]byte(nil), key...),
		t:    now,
	}
	return ephN, key, false, nil
}

// establishV1 caches a 1.0.16 HKDF(lt_, nonce) key so a GET Cookie retry is
// idempotent. A DH handshake on the same nonce is refused.
func (n *Nonces) establishV1(nonce, key []byte) (out []byte, replay bool, err error) {
	if n == nil {
		return key, false, nil
	}
	k := string(nonce)
	now := time.Now()
	n.mu.Lock()
	defer n.mu.Unlock()
	n.sweepLocked(now)
	if rec, ok := n.hs[k]; ok && now.Sub(rec.t) < n.ttl {
		if len(rec.ephC) != 0 {
			return nil, false, ErrAuth
		}
		return rec.key, true, nil
	}
	n.hs[k] = &hsRec{key: append([]byte(nil), key...), t: now}
	return key, false, nil
}

// FinishAuth verifies 0x02 (DH first, then 1.0.16), or a 0x03 ticket.
// A wrong key does not burn a valid blob. Drop Legacy in 1.1.
func FinishAuth(tokens [][]byte, blob []byte, seen *Nonces) (Auth, error) {
	var z Auth
	if len(blob) < 1 {
		return z, ErrAuth
	}
	switch blob[0] {
	case tagAuth:
		return finishAuth02(tokens, blob, seen)
	case tagTicket:
		return finishAuth03(tokens, blob)
	default:
		return z, ErrAuth
	}
}

func finishAuth02(tokens [][]byte, blob []byte, seen *Nonces) (Auth, error) {
	var z Auth
	if len(blob) >= ProofLen {
		nonce := blob[1:17]
		ephC := blob[17 : 17+EphSize]
		sum := blob[17+EphSize : ProofLen]
		for _, t := range tokens {
			if !hmac.Equal(sum, authMAC(t, nonce, ephC)) {
				continue
			}
			ephN, key, replay, err := seen.establish(nonce, ephC)
			if err != nil {
				return z, err
			}
			return Auth{
				Token:  t,
				Nonce:  append([]byte(nil), nonce...),
				Rest:   blob[ProofLen:],
				EphN:   ephN,
				Key:    key,
				Replay: replay,
			}, nil
		}
	}
	if len(blob) >= proofLenV1 {
		nonce := blob[1:17]
		sum := blob[17:proofLenV1]
		for _, t := range tokens {
			if !hmac.Equal(sum, mac16(t, nonce)) {
				continue
			}
			key, err := DeriveKey(t, nonce)
			if err != nil {
				return z, err
			}
			key, replay, err := seen.establishV1(nonce, key)
			if err != nil {
				return z, err
			}
			return Auth{
				Token:  t,
				Nonce:  append([]byte(nil), nonce...),
				Rest:   blob[proofLenV1:],
				Key:    key,
				Replay: replay,
				Legacy: true,
			}, nil
		}
	}
	return z, ErrAuth
}

func finishAuth03(tokens [][]byte, blob []byte) (Auth, error) {
	var z Auth
	for _, t := range tokens {
		_, nonce, rest, err := OpenTicket(t, blob)
		if err != nil {
			continue
		}
		key, err := DeriveKey(t, nonce)
		if err != nil {
			return z, err
		}
		return Auth{
			Token:  t,
			Nonce:  append([]byte(nil), nonce...),
			Rest:   rest,
			Key:    key,
			Legacy: true,
		}, nil
	}
	return z, ErrAuth
}

// CheckAuth verifies a 0x02 proof under one member key.
func CheckAuth(token, blob []byte, seen *Nonces) (nonce, rest []byte, err error) {
	a, err := FinishAuth([][]byte{token}, blob, seen)
	if err != nil {
		return nil, nil, err
	}
	return a.Nonce, a.Rest, nil
}

// CheckAuthAny tries each member key. Replay of the same nonce+eph_c is success.
func CheckAuthAny(tokens [][]byte, blob []byte, seen *Nonces) (token, nonce, rest []byte, err error) {
	a, err := FinishAuth(tokens, blob, seen)
	if err != nil {
		return nil, nil, nil, err
	}
	return a.Token, a.Nonce, a.Rest, nil
}

// MakeTicket mints a leftover Status lease blob (not an auth method).
// rand failure returns an error: a zero nonce must not be issued.
func MakeTicket(token []byte, instance string, ttl time.Duration) ([]byte, error) {
	exp := time.Now().Add(ttl).Unix()
	var eb [8]byte
	binary.BigEndian.PutUint64(eb[:], uint64(exp))
	nonce := make([]byte, 16)
	if _, err := io.ReadFull(entropy, nonce); err != nil {
		return nil, err
	}
	mac := mac16(token, ticketMAC(eb[:], nonce, instance))
	out := []byte{tagTicket}
	out = append(out, eb[:]...)
	out = append(out, nonce...)
	out = append(out, []byte{byte(len(instance))}...)
	out = append(out, instance...)
	out = append(out, mac...)
	return out, nil
}

// OpenTicket checks tag 0x03, issuer-clock expiry, and HMAC. rest is trailing Hello bytes.
func OpenTicket(token, blob []byte) (instance string, nonce, rest []byte, err error) {
	if len(blob) < 1+8+16+1+AuthSize || blob[0] != tagTicket {
		var b0 byte
		if len(blob) > 0 {
			b0 = blob[0]
		}
		return "", nil, nil, fmt.Errorf("crypto: ticket form len=%d tag=%d", len(blob), b0)
	}
	body := blob[1:]
	exp := int64(binary.BigEndian.Uint64(body[:8]))
	if time.Now().Unix() > exp {
		return "", nil, nil, fmt.Errorf("crypto: ticket expired")
	}
	nonce = body[8:24]
	n := int(body[24])
	if 25+n+AuthSize > len(body) {
		return "", nil, nil, fmt.Errorf("crypto: ticket n=%d body=%d", n, len(body))
	}
	instance = string(body[25 : 25+n])
	mac := body[25+n : 25+n+AuthSize]
	want := mac16(token, ticketMAC(body[:8], nonce, instance))
	if !hmac.Equal(mac, want) {
		return "", nil, nil, fmt.Errorf("crypto: ticket mac")
	}
	return instance, nonce, body[25+n+AuthSize:], nil
}

// ticketMAC is exp||nonce||instance, the HMAC input for tag 0x03.
func ticketMAC(exp, nonce []byte, instance string) []byte {
	b := make([]byte, 0, 8+16+len(instance))
	b = append(b, exp...)
	b = append(b, nonce...)
	b = append(b, instance...)
	return b
}

// RandomID is 12 random bytes as 24 hex characters (client instance_id).
func RandomID() string {
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	const hex = "0123456789abcdef"
	out := make([]byte, 24)
	for i, v := range b {
		out[i*2] = hex[v>>4]
		out[i*2+1] = hex[v&0x0f]
	}
	return string(out)
}

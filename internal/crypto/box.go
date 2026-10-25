// Package crypto seals mux, proves membership with clockless HMAC (0x02)
// bound to a client X25519 pub, and derives the session key from the DH
// secret (HKDF info "loft-v1"). Data default is ChaCha20-Poly1305.
// BindInner may select clear or AES-256-GCM after the handshake Status.
package crypto

import (
	"crypto/cipher"
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

	"github.com/a3app/loft/internal/wire"
	"golang.org/x/crypto/hkdf"
)

// entropy is crypto/rand; tests swap it to force nonce-prefix failure.
var entropy io.Reader = rand.Reader

// AEAD and auth sizes. Seal is nonce[12] || ChaCha20-Poly1305 (RFC 8439).
// Handshake Status is eph_n[32] || Seal (UnwrapHello / WrapHello).
const (
	NonceSize = 12
	KeySize   = 32
	AuthSize  = 16
	EphSize   = 32
	ProofLen  = 1 + 16 + EphSize + AuthSize // tag + nonce + eph_c + mac
)

const tagAuth byte = 0x02 // nonce + eph_c + HMAC(key, "lft-a2"||nonce||eph_c)

// LooksLikeAuth is the GET handshake Cookie: tag 0x02 of ProofLen.
// AEAD uplink is nonce[12]||ct and is not this shape. A 1/256 first-byte
// collision still fails FinishAuth; the GET front then treats it as mux.
func LooksLikeAuth(blob []byte) bool {
	return len(blob) >= ProofLen && blob[0] == tagAuth
}

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

// Seal is nonce[12] || AEAD(key, nonce, plaintext, aad).
// The default AEAD is ChaCha20-Poly1305. BindInner selects clear or AES-256-GCM
// for data frames; call it only after the handshake Status has been sealed.
// Nonce is unique per key (prefix+counter), not a fresh 96-bit random.
// LOFT_STAND_INNER=clear returns the plaintext for every key.
func Seal(key, plaintext, aad []byte) ([]byte, error) {
	switch dataInner(key) {
	case wire.InnerClear:
		return append([]byte(nil), plaintext...), nil
	case wire.InnerAES:
		return sealWith(key, plaintext, aad, newAESGCM)
	default:
		return sealWith(key, plaintext, aad, standAEAD)
	}
}

func sealWith(key, plaintext, aad []byte, newAEAD func([]byte) (cipher.AEAD, error)) ([]byte, error) {
	aead, err := newAEAD(key)
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
// The AEAD follows BindInner. LOFT_STAND_INNER=clear returns the blob.
func Open(key, blob, aad []byte) ([]byte, error) {
	switch dataInner(key) {
	case wire.InnerClear:
		return append([]byte(nil), blob...), nil
	case wire.InnerAES:
		return openWith(key, blob, aad, newAESGCM)
	default:
		return openWith(key, blob, aad, standAEAD)
	}
}

func openWith(key, blob, aad []byte, newAEAD func([]byte) (cipher.AEAD, error)) ([]byte, error) {
	if len(blob) < NonceSize+aeadOverhead() {
		return nil, ErrAuth
	}
	aead, err := newAEAD(key)
	if err != nil {
		return nil, ErrAuth
	}
	nonce := blob[:NonceSize]
	return aead.Open(nil, nonce, blob[NonceSize:], aad)
}

// aeadOverhead is the Poly1305 tag size.
func aeadOverhead() int { return 16 }

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

// FinishAuth verifies 0x02 (nonce || eph_c || HMAC). A wrong key does not
// burn a valid blob.
func FinishAuth(tokens [][]byte, blob []byte, seen *Nonces) (Auth, error) {
	var z Auth
	if len(blob) < ProofLen || blob[0] != tagAuth {
		return z, ErrAuth
	}
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

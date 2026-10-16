// Package crypto seals mux with ChaCha20-Poly1305, proves membership with
// clockless HMAC (0x02) or a resume ticket (0x03), and derives the session
// key via HKDF-SHA256 (info "loft-v1").
package crypto

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/binary"
	"fmt"
	"io"
	"sync"
	"time"

	"golang.org/x/crypto/chacha20poly1305"
	"golang.org/x/crypto/hkdf"
)

// AEAD and auth sizes. Seal is nonce[12] || ChaCha20-Poly1305 (RFC 8439).
const (
	NonceSize = 12
	KeySize   = 32
	AuthSize  = 16
	ProofLen  = 1 + 16 + AuthSize // tag + nonce + mac
)

// Auth blob tags: 0x02 clockless proof, 0x03 resume ticket.
const (
	tagAuth   byte = 0x02 // clockless: nonce + HMAC(key, nonce)
	tagResume byte = 0x03 // ticket issued by a node/replica
)

// ErrAuth is a failed proof, ticket, or AEAD open.
var ErrAuth = fmt.Errorf("crypto: auth failed")

// DeriveKey is HKDF-SHA256(ikm=member_key, salt=nonce, info="loft-v1", L=32).
func DeriveKey(token, nonce []byte) ([]byte, error) {
	r := hkdf.New(sha256.New, token, nonce, []byte("loft-v1"))
	key := make([]byte, KeySize)
	if _, err := io.ReadFull(r, key); err != nil {
		return nil, err
	}
	return key, nil
}

// Seal is nonce[12] || ChaCha20-Poly1305(key, nonce, plaintext, aad). Nonce is random per seal.
func Seal(key, plaintext, aad []byte) ([]byte, error) {
	aead, err := chacha20poly1305.New(key)
	if err != nil {
		return nil, err
	}
	nonce := make([]byte, NonceSize)
	if _, err := rand.Read(nonce); err != nil {
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

// MakeAuth is clockless: nonce || HMAC(key, nonce). Replay is the node's nonce cache (server clock).
func MakeAuth(token []byte) ([]byte, []byte, error) {
	nonce := make([]byte, 16)
	if _, err := rand.Read(nonce); err != nil {
		return nil, nil, err
	}
	blob := make([]byte, 0, ProofLen)
	blob = append(blob, tagAuth)
	blob = append(blob, nonce...)
	blob = append(blob, mac16(token, nonce)...)
	return blob, nonce, nil
}

// Nonces remembers auth nonces so a blob cannot be replayed. Expiry uses the
// node's clock only. Replicas share resume tickets, not this cache.
type Nonces struct {
	mu   sync.Mutex
	seen map[string]time.Time
	ttl  time.Duration
}

// NewNonces is a 10-minute nonce cache on the node's clock.
func NewNonces() *Nonces {
	return &Nonces{seen: map[string]time.Time{}, ttl: 10 * time.Minute}
}

// Take records nonce. False means it was seen inside TTL. Nil receiver always succeeds.
func (n *Nonces) Take(nonce []byte) bool {
	if n == nil {
		return true
	}
	k := string(nonce)
	now := time.Now()
	n.mu.Lock()
	defer n.mu.Unlock()
	if t, ok := n.seen[k]; ok && now.Sub(t) < n.ttl {
		return false
	}
	if len(n.seen) > 50_000 {
		for id, t := range n.seen {
			if now.Sub(t) >= n.ttl {
				delete(n.seen, id)
			}
		}
	}
	n.seen[k] = now
	return true
}

// CheckAuth verifies a 0x02 proof or 0x03 ticket under one member key.
func CheckAuth(token, blob []byte, seen *Nonces) (nonce, rest []byte, err error) {
	_, nonce, rest, err = CheckAuthAny([][]byte{token}, blob, seen)
	return nonce, rest, err
}

// CheckAuthAny tries each member key. The nonce cache is updated only after a
// MAC matches, so a wrong key does not burn a valid blob.
func CheckAuthAny(tokens [][]byte, blob []byte, seen *Nonces) (token, nonce, rest []byte, err error) {
	if len(blob) < 1 {
		return nil, nil, nil, ErrAuth
	}
	switch blob[0] {
	case tagAuth:
		if len(blob) < ProofLen {
			return nil, nil, nil, ErrAuth
		}
		nonce = blob[1:17]
		sum := blob[17:ProofLen]
		for _, tok := range tokens {
			if !hmac.Equal(sum, mac16(tok, nonce)) {
				continue
			}
			if seen != nil && !seen.Take(nonce) {
				return nil, nil, nil, ErrAuth
			}
			return tok, nonce, blob[ProofLen:], nil
		}
		return nil, nil, nil, ErrAuth
	case tagResume:
		var last error
		for _, tok := range tokens {
			_, n, r, e := OpenTicket(tok, blob)
			if e == nil {
				return tok, n, r, nil
			}
			last = e
		}
		if last != nil {
			return nil, nil, nil, last
		}
		return nil, nil, nil, ErrAuth
	default:
		return nil, nil, nil, fmt.Errorf("crypto: auth tag %d", blob[0])
	}
}

// Ticket lets any replica with the member key resume without the nonce cache.
// exp is on the issuer's clock; the client only echoes the bytes.
func MakeTicket(token []byte, instance string, ttl time.Duration) []byte {
	exp := time.Now().Add(ttl).Unix()
	var eb [8]byte
	binary.BigEndian.PutUint64(eb[:], uint64(exp))
	nonce := make([]byte, 16)
	_, _ = rand.Read(nonce)
	mac := mac16(token, ticketMAC(eb[:], nonce, instance))
	out := []byte{tagResume}
	out = append(out, eb[:]...)
	out = append(out, nonce...)
	out = append(out, []byte{byte(len(instance))}...)
	out = append(out, instance...)
	out = append(out, mac...)
	return out
}

// OpenTicket checks tag 0x03, issuer-clock expiry, and HMAC. rest is trailing Hello bytes.
func OpenTicket(token, blob []byte) (instance string, nonce, rest []byte, err error) {
	if len(blob) < 1+8+16+1+AuthSize || blob[0] != tagResume {
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

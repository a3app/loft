// Package backup is the panel's backup format: a tar.gz of the server's
// parts with a manifest of SHA-256 sums, encrypted to a public key.
//
// The key comes from the recovery phrase: Argon2id of the phrase gives an
// X25519 private key, and only its public half is kept on the server. A
// copy is sealed to that public key with a key agreed for it alone, so the
// server can make copies every night but cannot open any of them — not even
// its own — without the phrase. A thief of the server and its copies gets
// nothing to read.
//
// The stream is AES-256-GCM in 1 MiB chunks. Each nonce is the chunk's
// number plus a flag on the last chunk, and the header is every chunk's
// additional data: chunks cannot be reordered, dropped, cut off at the end
// or moved under another header without the open failing.
package backup

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strings"
	"time"

	"golang.org/x/crypto/argon2"
	"golang.org/x/crypto/hkdf"
)

// Magic starts every copy.
const Magic = "LOFTBAK1"

const chunk = 1 << 20

// phraseAlphabet has no 0/O, 1/I/L: the phrase is written down by hand.
const phraseAlphabet = "ABCDEFGHJKMNPQRSTUVWXYZ23456789"

// NewPhrase is 6 groups of 5 characters: 30 × log2(31) ≈ 148 bits.
func NewPhrase() string {
	b := make([]byte, 30)
	rand.Read(b)
	var s strings.Builder
	for i, x := range b {
		if i > 0 && i%5 == 0 {
			s.WriteByte('-')
		}
		s.WriteByte(phraseAlphabet[int(x)%len(phraseAlphabet)])
	}
	return s.String()
}

// NormPhrase drops spaces and dashes and uppercases, so the phrase may be
// typed as written or as remembered.
func NormPhrase(p string) string {
	var s strings.Builder
	for _, r := range strings.ToUpper(p) {
		if r == '-' || r == ' ' || r == '\t' || r == '\n' || r == '\r' {
			continue
		}
		s.WriteRune(r)
	}
	return s.String()
}

// Key is what the server keeps: the public half and the salt the phrase is
// stretched with.
type Key struct {
	Public string    `json:"public"` // X25519, base64
	Salt   string    `json:"salt"`   // base64
	Set    time.Time `json:"set"`
}

// KeyFromPhrase stretches the phrase (Argon2id, 64 MiB) into the private key.
func KeyFromPhrase(phrase string, salt []byte) (*ecdh.PrivateKey, error) {
	p := NormPhrase(phrase)
	if len(p) < 20 {
		return nil, errors.New("the recovery phrase is too short")
	}
	seed := argon2.IDKey([]byte(p), salt, 3, 64*1024, 2, 32)
	return ecdh.X25519().NewPrivateKey(seed)
}

// NewKey makes the server's key from a phrase.
func NewKey(phrase string) (Key, error) {
	salt := make([]byte, 16)
	rand.Read(salt)
	priv, err := KeyFromPhrase(phrase, salt)
	if err != nil {
		return Key{}, err
	}
	b64 := base64.StdEncoding
	return Key{Public: b64.EncodeToString(priv.PublicKey().Bytes()), Salt: b64.EncodeToString(salt), Set: time.Now().UTC()}, nil
}

// Header is the clear part of a copy: enough to tell what it is and to
// derive the key from the phrase.
type Header struct {
	V       int       `json:"v"`
	Host    string    `json:"host"`
	Created time.Time `json:"created"`
	Parts   []string  `json:"parts"`
	Salt    string    `json:"salt"`
	Public  string    `json:"public"`    // the recipient
	Eph     string    `json:"ephemeral"` // this copy's own key
}

func streamKey(shared, eph, pub []byte) []byte {
	k := make([]byte, 32)
	io.ReadFull(hkdf.New(sha256.New, shared, nil, append(append([]byte("loft backup v1"), eph...), pub...)), k)
	return k
}

type sealer struct {
	w      io.Writer
	aead   cipher.AEAD
	ad     []byte
	buf    []byte
	n      uint64
	closed bool
}

func nonce(n uint64, last bool) []byte {
	b := make([]byte, 12)
	binary.BigEndian.PutUint64(b, n)
	if last {
		b[11] = 1
	}
	return b
}

func (s *sealer) flush(last bool) error {
	ct := s.aead.Seal(nil, nonce(s.n, last), s.buf, s.ad)
	s.n++
	s.buf = s.buf[:0]
	var l [4]byte
	binary.BigEndian.PutUint32(l[:], uint32(len(ct)))
	if _, err := s.w.Write(l[:]); err != nil {
		return err
	}
	_, err := s.w.Write(ct)
	return err
}

func (s *sealer) Write(p []byte) (int, error) {
	n := 0
	for len(p) > 0 {
		k := min(chunk-len(s.buf), len(p))
		s.buf = append(s.buf, p[:k]...)
		p, n = p[k:], n+k
		if len(s.buf) == chunk {
			if err := s.flush(false); err != nil {
				return n, err
			}
		}
	}
	return n, nil
}

// Close writes the last chunk (possibly empty); without it a copy does not open.
func (s *sealer) Close() error {
	if s.closed {
		return nil
	}
	s.closed = true
	return s.flush(true)
}

func pubBytes(b64 string) (*ecdh.PublicKey, []byte, error) {
	b, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return nil, nil, err
	}
	pub, err := ecdh.X25519().NewPublicKey(b)
	return pub, b, err
}

// Seal writes the header and returns the writer for the plain stream; Close
// it to finish the copy.
func Seal(w io.Writer, k Key, h Header) (io.WriteCloser, error) {
	pub, pb, err := pubBytes(k.Public)
	if err != nil {
		return nil, fmt.Errorf("backup key: %w", err)
	}
	eph, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return nil, err
	}
	shared, err := eph.ECDH(pub)
	if err != nil {
		return nil, err
	}
	h.V, h.Salt, h.Public = 1, k.Salt, k.Public
	h.Eph = base64.StdEncoding.EncodeToString(eph.PublicKey().Bytes())
	hb, _ := json.Marshal(h)
	var l [4]byte
	binary.BigEndian.PutUint32(l[:], uint32(len(hb)))
	if _, err := w.Write(append(append([]byte(Magic), l[:]...), hb...)); err != nil {
		return nil, err
	}
	blk, _ := aes.NewCipher(streamKey(shared, eph.PublicKey().Bytes(), pb))
	aead, _ := cipher.NewGCM(blk)
	return &sealer{w: w, aead: aead, ad: hb, buf: make([]byte, 0, chunk)}, nil
}

// ReadHeader reads the clear header (no phrase needed).
func ReadHeader(r io.Reader) (Header, []byte, error) {
	var h Header
	m := make([]byte, len(Magic)+4)
	if _, err := io.ReadFull(r, m); err != nil || string(m[:len(Magic)]) != Magic {
		return h, nil, errors.New("not a loft backup")
	}
	n := binary.BigEndian.Uint32(m[len(Magic):])
	if n > 64<<10 {
		return h, nil, errors.New("not a loft backup: header too large")
	}
	hb := make([]byte, n)
	if _, err := io.ReadFull(r, hb); err != nil {
		return h, nil, errors.New("not a loft backup: header cut off")
	}
	if err := json.Unmarshal(hb, &h); err != nil || h.V != 1 {
		return h, nil, errors.New("not a loft backup, or a newer format")
	}
	return h, hb, nil
}

// ErrPhrase is a phrase that does not open the copy.
var ErrPhrase = errors.New("the recovery phrase does not match this copy")

type opener struct {
	r    io.Reader
	aead cipher.AEAD
	ad   []byte
	buf  *bytes.Reader
	n    uint64
	done bool
}

func (o *opener) Read(p []byte) (int, error) {
	for o.buf == nil || o.buf.Len() == 0 {
		if o.done {
			return 0, io.EOF
		}
		var l [4]byte
		if _, err := io.ReadFull(o.r, l[:]); err != nil {
			return 0, errors.New("the copy is cut off")
		}
		n := binary.BigEndian.Uint32(l[:])
		if n > chunk+64 {
			return 0, errors.New("the copy is damaged")
		}
		ct := make([]byte, n)
		if _, err := io.ReadFull(o.r, ct); err != nil {
			return 0, errors.New("the copy is cut off")
		}
		pt, err := o.aead.Open(nil, nonce(o.n, false), ct, o.ad)
		if err != nil {
			if pt, err = o.aead.Open(nil, nonce(o.n, true), ct, o.ad); err != nil {
				return 0, errors.New("the copy is damaged")
			}
			o.done = true
		}
		o.n++
		o.buf = bytes.NewReader(pt)
	}
	return o.buf.Read(p)
}

// Open reads the header, checks the phrase against it and returns the plain
// stream. A damaged or shortened copy fails while reading, never silently.
func Open(r io.Reader, phrase string) (Header, io.Reader, error) {
	h, hb, err := ReadHeader(r)
	if err != nil {
		return h, nil, err
	}
	salt, err := base64.StdEncoding.DecodeString(h.Salt)
	if err != nil {
		return h, nil, errors.New("not a loft backup: bad salt")
	}
	priv, err := KeyFromPhrase(phrase, salt)
	if err != nil {
		return h, nil, err
	}
	_, pb, err := pubBytes(h.Public)
	if err != nil {
		return h, nil, errors.New("not a loft backup: bad key")
	}
	if !bytes.Equal(priv.PublicKey().Bytes(), pb) {
		return h, nil, ErrPhrase
	}
	eph, eb, err := pubBytes(h.Eph)
	if err != nil {
		return h, nil, errors.New("not a loft backup: bad key")
	}
	shared, err := priv.ECDH(eph)
	if err != nil {
		return h, nil, err
	}
	blk, _ := aes.NewCipher(streamKey(shared, eb, pb))
	aead, _ := cipher.NewGCM(blk)
	return h, &opener{r: r, aead: aead, ad: hb}, nil
}

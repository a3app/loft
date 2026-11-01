package crypto

// AEAD seal/open, 0x02 + X25519 session key, multi-key FinishAuth.

import (
	"bytes"
	"crypto/ecdh"
	"testing"
)

// TestSealOpen round-trips ChaCha20-Poly1305 and rejects a wrong AAD.
func TestSealOpen(t *testing.T) {
	token := []byte("token-secret")
	nonce := []byte("1234567890abcdef")
	key, err := DeriveKey(token, nonce)
	if err != nil {
		t.Fatal(err)
	}
	ct, err := Seal(key, []byte("payload"), []byte("aad"))
	if err != nil {
		t.Fatal(err)
	}
	pt, err := Open(key, ct, []byte("aad"))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(pt, []byte("payload")) {
		t.Fatalf("got %q", pt)
	}
	if _, err := Open(key, ct, []byte("nope")); err == nil {
		t.Fatal("expected aad fail")
	}
}

func TestLooksLikeAuth(t *testing.T) {
	token := []byte("token-secret")
	blob, _, _, err := MakeAuth(token)
	if err != nil {
		t.Fatal(err)
	}
	if !LooksLikeAuth(blob) {
		t.Fatal("0x02")
	}
	if LooksLikeAuth(nil) || LooksLikeAuth([]byte{0x01, 1, 2, 3}) {
		t.Fatal("false positive")
	}
	if LooksLikeAuth([]byte{tagAuth}) {
		t.Fatal("short 0x02")
	}
	v1 := append([]byte{tagAuth}, bytes.Repeat([]byte{0x11}, 16+16)...)
	if LooksLikeAuth(v1) {
		t.Fatal("1.0.16 HMAC-only 0x02")
	}
	if LooksLikeAuth([]byte{0x03, 1, 2, 3}) {
		t.Fatal("0x03")
	}
}

// TestAuthClocklessAndReplay accepts a 0x02 proof and treats the same
// nonce+eph_c as an idempotent retry (GET Cookie), not a new session.
func TestAuthClocklessAndReplay(t *testing.T) {
	token := []byte("token-secret")
	seen := NewNonces()
	blob, nonce, sk, err := MakeAuth(token)
	if err != nil {
		t.Fatal(err)
	}
	a, err := FinishAuth([][]byte{token}, blob, seen)
	if err != nil || !bytes.Equal(a.Nonce, nonce) || len(a.Rest) != 0 || a.Replay {
		t.Fatalf("%+v %v", a, err)
	}
	b, err := FinishAuth([][]byte{token}, blob, seen)
	if err != nil || !b.Replay || !bytes.Equal(b.Key, a.Key) || !bytes.Equal(b.EphN, a.EphN) {
		t.Fatalf("retry %+v %v", b, err)
	}
	if _, _, err := CheckAuth([]byte("other"), blob, nil); err == nil {
		t.Fatal("bad key")
	}
	key, err := SessionKey(sk, a.EphN, nonce)
	if err != nil || !bytes.Equal(key, a.Key) {
		t.Fatalf("client key %v", err)
	}
}

// TestCheckAuthAnyPicksMatchingKey matches one token in a ring without burning others.
func TestCheckAuthAnyPicksMatchingKey(t *testing.T) {
	alice := []byte("alice-secret")
	bob := []byte("bob-secret")
	blob, nonce, _, err := MakeAuth(bob)
	if err != nil {
		t.Fatal(err)
	}
	seen := NewNonces()
	tok, got, rest, err := CheckAuthAny([][]byte{alice, bob}, blob, seen)
	if err != nil || !bytes.Equal(tok, bob) || !bytes.Equal(got, nonce) || len(rest) != 0 {
		t.Fatalf("tok=%q err=%v", tok, err)
	}
	if _, _, _, err := CheckAuthAny([][]byte{alice}, blob, nil); err == nil {
		t.Fatal("wrong ring")
	}
}

// TestAuthRejectsV1AndTicket: HMAC-only 0x02 and tag 0x03 are not auth.
func TestAuthRejectsV1AndTicket(t *testing.T) {
	token := []byte("token-secret")
	nonce := bytes.Repeat([]byte{0x11}, 16)
	v1 := append([]byte{tagAuth}, nonce...)
	v1 = append(v1, bytes.Repeat([]byte{0x22}, AuthSize)...)
	if _, err := FinishAuth([][]byte{token}, v1, nil); err == nil {
		t.Fatal("accepted 1.0.16 HMAC-only 0x02")
	}
	if _, err := FinishAuth([][]byte{token}, []byte{0x03, 1, 2, 3}, nil); err == nil {
		t.Fatal("accepted 0x03")
	}
}

func TestAuthDH(t *testing.T) {
	token := []byte("token-secret")
	blob, _, sk, err := MakeAuth(token)
	if err != nil {
		t.Fatal(err)
	}
	a, err := FinishAuth([][]byte{token}, blob, nil)
	if err != nil || len(a.EphN) != EphSize {
		t.Fatalf("dh %+v %v", a, err)
	}
	key, err := SessionKey(sk, a.EphN, a.Nonce)
	if err != nil || !bytes.Equal(key, a.Key) {
		t.Fatalf("dh %v", err)
	}
}

// TestStolenAuthBlobNeedsEphSecret: Cookie/logs have eph_c, not sk.
func TestStolenAuthBlobNeedsEphSecret(t *testing.T) {
	token := []byte("token-secret")
	blob, nonce, sk, err := MakeAuth(token)
	if err != nil {
		t.Fatal(err)
	}
	a, err := FinishAuth([][]byte{token}, blob, nil)
	if err != nil {
		t.Fatal(err)
	}
	eve, err := ecdh.X25519().GenerateKey(entropy)
	if err != nil {
		t.Fatal(err)
	}
	bad, err := SessionKey(eve, a.EphN, nonce)
	if err != nil {
		t.Fatal(err)
	}
	good, err := SessionKey(sk, a.EphN, nonce)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(bad, good) {
		t.Fatal("eve got session key")
	}
}

// TestSealIntoReuse writes nonce||ct into dst cap and matches Seal/Open.
func TestSealIntoReuse(t *testing.T) {
	key := bytes.Repeat([]byte{0x11}, 32)
	pt := []byte("payload")
	dst := make([]byte, 0, 4+NonceSize+len(pt)+AuthSize)
	dst = dst[:4]
	got, err := SealInto(dst, key, pt, []byte("aad"))
	if err != nil {
		t.Fatal(err)
	}
	if cap(got) != cap(dst) {
		t.Fatalf("realloc cap=%d", cap(got))
	}
	if len(got) != 4+NonceSize+len(pt)+AuthSize {
		t.Fatalf("len %d", len(got))
	}
	plain, err := Open(key, got[4:], []byte("aad"))
	if err != nil || !bytes.Equal(plain, pt) {
		t.Fatalf("open %q %v", plain, err)
	}
}

func TestSealIntoMatchesSeal(t *testing.T) {
	key := bytes.Repeat([]byte{0x22}, 32)
	pt := []byte("same")
	a, err := Seal(key, pt, nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := SealInto(nil, key, pt, nil)
	if err != nil {
		t.Fatal(err)
	}
	pa, err := Open(key, a, nil)
	if err != nil {
		t.Fatal(err)
	}
	pb, err := Open(key, b, nil)
	if err != nil || !bytes.Equal(pa, pb) || !bytes.Equal(pa, pt) {
		t.Fatalf("a=%q b=%q", pa, pb)
	}
}

// TestSealNonceUnique keeps a 12-byte nonce that Open accepts, distinct per seal.
func TestSealNonceUnique(t *testing.T) {
	key := bytes.Repeat([]byte{0x11}, 32)
	a, err := Seal(key, []byte("one"), nil)
	if err != nil {
		t.Fatal(err)
	}
	b, err := Seal(key, []byte("two"), nil)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(a[:NonceSize], b[:NonceSize]) {
		t.Fatal("same nonce twice")
	}
	pt, err := Open(key, a, nil)
	if err != nil || string(pt) != "one" {
		t.Fatalf("open a: %q %v", pt, err)
	}
	pt, err = Open(key, b, nil)
	if err != nil || string(pt) != "two" {
		t.Fatalf("open b: %q %v", pt, err)
	}
}

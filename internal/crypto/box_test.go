package crypto

// AEAD seal/open, 0x02 + X25519 session key, multi-key FinishAuth.

import (
	"bytes"
	"crypto/ecdh"
	"io"
	"testing"
	"time"
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

func makeAuthV1(token []byte) (blob, nonce []byte) {
	nonce = bytes.Repeat([]byte{0x11}, 16)
	blob = append([]byte{tagAuth}, nonce...)
	blob = append(blob, mac16(token, nonce)...)
	return blob, nonce
}

// TestAuthV1AndTicket accepts the 1.0.16 proof and a 0x03 ticket as Legacy.
func TestAuthV1AndTicket(t *testing.T) {
	token := []byte("token-secret")
	blob, nonce := makeAuthV1(token)
	hello := []byte("hello-v1")
	a, err := FinishAuth([][]byte{token}, append(append([]byte{}, blob...), hello...), nil)
	if err != nil || !a.Legacy || len(a.EphN) != 0 || !bytes.Equal(a.Rest, hello) {
		t.Fatalf("v1 %+v %v", a, err)
	}
	want, err := DeriveKey(token, nonce)
	if err != nil || !bytes.Equal(a.Key, want) {
		t.Fatalf("v1 key %v", err)
	}
	seen := NewNonces()
	first, err := FinishAuth([][]byte{token}, blob, seen)
	if err != nil || first.Replay {
		t.Fatalf("v1 first %+v %v", first, err)
	}
	retry, err := FinishAuth([][]byte{token}, blob, seen)
	if err != nil || !retry.Replay || !bytes.Equal(retry.Key, first.Key) {
		t.Fatalf("v1 retry %+v %v", retry, err)
	}

	tk, err := MakeTicket(token, "phone", time.Hour)
	if err != nil {
		t.Fatal(err)
	}
	b, err := FinishAuth([][]byte{token}, append(append([]byte{}, tk...), hello...), nil)
	if err != nil || !b.Legacy || !bytes.Equal(b.Rest, hello) {
		t.Fatalf("ticket %+v %v", b, err)
	}
	_, tn, _, err := OpenTicket(token, tk)
	if err != nil {
		t.Fatal(err)
	}
	tkey, err := DeriveKey(token, tn)
	if err != nil || !bytes.Equal(b.Key, tkey) {
		t.Fatalf("ticket key %v", err)
	}
}

// TestAuthV2PreferredOverV1: a DH proof is not parsed as HMAC(lt_, nonce).
func TestAuthV2PreferredOverV1(t *testing.T) {
	token := []byte("token-secret")
	blob, _, sk, err := MakeAuth(token)
	if err != nil {
		t.Fatal(err)
	}
	a, err := FinishAuth([][]byte{token}, blob, nil)
	if err != nil || a.Legacy || len(a.EphN) != EphSize {
		t.Fatalf("v2 %+v %v", a, err)
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

type failReader struct{}

func (failReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

// TestMakeTicketRandFail does not mint a ticket with a zero nonce.
func TestMakeTicketRandFail(t *testing.T) {
	old := entropy
	entropy = failReader{}
	t.Cleanup(func() { entropy = old })
	tk, err := MakeTicket([]byte("token-secret"), "phone", time.Hour)
	if err == nil || tk != nil {
		t.Fatalf("want err, got %v %x", err, tk)
	}
}

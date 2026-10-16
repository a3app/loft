package crypto

// AEAD seal/open, clockless auth replay, multi-key CheckAuth, and tickets.

import (
	"bytes"
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

// TestAuthClocklessAndReplay accepts a 0x02 proof once and rejects replay.
func TestAuthClocklessAndReplay(t *testing.T) {
	token := []byte("token-secret")
	seen := NewNonces()
	blob, nonce, err := MakeAuth(token)
	if err != nil {
		t.Fatal(err)
	}
	got, rest, err := CheckAuth(token, blob, seen)
	if err != nil || !bytes.Equal(got, nonce) || len(rest) != 0 {
		t.Fatalf("%v %x", err, got)
	}
	if _, _, err := CheckAuth(token, blob, seen); err == nil {
		t.Fatal("replay")
	}
	if _, _, err := CheckAuth([]byte("other"), blob, nil); err == nil {
		t.Fatal("bad key")
	}
}

// TestCheckAuthAnyPicksMatchingKey matches one token in a ring without burning others.
func TestCheckAuthAnyPicksMatchingKey(t *testing.T) {
	alice := []byte("alice-secret")
	bob := []byte("bob-secret")
	blob, nonce, err := MakeAuth(bob)
	if err != nil {
		t.Fatal(err)
	}
	seen := NewNonces()
	tok, got, rest, err := CheckAuthAny([][]byte{alice, bob}, blob, seen)
	if err != nil || !bytes.Equal(tok, bob) || !bytes.Equal(got, nonce) || len(rest) != 0 {
		t.Fatalf("tok=%q err=%v", tok, err)
	}
	if _, _, _, err := CheckAuthAny([][]byte{alice, bob}, blob, seen); err == nil {
		t.Fatal("replay")
	}
	if _, _, _, err := CheckAuthAny([][]byte{alice}, blob, nil); err == nil {
		t.Fatal("wrong ring")
	}
}

// TestTicketReplica opens a 0x03 ticket and accepts it through CheckAuth.
func TestTicketReplica(t *testing.T) {
	token := []byte("token-secret")
	tk := MakeTicket(token, "phone", time.Hour)
	inst, nonce, rest, err := OpenTicket(token, tk)
	if err != nil || inst != "phone" || len(nonce) != 16 || len(rest) != 0 {
		t.Fatalf("%s %v", inst, err)
	}
	got, _, err := CheckAuth(token, tk, nil)
	if err != nil || !bytes.Equal(got, nonce) {
		t.Fatalf("check tag=%d len=%d err=%v got=%x want=%x", tk[0], len(tk), err, got, nonce)
	}
}

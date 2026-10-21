package sstp

import (
	"bytes"
	"encoding/hex"
	"testing"
)

func TestNTPasswordHash(t *testing.T) {
	// RFC 2759 8.1
	want, _ := hex.DecodeString("44EBBA8D5312B8D611474411F56989AE")
	got := ntPasswordHash("clientPass")
	if !bytes.Equal(got, want) {
		t.Fatalf("got %x want %x", got, want)
	}
}

func TestChallengeHash(t *testing.T) {
	// RFC 2759 8.2
	peer, _ := hex.DecodeString("21402324255E262A28295F2B3A337C7E")
	auth, _ := hex.DecodeString("5B5D7C7D7B3F2F3E3C2C602132262628")
	want, _ := hex.DecodeString("D02E4386BCE91226")
	got := challengeHash(peer, auth, "User")
	if !bytes.Equal(got, want) {
		t.Fatalf("got %x want %x", got, want)
	}
}

func TestNTResponse(t *testing.T) {
	// RFC 2759 8.3
	auth, _ := hex.DecodeString("5B5D7C7D7B3F2F3E3C2C602132262628")
	peer, _ := hex.DecodeString("21402324255E262A28295F2B3A337C7E")
	got := generateNTResponse(auth, peer, "User", "clientPass")
	// RFC 2759 §9.2 (the 42 33 14 A3 dump on some mirrors is a typo)
	want, _ := hex.DecodeString("82309ECD8D708B5EA08FAA3981CD83544233114A3D85D6DF")
	if !bytes.Equal(got, want) {
		t.Fatalf("got %x want %x", got, want)
	}
}

func TestRC4RFC6229(t *testing.T) {
	// Classic RC4: key "Key", plaintext "Plaintext"
	c := newRC4([]byte("Key"))
	src := []byte("Plaintext")
	dst := make([]byte, len(src))
	c.xor(dst, src)
	want, _ := hex.DecodeString("BBF316E8D940AF0AD3")
	if !bytes.Equal(dst, want) {
		t.Fatalf("rc4 got %x want %x", dst, want)
	}
}

func TestAuthenticatorResponse(t *testing.T) {
	auth, _ := hex.DecodeString("5B5D7C7D7B3F2F3E3C2C602132262628")
	peer, _ := hex.DecodeString("21402324255E262A28295F2B3A337C7E")
	nt := generateNTResponse(auth, peer, "User", "clientPass")
	got := authenticatorResponse("clientPass", nt, peer, auth, "User")
	want := "S=407A5589115FD0D6209F510FE9C04566932CDA56"
	if got != want {
		t.Fatalf("got %s want %s", got, want)
	}
}

func TestDESKeyRFC2759(t *testing.T) {
	// RFC 2759 §9.3 first raw 7 octets → parity-corrected 8. Go DES strips parity.
	raw, _ := hex.DecodeString("FC156AF7EDCD6C")
	k := desKey(raw)
	if len(k) != 8 {
		t.Fatalf("len %d", len(k))
	}
	// 56 key bits must match FD 0B 5B 5E 7F 6E 34 D9 with parity ignored (LSB)
	want, _ := hex.DecodeString("FD0B5B5E7F6E34D9")
	for i := 0; i < 8; i++ {
		if k[i]&0xfe != want[i]&0xfe {
			t.Fatalf("byte %d got %02x want %02x", i, k[i], want[i])
		}
	}
}

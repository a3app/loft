package crypto

import (
	"bytes"
	"testing"
)

func TestAttachRoundTrip(t *testing.T) {
	key := bytes.Repeat([]byte{0xab}, KeySize)
	blob, err := MakeAttach(key, "aabbccddeeff001122334455", "h2")
	if err != nil {
		t.Fatal(err)
	}
	if !LooksLikeAttach(blob) || LooksLikeAuth(blob) {
		t.Fatalf("tag %x", blob[0])
	}
	sid, front, nonce, err := FinishAttach(key, blob)
	if err != nil || sid != "aabbccddeeff001122334455" || front != "h2" || len(nonce) != 16 {
		t.Fatalf("got %q %q %v", sid, front, err)
	}
	if _, _, _, err := FinishAttach(bytes.Repeat([]byte{0xcd}, KeySize), blob); err == nil {
		t.Fatal("wrong key")
	}
	bad := append([]byte(nil), blob...)
	bad[len(bad)-1] ^= 1
	if _, _, _, err := FinishAttach(key, bad); err == nil {
		t.Fatal("bad mac")
	}
}

func TestAttachReplayNonce(t *testing.T) {
	key := bytes.Repeat([]byte{0x11}, KeySize)
	blob, err := MakeAttach(key, "aabbccddeeff001122334455", "ws")
	if err != nil {
		t.Fatal(err)
	}
	_, _, nonce, err := FinishAttach(key, blob)
	if err != nil {
		t.Fatal(err)
	}
	seen := NewNonces()
	if err := seen.TakeAttach(nonce); err != nil {
		t.Fatal(err)
	}
	if err := seen.TakeAttach(nonce); err == nil {
		t.Fatal("replay")
	}
}

func TestAttachRejectsShortAndAuthTag(t *testing.T) {
	if LooksLikeAttach([]byte{tagAuth}) || LooksLikeAttach([]byte{tagAttach}) {
		t.Fatal("short")
	}
	if _, _, _, err := FinishAttach(bytes.Repeat([]byte{1}, KeySize), []byte{0x03, 1, 2, 3}); err == nil {
		t.Fatal("0x03")
	}
}

func TestAttachRejectsGetL3Connect(t *testing.T) {
	key := bytes.Repeat([]byte{0x22}, KeySize)
	for _, front := range []string{"get", "l3", "connect", "", "nope"} {
		if _, err := MakeAttach(key, "aabbccddeeff001122334455", front); err == nil {
			t.Fatalf("accepted %q", front)
		}
	}
}

package crypto

import (
	"bytes"
	"testing"

	"github.com/a3app/loft/internal/wire"
)

func TestBindInnerClearAndAES(t *testing.T) {
	key := bytes.Repeat([]byte{7}, KeySize)
	pt := []byte("mux-batch")
	sealed, err := Seal(key, pt, nil)
	if err != nil || bytes.Equal(sealed, pt) {
		t.Fatalf("chacha %d %v", len(sealed), err)
	}
	opened, err := Open(key, sealed, nil)
	if err != nil || !bytes.Equal(opened, pt) {
		t.Fatalf("open chacha %v", err)
	}

	BindInner(key, wire.InnerClear)
	if InnerOf(key) != wire.InnerClear {
		t.Fatal(InnerOf(key))
	}
	clear, err := Seal(key, pt, nil)
	if err != nil || !bytes.Equal(clear, pt) {
		t.Fatalf("clear %q %v", clear, err)
	}
	back, err := Open(key, clear, nil)
	if err != nil || !bytes.Equal(back, pt) {
		t.Fatalf("open clear %v", err)
	}

	BindInner(key, wire.InnerAES)
	aes, err := Seal(key, pt, nil)
	if err != nil || bytes.Equal(aes, pt) || bytes.Equal(aes, sealed) {
		t.Fatalf("aes %d %v", len(aes), err)
	}
	plain, err := Open(key, aes, nil)
	if err != nil || !bytes.Equal(plain, pt) {
		t.Fatalf("open aes %v", err)
	}
	if _, err := Open(key, sealed, nil); err == nil {
		t.Fatal("chacha blob opened as aes")
	}
	BindInner(key, wire.InnerChaCha)
	if InnerOf(key) != wire.InnerChaCha {
		t.Fatal(InnerOf(key))
	}
}

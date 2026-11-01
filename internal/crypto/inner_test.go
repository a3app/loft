package crypto

import (
	"bytes"
	"crypto/cipher"
	"errors"
	"sync"
	"testing"

	"github.com/a3app/loft/internal/wire"
)

var errPlainMismatch = errors.New("open plaintext mismatch")

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

func TestAEADCacheReused(t *testing.T) {
	key := bytes.Repeat([]byte{0xa1}, KeySize)
	pt := []byte("cache-batch")
	if _, err := Seal(key, pt, nil); err != nil {
		t.Fatal(err)
	}
	a, ok := chachaAEADs.Load(string(key))
	if !ok {
		t.Fatal("chacha AEAD missing")
	}
	if _, err := Seal(key, pt, nil); err != nil {
		t.Fatal(err)
	}
	b, _ := chachaAEADs.Load(string(key))
	if a.(cipher.AEAD) != b.(cipher.AEAD) {
		t.Fatal("chacha AEAD rebuilt")
	}

	BindInner(key, wire.InnerAES)
	if _, err := Seal(key, pt, nil); err != nil {
		t.Fatal(err)
	}
	c, ok := aesAEADs.Load(string(key))
	if !ok {
		t.Fatal("aes AEAD missing")
	}
	if _, err := Seal(key, pt, nil); err != nil {
		t.Fatal(err)
	}
	d, _ := aesAEADs.Load(string(key))
	if c.(cipher.AEAD) != d.(cipher.AEAD) {
		t.Fatal("aes AEAD rebuilt")
	}
	if a.(cipher.AEAD) == c.(cipher.AEAD) {
		t.Fatal("chacha and aes share an AEAD")
	}

	var wg sync.WaitGroup
	errc := make(chan error, 32)
	for i := 0; i < 32; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			s, err := Seal(key, pt, nil)
			if err != nil {
				errc <- err
				return
			}
			got, err := Open(key, s, nil)
			if err != nil {
				errc <- err
				return
			}
			if !bytes.Equal(got, pt) {
				errc <- errPlainMismatch
			}
		}()
	}
	wg.Wait()
	close(errc)
	for err := range errc {
		if err != nil {
			t.Fatal(err)
		}
	}
}

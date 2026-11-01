//go:build hiae || (!ios && !android && !darwin)

package crypto

import (
	"bytes"
	"testing"

	"github.com/a3app/loft/internal/wire"
)

func TestHiAEVectors(t *testing.T) {
	// draft-pham-cfrg-hiae-06 Appendix A, vectors 1, 2, 8, 11.
	cases := []struct {
		key, nonce, ad, msg, ct, tag string
	}{
		{
			"4b7a9c3ef8d2165a0b3e5f8c9d4a7b1e2c5f8a9d3b6e4c7f0a1d2e5b8c9f4a7d",
			"a5b8c2d9e3f4a7b1c8d5e9f2a3b6c7d8",
			"", "", "",
			"a25049aa37deea054de461d10ce7840b",
		},
		{
			"2f8e4d7c3b9a5e1f8d2c6b4a9f3e7d5c1b8a6f4e3d2c9b5a8f7e6d4c3b2a1f9e",
			"7c3e9f5a1d8b4c6f2e9a5d7b3f8c1e4a",
			"",
			"55f00fcc339669aa55f00fcc339669aa",
			"af9bd1865daa6fc351652589abf70bff",
			"ed9e2edc8241c3184fc08972bd8e9952",
		},
		{
			"7b6a5f4e3d2c1b0a9f8e7d6c5b4a3f2e1d0c9b8a7f6e5d4c3b2a1f0e9d8c7b6a",
			"2e7c9f5d3b8a4c6f1e9b5d7a3f8c2e4a",
			"",
			"ff",
			"21",
			"3cf9020bd1cc59cc5f2f6ce19f7cbf68",
		},
		{
			"1122334455667788112233445566778811223344556677881122334455667788",
			"aabbccddeeff0011aabbccddeeff0011",
			"0102030405060708090a0b0c0d",
			"48656c6c6f576f726c64",
			"1fb0e0348c6a3a917133",
			"7d292173b55ba02dae56ac1224b7e775",
		},
	}
	for i, c := range cases {
		key, nonce := unhex(t, c.key), unhex(t, c.nonce)
		ad, msg := unhex(t, c.ad), unhex(t, c.msg)
		want := append(unhex(t, c.ct), unhex(t, c.tag)...)
		got := hiaeEncrypt(key, nonce, msg, ad)
		if !bytes.Equal(got, want) {
			t.Fatalf("vec %d enc\n got %x\nwant %x", i+1, got, want)
		}
		plain, err := hiaeDecrypt(key, nonce, got, ad)
		if err != nil || !bytes.Equal(plain, msg) {
			t.Fatalf("vec %d dec %v %x", i+1, err, plain)
		}
		if len(got) > 0 {
			bad := append([]byte(nil), got...)
			bad[len(bad)-1] ^= 1
			if _, err := hiaeDecrypt(key, nonce, bad, ad); err == nil {
				t.Fatalf("vec %d forged", i+1)
			}
		}
	}
}

func TestHiAEAsmMatchesGo(t *testing.T) {
	if hiaeEncBlocksAsm == nil {
		t.Skip("no AES-NI/AESE ring")
	}
	enc, dec := hiaeEncBlocksAsm, hiaeDecBlocksAsm
	defer func() { hiaeEncBlocksAsm, hiaeDecBlocksAsm = enc, dec }()
	key := bytes.Repeat([]byte{0x11}, 32)
	nonce := bytes.Repeat([]byte{0x22}, 16)
	ad := bytes.Repeat([]byte{0x33}, 40)
	for _, n := range []int{0, 1, 15, 16, 17, 32, 63, 64, 1024, 4096} {
		msg := make([]byte, n)
		for i := range msg {
			msg[i] = byte(i)
		}
		hiaeEncBlocksAsm, hiaeDecBlocksAsm = nil, nil
		want := hiaeEncrypt(key, nonce, msg, ad)
		hiaeEncBlocksAsm, hiaeDecBlocksAsm = enc, dec
		got := hiaeEncrypt(key, nonce, msg, ad)
		if !bytes.Equal(got, want) {
			t.Fatalf("enc n=%d", n)
		}
		plain, err := hiaeDecrypt(key, nonce, got, ad)
		if err != nil || !bytes.Equal(plain, msg) {
			t.Fatalf("dec n=%d %v", n, err)
		}
	}
}

func TestBindInnerHiAE(t *testing.T) {
	if !HiAEEnabled() {
		t.Fatal("built")
	}
	key := bytes.Repeat([]byte{3}, KeySize)
	pt := []byte("hiae-batch")
	aad := []byte("sid")
	BindInner(key, wire.InnerHiAE)
	if InnerOf(key) != wire.InnerHiAE {
		t.Fatal(InnerOf(key))
	}
	sealed, err := Seal(key, pt, aad)
	if err != nil || len(sealed) != NonceSize+len(pt)+16 {
		t.Fatalf("seal %d %v", len(sealed), err)
	}
	back, err := Open(key, sealed, aad)
	if err != nil || !bytes.Equal(back, pt) {
		t.Fatalf("open %v %q", err, back)
	}
	if _, err := Open(key, sealed, nil); err == nil {
		t.Fatal("aad")
	}
	chacha, err := Seal(bytes.Repeat([]byte{4}, KeySize), pt, aad)
	if err != nil || bytes.Equal(chacha, sealed) {
		t.Fatal("same as chacha")
	}
}

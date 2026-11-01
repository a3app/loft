package crypto

import (
	"bytes"
	"testing"

	"github.com/a3app/loft/internal/wire"
)

func TestAESRoundRFC10032(t *testing.T) {
	in := loadBlk(unhex(t, "000102030405060708090a0b0c0d0e0f"))
	rk := loadBlk(unhex(t, "101112131415161718191a1b1c1d1e1f"))
	got := aesRound(in, rk)
	want := unhex(t, "7a7b4e5638782546a8c0477a3b813f43")
	if !bytes.Equal(got[:], want) {
		t.Fatalf("%x", got)
	}
}

func TestAEGIS128LUpdateRFC10032(t *testing.T) {
	var a aegis128L
	a.s = [8]blk{
		loadBlk(unhex(t, "9b7e60b24cc873ea894ecc07911049a3")),
		loadBlk(unhex(t, "330be08f35300faa2ebf9a7b0d274658")),
		loadBlk(unhex(t, "7bbd5bd2b049f7b9b515cf26fbe7756c")),
		loadBlk(unhex(t, "c35a00f55ea86c3886ec5e928f87db18")),
		loadBlk(unhex(t, "9ebccafce87cab446396c4334592c91f")),
		loadBlk(unhex(t, "58d83e31f256371e60fc6bb257114601")),
		loadBlk(unhex(t, "1639b56ea322c88568a176585bc915de")),
		loadBlk(unhex(t, "640818ffb57dc0fbc2e72ae93457e39a")),
	}
	a.update(
		loadBlk(unhex(t, "033e6975b94816879e42917650955aa0")),
		loadBlk(unhex(t, "fcc1968a46b7e97861bd6e89af6aa55f")),
	)
	want := []string{
		"596ab773e4433ca0127c73f60536769d",
		"790394041a3d26ab697bde865014652d",
		"38cf49e4b65248acd533041b64dd0611",
		"16d8e58748f437bfff1797f780337cee",
		"9689ecdf08228c74d7e3360cca53d0a5",
		"a21746bb193a569e331e1aa985d0d729",
		"09d714e6fcf9177a8ed1cde7e3d259a6",
		"61279ba73167f0ab76f0a11bf203bdff",
	}
	for i, w := range want {
		if !bytes.Equal(a.s[i][:], unhex(t, w)) {
			t.Fatalf("S%d %x", i, a.s[i])
		}
	}
}

func TestAEGIS128LVectorsRFC10032(t *testing.T) {
	key := unhex(t, "10010000000000000000000000000000")
	nonce := unhex(t, "10000200000000000000000000000000")
	cases := []struct {
		ad, msg, ct, tag string
	}{
		{"", "00000000000000000000000000000000", "c1c0e58bd913006feba00f4b3cc3594e", "abe0ece80c24868a226a35d16bdae37a"},
		{"", "", "", "c2b879a67def9d74e6c14f708bbcc9b4"},
		{
			"0001020304050607",
			"000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f",
			"79d94593d8c2119d7e8fd9b8fc77845c5c077a05b2528b6ac54b563aed8efe84",
			"cc6f3372f6aa1bb82388d695c3962d9a",
		},
		{
			"0001020304050607",
			"000102030405060708090a0b0c0d",
			"79d94593d8c2119d7e8fd9b8fc77",
			"5c04b3dba849b2701effbe32c7f0fab7",
		},
		{
			"000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20212223242526272829",
			"101112131415161718191a1b1c1d1e1f202122232425262728292a2b2c2d2e2f3031323334353637",
			"b31052ad1cca4e291abcf2df3502e6bdb1bfd6db36798be3607b1f94d34478aa7ede7f7a990fec10",
			"7542a745733014f9474417b337399507",
		},
	}
	for i, c := range cases {
		ad, msg, wantCT, wantTag := unhex(t, c.ad), unhex(t, c.msg), unhex(t, c.ct), unhex(t, c.tag)
		got := aegis128lEncrypt(key, nonce, msg, ad)
		if !bytes.Equal(got[:len(msg)], wantCT) || !bytes.Equal(got[len(msg):], wantTag) {
			t.Fatalf("enc %d ct=%x tag=%x", i, got[:len(msg)], got[len(msg):])
		}
		plain, err := aegis128lDecrypt(key, nonce, got, ad)
		if err != nil || !bytes.Equal(plain, msg) {
			t.Fatalf("dec %d %v %x", i, err, plain)
		}
	}
	bad := unhex(t, "79d94593d8c2119d7e8fd9b8fc77"+"5c04b3dba849b2701effbe32c7f0fab7")
	if _, err := aegis128lDecrypt(unhex(t, "10000200000000000000000000000000"), unhex(t, "10010000000000000000000000000000"), bad, unhex(t, "0001020304050607")); err == nil {
		t.Fatal("tv6 must fail")
	}
}

func TestBindInnerAEGIS(t *testing.T) {
	key := bytes.Repeat([]byte{3}, KeySize)
	pt := []byte("aegis-batch")
	BindInner(key, wire.InnerAEGIS)
	if InnerOf(key) != wire.InnerAEGIS {
		t.Fatal(InnerOf(key))
	}
	sealed, err := Seal(key, pt, nil)
	if err != nil || len(sealed) != NonceSize+len(pt)+16 || bytes.Equal(sealed, pt) {
		t.Fatalf("seal %d %v", len(sealed), err)
	}
	got, err := Open(key, sealed, nil)
	if err != nil || !bytes.Equal(got, pt) {
		t.Fatalf("open %v %q", err, got)
	}
	k16 := deriveAEGIS128L(key)
	if bytes.Equal(k16, key[:16]) {
		t.Fatal("aegis key is truncated session key")
	}
	BindInner(key, wire.InnerChaCha)
	if InnerOf(key) != wire.InnerChaCha {
		t.Fatal(InnerOf(key))
	}
}

func TestAEGISSealIntoMatchesSeal(t *testing.T) {
	key := bytes.Repeat([]byte{3}, KeySize)
	BindInner(key, wire.InnerAEGIS)
	pt := []byte("aegis-into")
	dst := make([]byte, 0, 4+NonceSize+len(pt)+AuthSize)
	dst = dst[:4]
	got, err := SealInto(dst, key, pt, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cap(got) != cap(dst) {
		t.Fatalf("realloc cap=%d", cap(got))
	}
	plain, err := Open(key, got[4:], nil)
	if err != nil || !bytes.Equal(plain, pt) {
		t.Fatalf("open %q %v", plain, err)
	}
}

func TestAEGISAsmMatchesGo(t *testing.T) {
	if aegisEncBlocksAsm == nil {
		t.Skip("no AES-NI/AESE AEGIS loop")
	}
	enc, dec := aegisEncBlocksAsm, aegisDecBlocksAsm
	defer func() { aegisEncBlocksAsm, aegisDecBlocksAsm = enc, dec }()
	key := bytes.Repeat([]byte{0x11}, 16)
	nonce := bytes.Repeat([]byte{0x22}, 16)
	ad := bytes.Repeat([]byte{0x33}, 40)
	for _, n := range []int{0, 1, 15, 16, 17, 31, 32, 33, 63, 64, 1024, 4096} {
		msg := make([]byte, n)
		for i := range msg {
			msg[i] = byte(i)
		}
		aegisEncBlocksAsm, aegisDecBlocksAsm = nil, nil
		want := aegis128lEncrypt(key, nonce, msg, ad)
		aegisEncBlocksAsm, aegisDecBlocksAsm = enc, dec
		got := aegis128lEncrypt(key, nonce, msg, ad)
		if !bytes.Equal(got, want) {
			t.Fatalf("enc n=%d", n)
		}
		plain, err := aegis128lDecrypt(key, nonce, got, ad)
		if err != nil || !bytes.Equal(plain, msg) {
			t.Fatalf("dec n=%d %v", n, err)
		}
	}
}

func BenchmarkAEGIS128L(b *testing.B) {
	benchAEGIS128L(b, true)
}

func BenchmarkAEGIS128LGo(b *testing.B) {
	benchAEGIS128L(b, false)
}

func benchAEGIS128L(b *testing.B, useAsm bool) {
	enc, dec := aegisEncBlocksAsm, aegisDecBlocksAsm
	if !useAsm {
		aegisEncBlocksAsm, aegisDecBlocksAsm = nil, nil
		defer func() { aegisEncBlocksAsm, aegisDecBlocksAsm = enc, dec }()
	}
	key := bytes.Repeat([]byte{0x11}, 16)
	nonce := bytes.Repeat([]byte{0x22}, 16)
	msg := bytes.Repeat([]byte{0x5a}, 12<<10)
	b.SetBytes(int64(len(msg)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = aegis128lEncrypt(key, nonce, msg, nil)
	}
}

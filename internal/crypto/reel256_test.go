//go:build reel256 || (!ios && !android && !darwin)

package crypto

import (
	"bytes"
	"testing"

	"github.com/a3app/loft/internal/wire"
)

func TestReelVectors(t *testing.T) {
	// andhash/reel256 test-vectors/reel256.json: empty, 1 B, 15 B, 16 B, 17 B+AD.
	cases := []struct {
		key, nonce, ad, msg, ct, tag string
	}{
		{
			"00070e151c232a313940474e555c636a727980878e959ca3abb2b9c0c7ced5dc",
			"1c232a31383f464d555c636a71787f86",
			"", "", "",
			"a8e54b4ee56a6e4e9048eeed2634d392",
		},
		{
			"5d646b727980878e969da4abb2b9c0c7cfd6dde4ebf2f900080f161d242b3239",
			"7980878e959ca3aab2b9c0c7ced5dce3",
			"",
			"b1",
			"af",
			"adf7f7acb935feb0333a1ae3dd224297",
		},
		{
			"7c838a91989fa6adb5bcc3cad1d8dfe6eef5fc030a11181f272e353c434a5158",
			"989fa6adb4bbc2c9d1d8dfe6edf4fb02",
			"",
			"d0d7dee5ecf3fa010910171e252c33",
			"947153861e7de5b8c330c78a98c643",
			"e2490a575a001e8a84680c72bd510f00",
		},
		{
			"9ba2a9b0b7bec5ccd4dbe2e9f0f7fe050d141b222930373e464d545b62697077",
			"b7bec5ccd3dae1e8f0f7fe050c131a21",
			"",
			"eff6fd040b121920282f363d444b5259",
			"65be37433793d6d75a32779d0e953356",
			"0a9e43a2851d78e77dc5680a25e27ceb",
		},
		{
			"bac1c8cfd6dde4ebf3fa01080f161d242c333a41484f565d656c737a81888f96",
			"d6dde4ebf2f900070f161d242b323940",
			"f2f900070e151c232b323940474e555c",
			"0e151c232a31383f474e555c636a717880",
			"8d7cf229cdfbabfdcd115930b8b1823f6e",
			"15d9c4f5863ed3b8b467592784c056dd",
		},
	}
	for i, c := range cases {
		key, nonce := unhex(t, c.key), unhex(t, c.nonce)
		ad, msg := unhex(t, c.ad), unhex(t, c.msg)
		want := append(unhex(t, c.ct), unhex(t, c.tag)...)
		got := reelEncrypt(key, nonce, msg, ad)
		if !bytes.Equal(got, want) {
			t.Fatalf("vec %d enc\n got %x\nwant %x", i+1, got, want)
		}
		plain, err := reelDecrypt(key, nonce, got, ad)
		if err != nil || !bytes.Equal(plain, msg) {
			t.Fatalf("vec %d dec %v %x", i+1, err, plain)
		}
		if len(got) > 0 {
			bad := append([]byte(nil), got...)
			bad[len(bad)-1] ^= 1
			if _, err := reelDecrypt(key, nonce, bad, ad); err == nil {
				t.Fatalf("vec %d forged", i+1)
			}
		}
	}
}

func TestReelAsmMatchesGo(t *testing.T) {
	if reelEncBlocksAsm == nil {
		t.Skip("no AES-NI/AESE ring")
	}
	enc, dec := reelEncBlocksAsm, reelDecBlocksAsm
	defer func() { reelEncBlocksAsm, reelDecBlocksAsm = enc, dec }()
	key := bytes.Repeat([]byte{0x44}, 32)
	nonce := bytes.Repeat([]byte{0x55}, 16)
	ad := bytes.Repeat([]byte{0x66}, 40)
	for _, n := range []int{0, 1, 15, 16, 17, 32, 63, 64, 1024, 4096} {
		msg := make([]byte, n)
		for i := range msg {
			msg[i] = byte(3 * i)
		}
		reelEncBlocksAsm, reelDecBlocksAsm = nil, nil
		want := reelEncrypt(key, nonce, msg, ad)
		reelEncBlocksAsm, reelDecBlocksAsm = enc, dec
		got := reelEncrypt(key, nonce, msg, ad)
		if !bytes.Equal(got, want) {
			t.Fatalf("enc n=%d", n)
		}
		plain, err := reelDecrypt(key, nonce, got, ad)
		if err != nil || !bytes.Equal(plain, msg) {
			t.Fatalf("dec n=%d %v", n, err)
		}
	}
}

func TestBindInnerReel(t *testing.T) {
	if !ReelEnabled() {
		t.Fatal("built")
	}
	key := bytes.Repeat([]byte{8}, KeySize)
	pt := []byte("reel-batch")
	aad := []byte("sid")
	BindInner(key, wire.InnerReel)
	if InnerOf(key) != wire.InnerReel {
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

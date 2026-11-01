package crypto

import (
	"bytes"
	"crypto/rand"
	"testing"
)

func TestAESLSoftZero(t *testing.T) {
	var z blk
	got := aeslSoft(z)
	want := bytes.Repeat([]byte{0x63}, 16)
	if !bytes.Equal(got[:], want) {
		t.Fatalf("AESL(0) = %x want %x", got, want)
	}
}

func TestAESLMatchesSoft(t *testing.T) {
	cases := []blk{{}, {0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff, 0xff}}
	for i := 0; i < 256; i++ {
		var x blk
		for j := 0; j < 16; j++ {
			x[j] = byte(i + j)
		}
		cases = append(cases, x)
	}
	var rnd [16]byte
	for i := 0; i < 256; i++ {
		if _, err := rand.Read(rnd[:]); err != nil {
			t.Fatal(err)
		}
		cases = append(cases, blk(rnd))
	}
	for i, x := range cases {
		got, want := aesl(x), aeslSoft(x)
		if got != want {
			t.Fatalf("%d aesl(%x)=\n %x\nwant %x", i, x, got, want)
		}
	}
}

func BenchmarkAESL(b *testing.B) {
	var x blk
	for i := 0; i < b.N; i++ {
		x = aesl(x)
	}
}

func BenchmarkAESLSoft(b *testing.B) {
	var x blk
	for i := 0; i < b.N; i++ {
		x = aeslSoft(x)
	}
}

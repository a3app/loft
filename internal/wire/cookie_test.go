package wire

import (
	"bytes"
	"encoding/base64"
	"testing"
)

func TestCookie85RoundTrip(t *testing.T) {
	for _, n := range []int{0, 1, 3, 4, 12, 28, 100, 12 << 10, 16<<10 - 40} {
		blob := make([]byte, n)
		for i := range blob {
			blob[i] = byte(i * 13)
		}
		s := EncodeUplink(blob, UplinkCookie85)
		if s == "" || s[0] != '~' {
			t.Fatalf("n=%d tag %q", n, s)
		}
		got, err := DecodeUplink(s)
		if err != nil {
			t.Fatalf("n=%d %v", n, err)
		}
		if !bytes.Equal(got, blob) {
			t.Fatalf("n=%d mismatch", n)
		}
	}
}

func TestDecodeUplinkBase64(t *testing.T) {
	blob := []byte("hello-loft")
	s := base64.RawURLEncoding.EncodeToString(blob)
	got, err := DecodeUplink(s)
	if err != nil || !bytes.Equal(got, blob) {
		t.Fatalf("%q %v", got, err)
	}
	if EncodeUplink(blob, UplinkBase64) != s {
		t.Fatal("base64")
	}
}

func TestCookie85FitsMoreThanBase64(t *testing.T) {
	blob := make([]byte, 12<<10)
	b64 := len(EncodeUplink(blob, UplinkBase64))
	z := len(EncodeUplink(blob, UplinkCookie85))
	if z >= b64 {
		t.Fatalf("z85 %d >= b64 %d", z, b64)
	}
}

func TestCookie85RejectsJunk(t *testing.T) {
	if _, err := DecodeUplink("~not!z85!!"); err == nil {
		t.Fatal("junk")
	}
}

package wire

import "testing"

func TestEncodeDecodeL3(t *testing.T) {
	pkt := []byte{0x45, 0x00, 0x00, 0x1c, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0}
	b := EncodeL3(L3IP, pkt)
	typ, payload, err := DecodeL3(b)
	if err != nil || typ != L3IP || string(payload) != string(pkt) {
		t.Fatalf("typ=%d payload=%x err=%v", typ, payload, err)
	}
	if _, _, err := DecodeL3(nil); err == nil {
		t.Fatal("short")
	}
}

func TestSplitL3Batch(t *testing.T) {
	a := make([]byte, 20)
	b := make([]byte, 24)
	a[0], b[0] = 0x45, 0x45
	plain := EncodeL3Batch([][]byte{a, b})
	got, err := SplitL3(plain)
	if err != nil || len(got) != 2 || len(got[0]) != 20 || len(got[1]) != 24 {
		t.Fatalf("got=%d err=%v", len(got), err)
	}
	one, err := SplitL3(EncodeL3(L3IP, a))
	if err != nil || len(one) != 1 || one[0][0] != 0x45 {
		t.Fatalf("one %v %v", one, err)
	}
	unk, err := SplitL3([]byte{0x7f, 1, 2, 3})
	if err != nil || unk != nil {
		t.Fatalf("unknown %v %v", unk, err)
	}
}

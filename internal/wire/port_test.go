package wire

import "testing"

func TestPort(t *testing.T) {
	if Port("443") != 443 {
		t.Fatalf("443")
	}
	if Port("65535") != 65535 {
		t.Fatalf("65535")
	}
	if Port("65536") != 0 {
		t.Fatalf("overflow")
	}
	if Port("-1") != 0 {
		t.Fatalf("neg")
	}
	if Port("x") != 0 {
		t.Fatalf("junk")
	}
}

func TestU16(t *testing.T) {
	if U16(1280) != 1280 {
		t.Fatalf("1280")
	}
	if U16(-1) != 0 {
		t.Fatalf("neg")
	}
	if U16(70000) != 65535 {
		t.Fatalf("clip")
	}
}

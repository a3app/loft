package transport

import "testing"

func TestStandTCPDefaultsToAutotune(t *testing.T) {
	t.Setenv("LOFT_STAND_TCPBUF", "")
	if _, pin := standTCP(); pin {
		t.Fatal("unset pins the buffer")
	}
	t.Setenv("LOFT_STAND_TCPBUF", "auto")
	if _, pin := standTCP(); pin {
		t.Fatal("auto pins the buffer")
	}
	t.Setenv("LOFT_STAND_TCPBUF", "262144")
	n, pin := standTCP()
	if !pin || n != 262144 {
		t.Fatalf("pin=%v n=%d", pin, n)
	}
	t.Setenv("LOFT_STAND_TCPBUF", "nope")
	if _, pin := standTCP(); pin {
		t.Fatal("bad value pins the buffer")
	}
}

func TestStandCopiesClamp(t *testing.T) {
	t.Setenv("LOFT_STAND_COPIES", "")
	if standCopies() != 1 {
		t.Fatal("default")
	}
	t.Setenv("LOFT_STAND_COPIES", "32")
	if standCopies() != 32 {
		t.Fatal("32")
	}
	t.Setenv("LOFT_STAND_COPIES", "1000")
	if standCopies() != 64 {
		t.Fatal("clamp")
	}
}

func TestStandLowatDefault(t *testing.T) {
	t.Setenv("LOFT_STAND_LOWAT", "")
	if g := standLowat(); g != notsentLowat {
		t.Fatalf("default %d", g)
	}
	t.Setenv("LOFT_STAND_LOWAT", "0")
	if standLowat() != 0 {
		t.Fatal("0 still sets a cap")
	}
	t.Setenv("LOFT_STAND_LOWAT", "off")
	if standLowat() != 0 {
		t.Fatal("off still sets a cap")
	}
	t.Setenv("LOFT_STAND_LOWAT", "8192")
	if g := standLowat(); g != 8192 {
		t.Fatalf("override %d", g)
	}
}

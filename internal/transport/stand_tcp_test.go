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

func TestStandGetDown(t *testing.T) {
	t.Setenv("LOFT_STAND_GETDOWN", "")
	if standGetDown(0) != getDownlinkBytes {
		t.Fatalf("default %d", standGetDown(0))
	}
	if standGetDown(128<<10) != 128<<10 {
		t.Fatal("conf")
	}
	t.Setenv("LOFT_STAND_GETDOWN", "262144")
	if standGetDown(128<<10) != 262144 {
		t.Fatal("env wins")
	}
	t.Setenv("LOFT_STAND_GETDOWN", "1024")
	if standGetDown(0) != 16<<10 {
		t.Fatal("floor")
	}
	t.Setenv("LOFT_STAND_GETDOWN", "2097152")
	if standGetDown(0) != 1<<20 {
		t.Fatal("ceiling")
	}
}

func TestStandPadMax(t *testing.T) {
	t.Setenv("LOFT_STAND_PADMAX", "")
	if n := standPadMax(16, 0); n != 8 {
		t.Fatalf("default 16 slots: %d", n)
	}
	if n := standPadMax(4, 0); n != 4 {
		t.Fatalf("clamp to slots: %d", n)
	}
	if n := standPadMax(16, 4); n != 4 {
		t.Fatalf("status: %d", n)
	}
	if n := standPadMax(4, 8); n != 4 {
		t.Fatalf("status clamp to slots: %d", n)
	}
	t.Setenv("LOFT_STAND_PADMAX", "4")
	if n := standPadMax(16, 8); n != 4 {
		t.Fatalf("env wins: %d", n)
	}
	t.Setenv("LOFT_STAND_PADMAX", "99")
	if n := standPadMax(16, 0); n != 16 {
		t.Fatal("clamp")
	}
}

func TestStandPadFull(t *testing.T) {
	t.Setenv("LOFT_STAND_PADFULL", "")
	if standPadFull() {
		t.Fatal("unset")
	}
	t.Setenv("LOFT_STAND_PADFULL", "1")
	if !standPadFull() {
		t.Fatal("on")
	}
	t.Setenv("LOFT_STAND_PADFULL", "0")
	if standPadFull() {
		t.Fatal("0")
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

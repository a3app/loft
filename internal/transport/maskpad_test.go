package transport

import (
	"testing"
	"time"
)

func TestDestRecFollowsThenWraps(t *testing.T) {
	pads := []int{100, 200, 300}
	if destRec(nil, 0) != 0 {
		t.Fatalf("empty")
	}
	if destRec(pads, 0) != 100 || destRec(pads, 1) != 200 || destRec(pads, 2) != 300 {
		t.Fatalf("first pass")
	}
	for i := 3; i < 40; i++ {
		got := destRec(pads, i)
		if got != 100 && got != 200 && got != 300 {
			t.Fatalf("wrap %d", got)
		}
	}
}

func TestJitterDurStaysInBand(t *testing.T) {
	if jitterDur(0) != 0 {
		t.Fatalf("zero")
	}
	d := 7500 * time.Millisecond
	lo, hi := d/2, d+d/3
	for i := 0; i < 64; i++ {
		got := jitterDur(d)
		if got < lo || got > hi {
			t.Fatalf("got %s", got)
		}
	}
}

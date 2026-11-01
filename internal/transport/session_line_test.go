package transport

import (
	"strings"
	"testing"
)

func TestSessionLineAfterMaskPub(t *testing.T) {
	const addr = "203.0.113.51:444"
	pub := make([]byte, 32)
	for i := range pub {
		pub[i] = byte(i + 1)
	}
	RememberMaskPub(addr, pub)
	RememberDecoy(addr, []string{"origin.example:443"})
	noteKEX("X25519MLKEM768")
	line := SessionLine("https://203.0.113.51:444/static/v3/build")
	for _, want := range []string{
		"next_sid=v3",
		"mask=on",
		"kex=X25519MLKEM768",
		"decoy=origin.example",
		"pbk=0102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f20",
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("missing %s in %s", want, line)
		}
	}
}

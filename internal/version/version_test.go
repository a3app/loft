package version

// Program stamp versus protocol 1.0, and --version flags.

import (
	"testing"

	"github.com/a3app/loft/internal/wire"
)

// TestLineSeparatesProgramAndProtocol keeps software 0.1.0 apart from protocol 1.0.
func TestLineSeparatesProgramAndProtocol(t *testing.T) {
	Version = "0.1.0"
	s := Line("loft-node")
	if s != "loft-node 0.1.0 (protocol 1.0)" {
		t.Fatal(s)
	}
}

// TestStamp fills Hello and Status program/software/proto fields.
func TestStamp(t *testing.T) {
	Version = "0.1.0"
	var h wire.Hello
	StampHello(&h)
	if h.Program != Client || h.Software != "0.1.0" || h.Max != wire.Proto {
		t.Fatalf("%+v", h)
	}
	var s wire.Status
	StampNode(&s)
	if s.Program != Node || s.Software != "0.1.0" || s.Min != wire.ProtoMin {
		t.Fatalf("%+v", s)
	}
}

// TestWanted accepts --version/-V and ignores unrelated flags.
func TestWanted(t *testing.T) {
	if !Wanted([]string{"--version"}) || !Wanted([]string{"-V"}) {
		t.Fatal("flag")
	}
	if Wanted([]string{"-dir", "/var/lib/loft"}) {
		t.Fatal("false positive")
	}
}

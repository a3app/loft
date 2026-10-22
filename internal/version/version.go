// Package version holds the program release (injected from VERSION, currently
// 1.0.25 when built) distinct from protocol 1.0 (0x0100). Handshake carries both.
package version

import (
	"fmt"
	"os"

	"github.com/a3app/loft/internal/wire"
)

// Program names on Hello/Status. Not git, OS, or Go.
const (
	Client = "loft"
	Node   = "loft-node"
)

// Version is the program release (node and clients). Injected at build:
//
//	-X github.com/a3app/loft/internal/version.Version=$(cat VERSION)
//
// Distinct from the protocol version. Handshake sends program name + this
// semver; not git, OS, or Go.
var Version = "dev"

// Protocol is the wire version string ("1.0").
func Protocol() string { return wire.ProtoString(wire.Proto) }

// StampHello fills program name, software semver, and default proto/caps.
func StampHello(h *wire.Hello) {
	h.Program = Client
	h.Software = Version
	if h.Max == 0 {
		h.Min, h.Max = wire.ProtoMin, wire.Proto
	}
	if h.Caps == 0 {
		h.Caps = wire.NodeCaps
	}
}

// StampNode fills node program name, software semver, and default proto/caps.
func StampNode(s *wire.Status) {
	s.Program = Node
	s.Software = Version
	if s.Max == 0 {
		s.Min, s.Max, s.Proto = wire.ProtoMin, wire.Proto, wire.Proto
	}
	if s.Caps == 0 {
		s.Caps = wire.NodeCaps
	}
}

// Line is "name VERSION (LOFT/1.0)".
func Line(name string) string {
	return fmt.Sprintf("%s %s (LOFT/%s)", name, Version, Protocol())
}

// Wanted reports whether args asked for the version and nothing else.
func Wanted(args []string) bool {
	if len(args) == 1 && args[0] == "version" {
		return true
	}
	for _, a := range args {
		if a == "--version" || a == "-version" || a == "-V" {
			return true
		}
	}
	return false
}

// Print writes Line(name) to stdout.
func Print(name string) {
	fmt.Println(Line(name))
}

// ExitIfAsked prints version and exits 0 when args asked for it.
func ExitIfAsked(name string, args []string) {
	if Wanted(args) {
		Print(name)
		os.Exit(0)
	}
}

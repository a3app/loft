package wire

import "strconv"

// Port parses a TCP/UDP port. Junk or a value outside 0–65535 is 0.
func Port(s string) uint16 {
	n, err := strconv.ParseUint(s, 10, 16)
	if err != nil {
		return 0
	}
	return uint16(n)
}

// U16 clips n into a uint16 so a config int cannot wrap on the wire.
func U16(n int) uint16 {
	if n < 0 {
		return 0
	}
	if n > 0xffff {
		return 0xffff
	}
	return uint16(n)
}

package wire

// CmdFront selects which attached front carries bulk mux.

// EncodeFront is the CmdFront payload: the front name (get, ws, h2, quic, h3).
func EncodeFront(name string) []byte {
	return []byte(name)
}

// ParseFrontCmd reads a CmdFront payload. Unknown names are empty.
func ParseFrontCmd(p []byte) string {
	s := string(p)
	f, err := ParseFront(s)
	if err != nil {
		return ""
	}
	if f == FrontConnect || f == FrontL3 {
		return ""
	}
	return f
}

// FrontFrame is CmdFront on stream 0.
func FrontFrame(name string) Frame {
	return Frame{Cmd: CmdFront, Payload: EncodeFront(name)}
}

// SplitFront pulls CmdFront out of a mux batch. last is the last valid name.
func SplitFront(fs []Frame) (rest []Frame, last string) {
	for _, f := range fs {
		if f.Cmd != CmdFront {
			rest = append(rest, f)
			continue
		}
		if n := ParseFrontCmd(f.Payload); n != "" {
			last = n
		}
	}
	return rest, last
}

package wire

import "encoding/binary"

// Resin frames are held until CmdReport, then retransmitted.
// Draft frames are sent once: inner TCP or UDP will produce a new frame
// if the application still needs the bytes. Close/Reset are resin but
// SplitUplink puts them on their own GET seq after Data so they cannot
// jump queued Data in the same Cookie.
func Resin(f Frame) bool {
	switch f.Cmd {
	case CmdOpen, CmdPing, CmdPong, CmdLeave, CmdTune, CmdReport, CmdClose, CmdReset:
		return true
	case CmdDgram:
		return dgramPort(f.Payload) == 53
	default:
		return false
	}
}

// dgramPort reads the Open-target port from a Dgram payload. 0 if short.
func dgramPort(payload []byte) uint16 {
	if len(payload) < 4 {
		return 0
	}
	n := int(binary.BigEndian.Uint16(payload[0:2]))
	if len(payload) < 2+n+2 {
		return 0
	}
	return binary.BigEndian.Uint16(payload[2+n:])
}

// PackResin puts control resin first, then draft, then Close/Reset, then Pad.
func PackResin(fs []Frame) []Frame {
	if len(fs) < 2 {
		return fs
	}
	control, draft, fin, pad := classifyUplink(fs)
	out := make([]Frame, 0, len(fs))
	out = append(out, control...)
	out = append(out, draft...)
	out = append(out, fin...)
	return append(out, pad...)
}

// SplitUplink cuts one mux Pull into GET seqs: control resin, draft Data,
// then Close/Reset. A 404 retries only that seq.
func SplitUplink(fs []Frame) [][]Frame {
	control, draft, fin, pad := classifyUplink(fs)
	var parts [][]Frame
	if len(control) > 0 {
		parts = append(parts, control)
	}
	if len(draft) > 0 {
		parts = append(parts, draft)
	}
	if len(fin) > 0 {
		parts = append(parts, fin)
	}
	if len(parts) == 0 {
		if len(pad) == 0 {
			return nil
		}
		return [][]Frame{pad}
	}
	if len(pad) > 0 {
		i := len(parts) - 1
		parts[i] = append(parts[i], pad...)
	}
	return parts
}

func classifyUplink(fs []Frame) (control, draft, fin, pad []Frame) {
	for _, f := range fs {
		switch {
		case f.Cmd == CmdPad:
			pad = append(pad, f)
		case f.Cmd == CmdClose || f.Cmd == CmdReset:
			fin = append(fin, f)
		case Resin(f):
			control = append(control, f)
		default:
			draft = append(draft, f)
		}
	}
	return
}

// ResinOnly drops draft and Pad.
func ResinOnly(fs []Frame) []Frame {
	var out []Frame
	for _, f := range fs {
		if Resin(f) {
			out = append(out, f)
		}
	}
	return out
}

// EncodeReport is u64be applied-through GET seq (server nextSeq-1).
func EncodeReport(applied uint64) []byte {
	var b [8]byte
	binary.BigEndian.PutUint64(b[:], applied)
	return b[:]
}

// ParseReport reads a CmdReport payload. Short payloads are ignored.
func ParseReport(p []byte) (uint64, bool) {
	if len(p) < 8 {
		return 0, false
	}
	return binary.BigEndian.Uint64(p[:8]), true
}

// ReportFrame is CmdReport on stream 0.
func ReportFrame(applied uint64) Frame {
	return Frame{Cmd: CmdReport, Payload: EncodeReport(applied)}
}

// SplitReport pulls CmdReport out of a mux batch. applied is the highest
// contiguous GET seq the peer has ingested.
func SplitReport(fs []Frame) (rest []Frame, applied uint64, ok bool) {
	for _, f := range fs {
		if f.Cmd != CmdReport {
			rest = append(rest, f)
			continue
		}
		a, yes := ParseReport(f.Payload)
		if yes && (!ok || a > applied) {
			applied = a
			ok = true
		}
	}
	return rest, applied, ok
}

package wire

import "encoding/binary"

// CmdTune payload is 4 bytes plus optional tail. Client event ≠ 0 reports
// a front break once; node event 0 sets live header_budget / inflight /
// h2_conns / pad_max. Zero inflight, budget, h2_conns, or pad_max means
// leave that field unchanged.
//
// This lets the GET limits change during a session. The client tells the
// node what went wrong (an HTTP 413, 404, 429, a GOAWAY from the CDN), and
// the node can answer with smaller limits: header_budget is the size of
// the Cookie per GET, inflight is how many GETs may be outstanding at once,
// pad_max is how many of those may be idle pads.
const (
	TuneSet    byte = 0
	TuneCalm   byte = 1 // H2 GOAWAY ENHANCE_YOUR_CALM
	Tune413    byte = 2
	Tune404    byte = 3
	TuneReset  byte = 4
	Tune429    byte = 5
	TuneGoaway byte = 6 // other GOAWAY
)

// Tune is one CmdTune payload.
type Tune struct {
	Event    byte
	Inflight byte
	Budget   uint16 // encoded Cookie / header budget, bytes
	H2Conns  byte   // optional 5th byte; 0 = leave unchanged
	PadMax   byte   // optional 6th byte; 0 = leave unchanged
}

// EncodeTune is event || inflight || u16be budget.
func EncodeTune(event, inflight byte, budget uint16) []byte {
	var b [4]byte
	b[0] = event
	b[1] = inflight
	binary.BigEndian.PutUint16(b[2:4], budget)
	return b[:]
}

// ParseTune reads a CmdTune payload. Short payloads are ignored.
func ParseTune(p []byte) (Tune, bool) {
	if len(p) < 4 {
		return Tune{}, false
	}
	t := Tune{
		Event:    p[0],
		Inflight: p[1],
		Budget:   binary.BigEndian.Uint16(p[2:4]),
	}
	if len(p) >= 5 {
		t.H2Conns = p[4]
	}
	if len(p) >= 6 {
		t.PadMax = p[5]
	}
	return t, true
}

// EncodeTuneAll is EncodeTune plus optional h2_conns and pad_max.
// pad_max writes h2_conns 0 first when that field is unset.
func EncodeTuneAll(t Tune) []byte {
	b := EncodeTune(t.Event, t.Inflight, t.Budget)
	if t.H2Conns > 0 || t.PadMax > 0 {
		b = append(b, t.H2Conns)
		if t.PadMax > 0 {
			b = append(b, t.PadMax)
		}
	}
	return b
}

// TuneFrame is CmdTune on stream 0.
func TuneFrame(event, inflight byte, budget uint16) Frame {
	return Frame{Cmd: CmdTune, Payload: EncodeTune(event, inflight, budget)}
}

// SplitTune pulls client events out of a mux batch. One incident per batch.
func SplitTune(fs []Frame) (rest []Frame, event byte) {
	for _, f := range fs {
		if f.Cmd != CmdTune {
			rest = append(rest, f)
			continue
		}
		t, ok := ParseTune(f.Payload)
		if ok && t.Event != 0 && event == 0 {
			event = t.Event
		}
	}
	return rest, event
}

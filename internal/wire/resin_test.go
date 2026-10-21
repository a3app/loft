package wire

import (
	"encoding/binary"
	"testing"
)

func TestResinClass(t *testing.T) {
	if !Resin(Frame{Cmd: CmdOpen}) || !Resin(Frame{Cmd: CmdTune}) || !Resin(Frame{Cmd: CmdLeave}) {
		t.Fatal("control is resin")
	}
	if !Resin(Frame{Cmd: CmdClose}) || !Resin(Frame{Cmd: CmdReset}) {
		t.Fatal("Close/Reset are resin")
	}
	if Resin(Frame{Cmd: CmdData, Payload: []byte("x")}) {
		t.Fatal("Data is draft")
	}
	if Resin(Frame{Cmd: CmdPad}) {
		t.Fatal("Pad is draft")
	}
	dns := Frame{Cmd: CmdDgram, Payload: dgramTarget("1.1.1.1", 53)}
	icmp := Frame{Cmd: CmdDgram, Payload: dgramTarget("1.1.1.1", 0)}
	if !Resin(dns) {
		t.Fatal("DNS dgram is resin")
	}
	if Resin(icmp) {
		t.Fatal("non-53 dgram is draft")
	}
}

func TestPackResinOrder(t *testing.T) {
	fs := []Frame{
		{Cmd: CmdData, StreamID: 1, Payload: []byte("bulk")},
		{Cmd: CmdOpen, StreamID: 3},
		{Cmd: CmdPad, Payload: []byte{1}},
		{Cmd: CmdDgram, Payload: dgramTarget("8.8.8.8", 53)},
		{Cmd: CmdDgram, Payload: dgramTarget("8.8.8.8", 443)},
	}
	got := PackResin(fs)
	if len(got) != 5 {
		t.Fatalf("%d", len(got))
	}
	if got[0].Cmd != CmdOpen || got[1].Cmd != CmdDgram || dgramPort(got[1].Payload) != 53 {
		t.Fatalf("resin first %+v", got)
	}
	if got[2].Cmd != CmdData || got[3].Cmd != CmdDgram || dgramPort(got[3].Payload) != 443 {
		t.Fatalf("draft next %+v", got)
	}
	if got[4].Cmd != CmdPad {
		t.Fatalf("pad last %+v", got[4])
	}
	only := ResinOnly(fs)
	if len(only) != 2 || only[0].Cmd != CmdOpen || only[1].Cmd != CmdDgram {
		t.Fatalf("%+v", only)
	}
}

func TestSplitUplinkSeparatesOpenDataClose(t *testing.T) {
	fs := []Frame{
		{Cmd: CmdOpen, StreamID: 1},
		{Cmd: CmdData, StreamID: 1, Payload: []byte("x")},
		{Cmd: CmdClose, StreamID: 1},
		{Cmd: CmdPad, Payload: []byte{1}},
	}
	parts := SplitUplink(fs)
	if len(parts) != 3 {
		t.Fatalf("%d %+v", len(parts), parts)
	}
	if parts[0][0].Cmd != CmdOpen || parts[1][0].Cmd != CmdData || parts[2][0].Cmd != CmdClose {
		t.Fatalf("%+v", parts)
	}
	if parts[2][len(parts[2])-1].Cmd != CmdPad {
		t.Fatalf("pad on close %+v", parts[2])
	}
}

func TestReportRoundTrip(t *testing.T) {
	f := ReportFrame(42)
	if f.Cmd != CmdReport || f.StreamID != 0 {
		t.Fatalf("%+v", f)
	}
	a, ok := ParseReport(f.Payload)
	if !ok || a != 42 {
		t.Fatalf("%d %v", a, ok)
	}
	rest, applied, ok := SplitReport([]Frame{
		{Cmd: CmdData, Payload: []byte("x")},
		ReportFrame(7),
		ReportFrame(9),
		{Cmd: CmdPad},
	})
	if !ok || applied != 9 || len(rest) != 2 || rest[0].Cmd != CmdData || rest[1].Cmd != CmdPad {
		t.Fatalf("applied=%d rest=%+v", applied, rest)
	}
	if _, ok := ParseReport(nil); ok {
		t.Fatal("short")
	}
}

func dgramTarget(host string, port uint16) []byte {
	b := make([]byte, 2+len(host)+2)
	binary.BigEndian.PutUint16(b[0:2], uint16(len(host)))
	copy(b[2:], host)
	binary.BigEndian.PutUint16(b[2+len(host):], port)
	return b
}

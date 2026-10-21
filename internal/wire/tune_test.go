package wire

import "testing"

func TestTuneRoundTrip(t *testing.T) {
	b := EncodeTune(TuneCalm, 16, 16<<10)
	got, ok := ParseTune(b)
	if !ok || got.Event != TuneCalm || got.Inflight != 16 || got.Budget != 16<<10 {
		t.Fatalf("%+v %v", got, ok)
	}
	if _, ok := ParseTune(b[:3]); ok {
		t.Fatal("short")
	}
}

func TestSplitTuneOneEvent(t *testing.T) {
	fs := []Frame{
		{Cmd: CmdData, Payload: []byte("x")},
		TuneFrame(TuneCalm, 16, 12<<10),
		TuneFrame(Tune413, 8, 8<<10),
		{Cmd: CmdPad, Payload: []byte{1}},
	}
	rest, ev := SplitTune(fs)
	if ev != TuneCalm {
		t.Fatalf("event %d", ev)
	}
	if len(rest) != 2 || rest[0].Cmd != CmdData || rest[1].Cmd != CmdPad {
		t.Fatalf("%+v", rest)
	}
}

func TestStatusHeaderBudgetRoundTrip(t *testing.T) {
	s := Status{
		Code: OK, IP: "10.8.0.2", Gateway: "10.8.0.1", InstanceID: "b",
		MTU: 1280, Proto: Proto, Min: ProtoMin, Max: Proto, Caps: NodeCaps,
		HeaderBudget: 16 << 10, Inflight: 16,
	}
	out, err := DecodeStatus(s.Encode())
	if err != nil {
		t.Fatal(err)
	}
	if out.HeaderBudget != 16<<10 || out.Inflight != 16 {
		t.Fatalf("%+v", out)
	}
	if out.IP6 != "" || out.Gateway6 != "" {
		t.Fatalf("ipv6 %q %q", out.IP6, out.Gateway6)
	}
}

func TestStatusOldBlobHasNoHeaderBudget(t *testing.T) {
	s := Status{Code: OK, IP: "10.8.0.2", Gateway: "10.8.0.1", InstanceID: "b", MTU: 1280, Proto: Proto, Min: ProtoMin, Max: Proto}
	out, err := DecodeStatus(s.Encode())
	if err != nil {
		t.Fatal(err)
	}
	if out.HeaderBudget != 0 || out.Inflight != 0 {
		t.Fatalf("%+v", out)
	}
}

func TestStatusH2ConnsAndUplinkEnc(t *testing.T) {
	s := Status{
		Code: OK, IP: "10.8.0.2", Gateway: "10.8.0.1", InstanceID: "b",
		MTU: 1280, Proto: Proto, Min: ProtoMin, Max: Proto, Caps: NodeCaps,
		HeaderBudget: 16 << 10, Inflight: 16, H2Conns: 2, UplinkEnc: UplinkCookie85,
	}
	out, err := DecodeStatus(s.Encode())
	if err != nil {
		t.Fatal(err)
	}
	if out.HeaderBudget != 16<<10 || out.Inflight != 16 || out.H2Conns != 2 || out.UplinkEnc != UplinkCookie85 {
		t.Fatalf("%+v", out)
	}
}

func TestStatusOldBudgetHasNoH2Conns(t *testing.T) {
	s := Status{
		Code: OK, IP: "10.8.0.2", Gateway: "10.8.0.1", InstanceID: "b",
		MTU: 1280, Proto: Proto, Min: ProtoMin, Max: Proto, Caps: NodeCaps,
		HeaderBudget: 16 << 10, Inflight: 16,
	}
	out, err := DecodeStatus(s.Encode())
	if err != nil {
		t.Fatal(err)
	}
	if out.H2Conns != 0 || out.UplinkEnc != 0 {
		t.Fatalf("%+v", out)
	}
}

func TestTuneOptionalH2Conns(t *testing.T) {
	b := EncodeTune(TuneSet, 16, 16<<10)
	b = append(b, 2)
	got, ok := ParseTune(b)
	if !ok || got.H2Conns != 2 || got.Inflight != 16 {
		t.Fatalf("%+v %v", got, ok)
	}
	old, ok := ParseTune(b[:4])
	if !ok || old.H2Conns != 0 {
		t.Fatalf("%+v", old)
	}
}

func TestStatusIPv6ThenBudget(t *testing.T) {
	s := Status{
		Code: OK, IP: "10.8.0.2", Gateway: "10.8.0.1", InstanceID: "b",
		MTU: 1280, Proto: Proto, Min: ProtoMin, Max: Proto, Caps: NodeCaps,
		IP6: "fd00::2", Gateway6: "fd00::1", HeaderBudget: 12 << 10, Inflight: 8,
	}
	out, err := DecodeStatus(s.Encode())
	if err != nil {
		t.Fatal(err)
	}
	if out.IP6 != "fd00::2" || out.Gateway6 != "fd00::1" || out.HeaderBudget != 12<<10 || out.Inflight != 8 {
		t.Fatalf("%+v", out)
	}
}

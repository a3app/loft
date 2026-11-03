package wire

import (
	"bytes"
	"testing"
)

func TestHelloCC(t *testing.T) {
	for _, pin := range [][]byte{nil, PinOf([]byte("controller"))} {
		h := Hello{Version: 1, MemberID: "m", Min: ProtoMin, Max: Proto, ControllerPin: pin, CC: CCFixed, CCUp: 20000, CCDown: 100000}
		got, err := DecodeHello(h.Encode())
		if err != nil {
			t.Fatal(err)
		}
		if got.CC != CCFixed || got.CCUp != 20000 || got.CCDown != 100000 || !bytes.Equal(got.ControllerPin, pin) {
			t.Fatalf("pin %x: %+v", pin, got)
		}
	}
	// No ask: the tail stops where it did before.
	h := Hello{Version: 1, MemberID: "m", Min: ProtoMin, Max: Proto, ControllerPin: PinOf([]byte("c"))}
	plain := h.Encode()
	h.CC = CCBBR
	if got, _ := DecodeHello(plain); got.CC != CCDefault {
		t.Fatal("cc without ask")
	}
	if len(h.Encode()) != len(plain)+9 {
		t.Fatal("cc tail is not 9 bytes")
	}
}

func TestStatusCC(t *testing.T) {
	st := Status{Code: OK, IP: "10.8.0.2", CC: CCFixed, CCUp: 1, CCDown: 2}
	got, err := DecodeStatus(st.Encode())
	if err != nil || got.CC != CCFixed || got.CCUp != 1 || got.CCDown != 2 || len(got.KeyRoll) != 0 {
		t.Fatalf("%+v %v", got, err)
	}
	if got, _ := DecodeStatus((Status{Code: OK, IP: "10.8.0.2"}).Encode()); got.CC != CCDefault {
		t.Fatal("cc from nothing")
	}
}

func TestStatusGetDownOptionalTail(t *testing.T) {
	st := Status{Code: OK, IP: "10.8.0.2", GetDown: 256 << 10}
	got, err := DecodeStatus(st.Encode())
	if err != nil || got.GetDown != 256<<10 {
		t.Fatalf("%+v %v", got, err)
	}
	if got.CC != CCDefault || got.Inner != InnerChaCha {
		t.Fatalf("pad inner/cc %+v", got)
	}
	old := Status{Code: OK, IP: "10.8.0.2", CC: CCFixed, CCUp: 1, CCDown: 2}
	got, err = DecodeStatus(old.Encode())
	if err != nil || got.GetDown != 0 || got.CC != CCFixed {
		t.Fatalf("cc without get_down %+v %v", got, err)
	}
	if ClampGetDown(0) != GetDownDefault || ClampGetDown(1024) != GetDownMin || ClampGetDown(2<<20) != GetDownMax {
		t.Fatal("clamp")
	}
	if ClampGetDown(-1) != GetDownDefault || ClampGetDown(1<<30) != GetDownMax {
		t.Fatal("clamp overflow")
	}
}

func TestStatusPadMaxOptionalTail(t *testing.T) {
	st := Status{Code: OK, IP: "10.8.0.2", GetDown: 256 << 10, PadMax: 8}
	got, err := DecodeStatus(st.Encode())
	if err != nil || got.PadMax != 8 || got.GetDown != 256<<10 {
		t.Fatalf("%+v %v", got, err)
	}
	old := Status{Code: OK, IP: "10.8.0.2", GetDown: 256 << 10}
	got, err = DecodeStatus(old.Encode())
	if err != nil || got.PadMax != 0 || got.GetDown != 256<<10 {
		t.Fatalf("get_down without pad_max %+v %v", got, err)
	}
	only := Status{Code: OK, IP: "10.8.0.2", PadMax: 4}
	got, err = DecodeStatus(only.Encode())
	if err != nil || got.PadMax != 4 || got.GetDown != 0 {
		t.Fatalf("pad_max pads get_down %+v %v", got, err)
	}
	if ClampPadMax(0) != PadMaxDefault || ClampPadMax(99) != PadMaxMax {
		t.Fatal("clamp")
	}
	if ClampPadMax(-1) != PadMaxDefault || ClampPadMax(256) != PadMaxMax || ClampPadMax(257) != PadMaxMax {
		t.Fatal("clamp overflow")
	}
}

func TestAgreeCC(t *testing.T) {
	tcp := CCOffer{Allow: []byte{CCBBR, CCFixed}, MaxDown: 50000}
	all := CCOffer{Allow: []byte{CCBBR, CCFixed}, QUIC: true, MaxDown: 50000}
	cases := []struct {
		name     string
		front    string
		ask      byte
		up, down uint32
		o        CCOffer
		id       byte
		u, d     uint32
	}{
		{"no ask", FrontH2, CCDefault, 0, 0, tcp, CCDefault, 0, 0},
		{"get never", FrontGET, CCBBR, 0, 0, tcp, CCDefault, 0, 0},
		{"node off", FrontQUIC, CCBBR, 0, 0, CCOffer{QUIC: true}, CCDefault, 0, 0},
		{"tcp stays default", FrontWS, CCBBR, 0, 0, tcp, CCDefault, 0, 0},
		{"tcp fixed stays default", FrontL3, CCFixed, 1, 1, tcp, CCDefault, 0, 0},
		{"quic bbr", FrontQUIC, CCBBR, 0, 0, all, CCBBR, 0, 0},
		{"l3 over h3 fixed", FrontL3, CCFixed, 1, 1, all, CCFixed, 1, 1},
		{"quic fixed capped", FrontQUIC, CCFixed, 10000, 90000, all, CCFixed, 10000, 50000},
		{"h3 fixed one way", FrontH3, CCFixed, 0, 30000, all, CCFixed, 0, 30000},
		{"fixed without rates", FrontQUIC, CCFixed, 0, 0, all, CCBBR, 0, 0},
		{"fixed not allowed", FrontQUIC, CCFixed, 1, 1, CCOffer{Allow: []byte{CCBBR}, QUIC: true}, CCBBR, 0, 0},
		{"bbr not allowed", FrontQUIC, CCBBR, 0, 0, CCOffer{Allow: []byte{CCFixed}, QUIC: true}, CCDefault, 0, 0},
		{"unknown id", FrontQUIC, 9, 0, 0, all, CCDefault, 0, 0},
	}
	for _, c := range cases {
		id, u, d := AgreeCC(c.front, c.ask, c.up, c.down, c.o)
		if id != c.id || u != c.u || d != c.d {
			t.Errorf("%s: got %d %d %d", c.name, id, u, d)
		}
	}
}

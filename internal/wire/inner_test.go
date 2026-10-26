package wire

import "testing"

func TestHelloInnerByte(t *testing.T) {
	h := Hello{Version: 1, MemberID: "a", InstanceID: "b", Min: ProtoMin, Max: Proto, Caps: MemberCaps, Program: "loft", Software: "1.0.28"}
	plain, err := DecodeHello(h.Encode())
	if err != nil || plain.InnerSet {
		t.Fatalf("absent %+v %v", plain, err)
	}
	h.InnerSet = true
	h.Inner = InnerClear
	raw := h.Encode()
	if raw[len(raw)-1] != InnerClear {
		t.Fatalf("last %d", raw[len(raw)-1])
	}
	got, err := DecodeHello(raw)
	if err != nil || !got.InnerSet || got.Inner != InnerClear {
		t.Fatalf("clear %+v %v", got, err)
	}
	cut, err := DecodeHello(raw[:len(raw)-1])
	if err != nil || cut.InnerSet {
		t.Fatalf("truncated %+v %v", cut, err)
	}
}

func TestStatusInnerByte(t *testing.T) {
	s := Status{
		Code: OK, IP: "10.8.0.2", Gateway: "10.8.0.1", InstanceID: "b",
		MTU: 1280, Proto: Proto, Min: ProtoMin, Max: Proto, Caps: NodeCaps,
		Fronts: []FrontEP{{Name: "ws", Path: "/api/v1/sync"}},
	}
	old, err := DecodeStatus(s.Encode())
	if err != nil || old.InnerSet {
		t.Fatalf("absent %+v %v", old.InnerSet, err)
	}
	s.InnerSet = true
	s.Inner = InnerClear
	raw := s.Encode()
	if raw[len(raw)-1] != InnerClear {
		t.Fatalf("last %d", raw[len(raw)-1])
	}
	got, err := DecodeStatus(raw)
	if err != nil || !got.InnerSet || got.Inner != InnerClear || got.Edge != EdgePlain {
		t.Fatalf("clear set=%v id=%d edge=%d %v", got.InnerSet, got.Inner, got.Edge, err)
	}
	if len(got.Fronts) != 1 || got.Fronts[0].Name != "ws" {
		t.Fatalf("fronts %+v", got.Fronts)
	}
	cut, err := DecodeStatus(raw[:len(raw)-1])
	if err != nil || cut.InnerSet {
		t.Fatalf("truncated set=%v %v", cut.InnerSet, err)
	}
	s.Inner = InnerAES
	s.Edge = EdgeTight
	aes, err := DecodeStatus(s.Encode())
	if err != nil || !aes.InnerSet || aes.Inner != InnerAES || aes.Edge != EdgeTight {
		t.Fatalf("aes %+v %v", aes.Inner, err)
	}
}

func TestAgreeInner(t *testing.T) {
	allow := []byte{InnerClear, InnerAES}
	if set, id := AgreeInner(FrontGET, true, InnerClear, allow); set || id != InnerChaCha {
		t.Fatalf("get %v %d", set, id)
	}
	if set, id := AgreeInner(FrontWS, false, InnerClear, allow); set || id != InnerChaCha {
		t.Fatalf("no ask %v %d", set, id)
	}
	if set, id := AgreeInner(FrontWS, true, InnerClear, nil); set || id != InnerChaCha {
		t.Fatalf("policy %v %d", set, id)
	}
	if set, id := AgreeInner(FrontWS, true, InnerClear, allow); !set || id != InnerClear {
		t.Fatalf("clear %v %d", set, id)
	}
	if set, id := AgreeInner(FrontH2, true, InnerAES, allow); !set || id != InnerAES {
		t.Fatalf("aes %v %d", set, id)
	}
	if set, id := AgreeInner(FrontWS, true, 9, allow); set || id != InnerChaCha {
		t.Fatalf("unknown %v %d", set, id)
	}
	if _, err := ParseInnerName("nope"); err == nil {
		t.Fatal("name")
	}
}

// TestHelloControllerPin: the pin rides after the inner byte; without a
// cipher request that byte is ChaCha, which asks for nothing.
func TestHelloControllerPin(t *testing.T) {
	pin := PinOf([]byte("controller public key"))
	h, err := DecodeHello(Hello{Program: "t", ControllerPin: pin}.Encode())
	if err != nil || string(h.ControllerPin) != string(pin) || h.Inner != InnerChaCha {
		t.Fatalf("pin round trip: %+v %v", h, err)
	}
	h, _ = DecodeHello(Hello{Program: "t", InnerSet: true, Inner: InnerAES, ControllerPin: pin}.Encode())
	if string(h.ControllerPin) != string(pin) || h.Inner != InnerAES {
		t.Fatalf("pin with AES: %+v", h)
	}
	if h, _ := DecodeHello(Hello{Program: "t", InnerSet: true, Inner: InnerAES}.Encode()); h.ControllerPin != nil {
		t.Fatal("pin out of nothing")
	}
}

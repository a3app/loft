package transport

import (
	"bytes"
	"testing"

	ecrypto "github.com/a3app/loft/internal/crypto"
	"github.com/a3app/loft/internal/wire"
)

func TestApplyInnerGETStaysChaCha(t *testing.T) {
	SetInnerAllow([]byte{wire.InnerClear, wire.InnerAES})
	t.Cleanup(func() { SetInnerAllow(nil) })
	hello := wire.Hello{Version: 1, Min: wire.ProtoMin, Max: wire.Proto, Caps: wire.MemberCaps, InnerSet: true, Inner: wire.InnerClear}.Encode()
	var st wire.Status
	applyInner(&st, wire.FrontGET, hello)
	if st.InnerSet {
		t.Fatal("get confirmed clear")
	}
	applyInner(&st, wire.FrontWS, hello)
	if !st.InnerSet || st.Inner != wire.InnerClear {
		t.Fatalf("ws %v %d", st.InnerSet, st.Inner)
	}
	SetInnerAllow(nil)
	st = wire.Status{}
	applyInner(&st, wire.FrontWS, hello)
	if st.InnerSet {
		t.Fatal("policy refused")
	}
}

func TestDirectFrontAgreesClear(t *testing.T) {
	// One process shares innerModes. Production peers do not: the server
	// commits after Seal(Status), the client opens that blob, then takeInner.
	SetInnerAllow([]byte{wire.InnerClear})
	setClientInner(true, wire.InnerClear)
	key := bytes.Repeat([]byte{9}, ecrypto.KeySize)
	t.Cleanup(func() {
		SetInnerAllow(nil)
		setClientInner(false, wire.InnerChaCha)
		ecrypto.BindInner(key, wire.InnerChaCha)
	})
	hello := newClientHello(wire.MemberCaps &^ wire.CapL3).Encode()
	st := wire.Status{
		Code: wire.OK, IP: "10.8.0.2", Gateway: "10.8.0.1", InstanceID: "b", MTU: 1280,
	}
	applyInner(&st, wire.FrontH2, hello)
	if !st.InnerSet || st.Inner != wire.InnerClear {
		t.Fatalf("confirm %v %d", st.InnerSet, st.Inner)
	}
	body := st.Encode()
	sealed, err := ecrypto.Seal(key, body, nil)
	if err != nil || bytes.Equal(sealed, body) {
		t.Fatal("handshake status must stay chacha")
	}
	plain, err := ecrypto.Open(key, sealed, nil)
	if err != nil || !bytes.Equal(plain, body) {
		t.Fatalf("open status %v", err)
	}
	got, err := wire.DecodeStatus(plain)
	if err != nil {
		t.Fatal(err)
	}
	takeInner(wire.FrontGET, key, got)
	if ecrypto.InnerOf(key) != wire.InnerChaCha {
		t.Fatal("get took clear")
	}
	takeInner(wire.FrontH2, key, got)
	if ecrypto.InnerOf(key) != wire.InnerClear {
		t.Fatalf("mode %d", ecrypto.InnerOf(key))
	}
	pt := []byte("batch")
	data, err := ecrypto.Seal(key, pt, nil)
	if err != nil || !bytes.Equal(data, pt) {
		t.Fatalf("data %q %v", data, err)
	}
}

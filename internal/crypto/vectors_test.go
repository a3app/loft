package crypto

import (
	"bytes"
	"crypto/ecdh"
	"crypto/ed25519"
	"encoding/hex"
	"testing"
	"time"

	"github.com/a3app/loft/internal/wire"
	"golang.org/x/crypto/chacha20poly1305"
)

// Inputs of docs/protocol.md §16.2–§16.6. Every value is fixed so a third
// party can reproduce the handshake byte for byte.
const (
	vecToken  = "lt_4576657279206c6574746572206973207072697661746521"
	vecTicket = "415254205849582031393438"
	vecInst   = "0123456789abcdef01234567"
)

func vecHex(b []byte) string { return hex.EncodeToString(b) }

func vecCheck(t *testing.T, name, got, want string) {
	t.Helper()
	if got != want {
		t.Errorf("%s\n got %s\nwant %s", name, got, want)
	}
}

func vecHello() []byte {
	return wire.Hello{
		Version:    1,
		InstanceID: vecInst,
		Min:        0x0100,
		Max:        0x0100,
		Caps: wire.CapGET | wire.CapWS | wire.CapStatus | wire.CapTimers | wire.CapHelloCaps |
			wire.CapReset | wire.CapDgram | wire.CapClockless | wire.CapTicket | wire.CapResin | wire.CapHold,
		Program:  "loft",
		Software: "1.0.28",
	}.Encode()
}

func vecStatus() wire.Status {
	return wire.Status{
		Code:        wire.OK,
		LeaseSec:    3600,
		MTU:         1380,
		KeepaliveMS: 15000,
		IdleMS:      120000,
		ReMinMS:     200,
		ReMaxMS:     8000,
		ReFastN:     5,
		IP:          "10.77.0.2",
		Gateway:     "10.77.0.1",
		InstanceID:  vecInst,
		ReFastForMS: 8000,
		Proto:       0x0100,
		Min:         0x0100,
		Max:         0x0100,
		Caps:        wire.CapGET | wire.CapWS | wire.CapStatus | wire.CapTimers | wire.CapHelloCaps | wire.CapClockless | wire.CapTicket,
		Program:     "loft-node",
		Software:    "1.0.28",
		Ticket:      []byte(vecTicket),
		DNS:         []string{"10.77.0.1"},
	}
}

// TestHandshakeVectors locks §16.2–§16.4: 0x02 blob, session key, Status, first node message.
func TestHandshakeVectors(t *testing.T) {
	token := []byte(vecToken)
	nonce := []byte("UDHR-1948-Art-12")
	skC, err := ecdh.X25519().NewPrivateKey([]byte("Every letter is private. Art. 12"))
	if err != nil {
		t.Fatal(err)
	}
	skN, err := ecdh.X25519().NewPrivateKey([]byte("the Net routes around censorship"))
	if err != nil {
		t.Fatal(err)
	}
	ephC, ephN := skC.PublicKey().Bytes(), skN.PublicKey().Bytes()
	hello := vecHello()

	mac := authMAC(token, nonce, ephC)
	blob := append([]byte{tagAuth}, nonce...)
	blob = append(blob, ephC...)
	blob = append(blob, mac...)
	blob = append(blob, hello...)
	a, err := FinishAuth([][]byte{token}, blob, NewNonces())
	if err != nil || !bytes.Equal(a.Rest, hello) {
		t.Fatalf("FinishAuth: %v", err)
	}

	keyC, err := SessionKey(skC, ephN, nonce)
	if err != nil {
		t.Fatal(err)
	}
	keyN, err := SessionKey(skN, ephC, nonce)
	if err != nil || !bytes.Equal(keyC, keyN) {
		t.Fatalf("session key: client and node differ (%v)", err)
	}

	status := vecStatus().Encode()
	sealNonce, _ := hex.DecodeString("554448520000000000000001")
	aead, err := chacha20poly1305.New(keyC)
	if err != nil {
		t.Fatal(err)
	}
	first := append(append([]byte(nil), ephN...), sealNonce...)
	first = append(first, aead.Seal(nil, sealNonce, status, []byte(vecTicket))...)
	key, plain, err := OpenHello(skC, nonce, first, []byte(vecTicket))
	if err != nil || !bytes.Equal(key, keyC) || !bytes.Equal(plain, status) {
		t.Fatalf("OpenHello: %v", err)
	}
	if _, err := wire.DecodeStatus(plain); err != nil {
		t.Fatal(err)
	}

	vecCheck(t, "eph_c", vecHex(ephC), "32c45e8f52d9c0bb8a6cc2ffbd4e77192774145cad9a8708357a519051124a79")
	vecCheck(t, "eph_n", vecHex(ephN), "f54cc5cde68414ebb35ced960185d2cd307a2f0942281d3c3b37133a0d55676e")
	vecCheck(t, "hello", vecHex(hello), "0100183031323334353637383961626364656630313233343536370100010000000000060077e0046c6f667406312e302e3238")
	vecCheck(t, "mac", vecHex(mac), "8efb91df15eb9b7f04fb08d442693be4")
	vecCheck(t, "auth", vecHex(blob), "02554448522d313934382d4172742d313232c45e8f52d9c0bb8a6cc2ffbd4e77192774145cad9a8708357a519051124a798efb91df15eb9b7f04fb08d442693be40100183031323334353637383961626364656630313233343536370100010000000000060077e0046c6f667406312e302e3238")
	vecCheck(t, "cookie", wire.EncodeUplink(blob, wire.UplinkBase64), "AlVESFItMTk0OC1BcnQtMTIyxF6PUtnAu4pswv-9TncZJ3QUXK2ahwg1elGQURJKeY77kd8V65t_BPsI1EJpO-QBABgwMTIzNDU2Nzg5YWJjZGVmMDEyMzQ1NjcBAAEAAAAAAAYAd-AEbG9mdAYxLjAuMjg")
	vecCheck(t, "key", vecHex(keyC), "560604d0c0dca87fd5a1180216ffce0feb8b4e2ac1e03166c36af7b78939157a")
	vecCheck(t, "status", vecHex(status), "000000000e100564000000003a980001d4c0000000c800001f4000050931302e37372e302e320931302e37372e302e311830313233343536373839616263646566303132333435363700001f4001000100010000000000000063e0096c6f66742d6e6f646506312e302e323800183431353235343230353834393538323033313339333433380000000000000000000000000000000000000000010931302e37372e302e31")
	vecCheck(t, "first", vecHex(first), "f54cc5cde68414ebb35ced960185d2cd307a2f0942281d3c3b37133a0d55676e55444852000000000000000177e1a52129d70e616413165f1ad9b635a1104d0fd7ec9f840ff529358825405d4f5f6e3f416695385291b4b92e286254688991005a7dd2e1450d2747f64138d4b8d913f75387f007b4e5826ba144a3277ecb1477443b8e78e9d6aac93da1f8382d56aae4f32544786437122fc046062869acc2e4cc1eaa492fdba7234a7b715b05077d1d433d3801eeb76a03e9c9bf6343dee64ceb7af54f238bd5e6a9e9b98c3568a8e5910c313784f6ff0e5421794caf67aaf371")
}

// TestStatusTailVector locks §16.4: gap_hold_ms and routes force the earlier optional fields.
func TestStatusTailVector(t *testing.T) {
	s := vecStatus()
	s.GapHoldMS = 8000
	s.Routes = []wire.Route{{Dst: "192.168.10.0/24", Via: "10.77.0.5"}}
	b := s.Encode()
	d, err := wire.DecodeStatus(b)
	if err != nil || d.GapHoldMS != 8000 || len(d.Routes) != 1 || d.Routes[0] != s.Routes[0] {
		t.Fatalf("DecodeStatus: %v %+v", err, d.Routes)
	}
	vecCheck(t, "status tail", vecHex(b[len(vecStatus().Encode()):]), "0000000000000000001f40010f3139322e3136382e31302e302f32340931302e37372e302e35")
}

// TestAttachVector locks §16.5: 0x04 under the session key of §16.3.
func TestAttachVector(t *testing.T) {
	skC, _ := ecdh.X25519().NewPrivateKey([]byte("Every letter is private. Art. 12"))
	skN, _ := ecdh.X25519().NewPrivateKey([]byte("the Net routes around censorship"))
	key, err := SessionKey(skC, skN.PublicKey().Bytes(), []byte("UDHR-1948-Art-12"))
	if err != nil {
		t.Fatal(err)
	}
	nonce := []byte("offline = online")
	blob := append([]byte{tagAttach, byte(len(vecTicket))}, vecTicket...)
	blob = append(blob, nonce...)
	blob = append(blob, 2, 'w', 's')
	blob = append(blob, attachMAC(key, vecTicket, nonce, "ws")...)
	if sid, front, _, err := FinishAttach(key, blob); err != nil || sid != vecTicket || front != "ws" {
		t.Fatalf("FinishAttach: %v", err)
	}
	vecCheck(t, "attach", vecHex(blob), "04183431353235343230353834393538323033313339333433386f66666c696e65203d206f6e6c696e650277730942e1cd2a573568a57c2c74dd7a358e")
}

// TestSignedVectors locks §16.6: roster, its manifest, and address lists.
func TestSignedVectors(t *testing.T) {
	priv := ed25519.NewKeyFromSeed([]byte("Liberty Of Free Transmission 1.0"))
	mask, _ := hex.DecodeString("cccb563311f0de9f0c3dd28b1450d978bdabfc63ca6d73cf561f2ff3ecc09443")
	r := SignRoster(priv, wire.Roster{Serial: 19481210, Relays: []wire.RelayInfo{{
		ID:      "self",
		URL:     "https://loft.example.com",
		Decoy:   "origin.example",
		Fronts:  []string{"get", "ws"},
		Path:    "/static/v3/build",
		MaskPub: mask,
	}}})
	if err := r.Verify(); err != nil {
		t.Fatal(err)
	}
	man, err := r.ManifestJSON()
	if err != nil {
		t.Fatal(err)
	}
	l := SignLists(priv, wire.Lists{Serial: 19661216, Sets: []wire.AddrList{{
		Name:    "lan",
		Entries: []wire.Addr{wire.ParseAddr("10.0.0.0/8"), wire.ParseAddr(".example.com")},
	}}})
	if err := l.Verify(); err != nil {
		t.Fatal(err)
	}
	h := l.Hash()
	vecCheck(t, "controller pub", vecHex(r.Pub), "0fd3ecac78db360ace8ab85e4d92d897feb4a5e8565421ee9dcc6efbaf63ae05")
	vecCheck(t, "roster", vecHex(r.Encode()), "000000000129427a010473656c66001868747470733a2f2f6c6f66742e6578616d706c652e636f6d0e6f726967696e2e6578616d706c6502036765740277730064102f7374617469632f76332f6275696c6420cccb563311f0de9f0c3dd28b1450d978bdabfc63ca6d73cf561f2ff3ecc09443200fd3ecac78db360ace8ab85e4d92d897feb4a5e8565421ee9dcc6efbaf63ae050040d3aedaddbc78bf5c8c29b7e5802b4ea8ae80acc57490ce8a8bfc1bc596f6ea15c861451304262fb6b4c05ddcf0d35ac2cfc6fdccfb531141b986f3a12c68410c")
	vecCheck(t, "manifest", string(man), "{\"v\":1,\"p\":\"AAAAAAEpQnoBBHNlbGYAGGh0dHBzOi8vbG9mdC5leGFtcGxlLmNvbQ5vcmlnaW4uZXhhbXBsZQIDZ2V0AndzAGQQL3N0YXRpYy92My9idWlsZCDMy1YzEfDenww90osUUNl4vav8Y8ptc89WHy_z7MCUQyAP0-yseNs2Cs6KuF5NktiX_rSl6FZUIe6dzG77r2OuBQBA067a3bx4v1yMKbflgCtOqK6ArMV0kM6Ki_wbxZb26hXIYUUTBCYvtrTAXdzw01rCz8b9zPtTEUG5hvOhLGhBDA\"}")
	vecCheck(t, "lists", vecHex(l.Encode()), "00000000012c01a001036c616e000203000a31302e302e302e302f3802000b6578616d706c652e636f6d200fd3ecac78db360ace8ab85e4d92d897feb4a5e8565421ee9dcc6efbaf63ae050040506182dab9d93d73af943ce5a5bcd097e024b7b51666acb654145b6a16f5c250cb6af43fb4a3b365a802bb4fe8f7b7be479e3bd84c1a7ca9f036feadd51b0203")
	vecCheck(t, "lists hash", vecHex(h[:]), "647fe575001d7d8dd007eb04efebacb72b23881ad5ea241ec4440692b22fddc4")
}

// TestProfileListsVector locks the operator profile of §16.6: a header set with
// an empty name, kinds 4–6 as metadata, and the Lists frames that carry it.
func TestProfileListsVector(t *testing.T) {
	priv := ed25519.NewKeyFromSeed([]byte("Liberty Of Free Transmission 1.0"))
	l := SignLists(priv, wire.Lists{Serial: 20261027, Sets: []wire.AddrList{
		{Name: "", Entries: []wire.Addr{{Kind: 5, Val: "RU direct"}, {Kind: 6, Val: "77.88.8.8"}}},
		{Name: "ru", Entries: []wire.Addr{{Kind: 4, Val: "direct"}, {Kind: 5, Val: "Russia"}, {Kind: 3, Val: "5.255.255.0/24"}, {Kind: 2, Val: "yandex.ru"}}},
		{Name: "ads", Entries: []wire.Addr{{Kind: 4, Val: "drop"}, {Kind: 2, Val: "doubleclick.net"}}},
	}})
	if err := l.Verify(); err != nil {
		t.Fatal(err)
	}
	blob := l.Encode()
	h := l.Hash()
	vecCheck(t, "profile lists", vecHex(blob), "00000000013528a30300000205000952552064697265637406000937372e38382e382e38027275000404000664697265637405000652757373696103000e352e3235352e3235352e302f323402000979616e6465782e727503616473000204000464726f7002000f646f75626c65636c69636b2e6e6574200fd3ecac78db360ace8ab85e4d92d897feb4a5e8565421ee9dcc6efbaf63ae05004035dbe6002c80709d7a5f7e7d8430bc17e8ec036925b7df8bfa99b85c4c2f6eb1dce5d661b66a8c779db891c5e922456c753daef8fce727c6b9c50a28256d3902")
	vecCheck(t, "profile hash", vecHex(h[:]), "81f9f2e4ae1ef80c152c3ce7de200061c289268a348a95b45d670912e7802114")
	// Metadata kinds never match a host or an IP.
	for _, s := range l.Sets {
		for _, e := range s.Entries {
			if e.Kind >= 4 && (e.MatchHost("yandex.ru") || e.MatchHost(e.Val)) {
				t.Errorf("kind %d %q matched a host", e.Kind, e.Val)
			}
		}
	}
	// Lists frames: cmd 13 on stream 0.
	frame := func(p []byte) string {
		b, err := wire.Frame{Cmd: 13, Payload: p}.Encode()
		if err != nil {
			t.Fatal(err)
		}
		return vecHex(b)
	}
	resp := append(hexBytes(t, "00000000013528a3000000da00000000"), blob...)
	vecCheck(t, "lists req", frame(make([]byte, 12)), "000c0d00000000000000000000000000000000")
	vecCheck(t, "lists resp", frame(resp), "00ea0d00000000"+vecHex(resp))
	vecCheck(t, "lists have", frame(hexBytes(t, "00000000013528a300000000")), "000c0d0000000000000000013528a300000000")
	vecCheck(t, "lists none", frame(hexBytes(t, "00000000013528a30000000000000000")), "00100d0000000000000000013528a30000000000000000")
}

func hexBytes(t *testing.T, s string) []byte {
	t.Helper()
	b, err := hex.DecodeString(s)
	if err != nil {
		t.Fatal(err)
	}
	return b
}

// TestCookie85Vector locks §16.7: Z85 pads u16be length || blob with zeros to a multiple of 4.
func TestCookie85Vector(t *testing.T) {
	frame, _ := hex.DecodeString("000202000000016869")
	vecCheck(t, "z85", wire.EncodeUplink(frame, wire.UplinkCookie85), "~00@Sb0SSi20CDfE")
}

// TestKeyRollVector is the controller key roll of protocol §16.9: the key of
// the roster vector hands over to the key from seed "Liberty Of Free
// Transmission 1.1", serial 1, not_after 2050-01-01.
func TestKeyRollVector(t *testing.T) {
	old := ed25519.NewKeyFromSeed([]byte("Liberty Of Free Transmission 1.0"))
	next := ed25519.NewKeyFromSeed([]byte("Liberty Of Free Transmission 1.1"))
	k := SignKeyRoll(old, next.Public().(ed25519.PublicKey), 1, time.Unix(2524608000, 0))
	want := "6c66742d726f6c6c000000000000000100000000967a76000fd3ecac78db360ace8ab85e4d92d897feb4a5e8565421ee9dcc6efbaf63ae054c4bd2cfd6b9b0741b7f7f31967f58e9e063ef28ab6edac047299effa80025f5" +
		"7712b8da2955ab0df3f7a6f64fbcc784280075f964bd85e0d95e2e72cd0a27c473c869ee53c08d63cdeb3f90611acda4b99db6e9c1b1d6a286afdda469b47103"
	if got := hex.EncodeToString(k.Encode()); got != want {
		t.Fatalf("key roll\n got %s\nwant %s", got, want)
	}
	back, err := wire.DecodeKeyRoll(k.Encode())
	if err != nil || back.Verify(time.Unix(2524608000, 0)) != nil {
		t.Fatalf("vector does not verify: %v", err)
	}
}

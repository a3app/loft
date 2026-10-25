package crypto

import (
	"bytes"
	"crypto/ecdh"
	"crypto/ed25519"
	"encoding/hex"
	"testing"

	"github.com/a3app/loft/internal/wire"
	"golang.org/x/crypto/chacha20poly1305"
)

// Inputs of docs/protocol.md §14.2–§14.6. Every value is fixed so a third
// party can reproduce the handshake byte for byte.
const (
	vecToken  = "lt_00112233445566778899aabbccddeeff0011223344556677"
	vecTicket = "a1b2c3d4e5f60718293a4b5c"
	vecInst   = "0123456789abcdef01234567"
)

func vecSeq(start byte, n int) []byte {
	b := make([]byte, n)
	for i := range b {
		b[i] = start + byte(i)
	}
	return b
}

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

// TestHandshakeVectors locks §14.2–§14.4: 0x02 blob, session key, Status, first node message.
func TestHandshakeVectors(t *testing.T) {
	token := []byte(vecToken)
	nonce := vecSeq(0x00, 16)
	skC, err := ecdh.X25519().NewPrivateKey(vecSeq(0x20, 32))
	if err != nil {
		t.Fatal(err)
	}
	skN, err := ecdh.X25519().NewPrivateKey(vecSeq(0x40, 32))
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
	sealNonce, _ := hex.DecodeString("010203040000000000000001")
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

	vecCheck(t, "eph_c", vecHex(ephC), "358072d6365880d1aeea329adf9121383851ed21a28e3b75e965d0d2cd166254")
	vecCheck(t, "eph_n", vecHex(ephN), "79a631eede1bf9c98f12032cdeadd0e7a079398fc786b88cc846ec89af85a51a")
	vecCheck(t, "hello", vecHex(hello), "0100183031323334353637383961626364656630313233343536370100010000000000060077e0046c6f667406312e302e3238")
	vecCheck(t, "mac", vecHex(mac), "00dd255f82e8d3e9afb3a74294cfadd1")
	vecCheck(t, "auth", vecHex(blob), "02000102030405060708090a0b0c0d0e0f358072d6365880d1aeea329adf9121383851ed21a28e3b75e965d0d2cd16625400dd255f82e8d3e9afb3a74294cfadd10100183031323334353637383961626364656630313233343536370100010000000000060077e0046c6f667406312e302e3238")
	vecCheck(t, "cookie", wire.EncodeUplink(blob, wire.UplinkBase64), "AgABAgMEBQYHCAkKCwwNDg81gHLWNliA0a7qMprfkSE4OFHtIaKOO3XpZdDSzRZiVADdJV-C6NPpr7OnQpTPrdEBABgwMTIzNDU2Nzg5YWJjZGVmMDEyMzQ1NjcBAAEAAAAAAAYAd-AEbG9mdAYxLjAuMjg")
	vecCheck(t, "key", vecHex(keyC), "864e5c505dde097ea873c026daa20d1ff565a2458ebee8bc49c58779b9a89920")
	vecCheck(t, "status", vecHex(status), "000000000e100564000000003a980001d4c0000000c800001f4000050931302e37372e302e320931302e37372e302e311830313233343536373839616263646566303132333435363700001f4001000100010000000000000063e0096c6f66742d6e6f646506312e302e323800186131623263336434653566363037313832393361346235630000000000000000000000000000000000000000010931302e37372e302e31")
	vecCheck(t, "first", vecHex(first), "79a631eede1bf9c98f12032cdeadd0e7a079398fc786b88cc846ec89af85a51a0102030400000000000000017e625678697eaa81d462a48e6d0d612023b3ce1eadd7fd76c1dc0b353685f4e25f2c2e0fdf2d68537a2f5f872446c0e56ae544da11a7a7ce353affc210c35c682006d4dcedb0ca3560368d1109b5fd47f76bb3fc2619e21c659d569ff56c1e5f923dc79fc23c013f2d7e891ad638e25868264abd03a0bea1dc10039fb1479e91b425195c1cacba02e3aae67d3d21864d545eecc4ff13b696ec480b713f8d74c0da3f40688299eae1690ffb44f887c1be108c566b59")
}

// TestStatusTailVector locks §14.4: gap_hold_ms and routes force the earlier optional fields.
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

// TestAttachVector locks §14.5: 0x04 under the session key of §14.3.
func TestAttachVector(t *testing.T) {
	skC, _ := ecdh.X25519().NewPrivateKey(vecSeq(0x20, 32))
	skN, _ := ecdh.X25519().NewPrivateKey(vecSeq(0x40, 32))
	key, err := SessionKey(skC, skN.PublicKey().Bytes(), vecSeq(0x00, 16))
	if err != nil {
		t.Fatal(err)
	}
	nonce := bytes.Repeat([]byte{0x33}, 16)
	blob := append([]byte{tagAttach, byte(len(vecTicket))}, vecTicket...)
	blob = append(blob, nonce...)
	blob = append(blob, 2, 'w', 's')
	blob = append(blob, attachMAC(key, vecTicket, nonce, "ws")...)
	if sid, front, _, err := FinishAttach(key, blob); err != nil || sid != vecTicket || front != "ws" {
		t.Fatalf("FinishAttach: %v", err)
	}
	vecCheck(t, "attach", vecHex(blob), "0418613162326333643465356636303731383239336134623563333333333333333333333333333333330277735ea380c409c8b53fa95ca96ea2cf9826")
}

// TestSignedVectors locks §14.6: roster, its manifest, and address lists.
func TestSignedVectors(t *testing.T) {
	priv := ed25519.NewKeyFromSeed(bytes.Repeat([]byte{0x07}, 32))
	mask, _ := hex.DecodeString("cccb563311f0de9f0c3dd28b1450d978bdabfc63ca6d73cf561f2ff3ecc09443")
	r := SignRoster(priv, wire.Roster{Serial: 1, Relays: []wire.RelayInfo{{
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
	l := SignLists(priv, wire.Lists{Serial: 1, Sets: []wire.AddrList{{
		Name:    "lan",
		Entries: []wire.Addr{wire.ParseAddr("10.0.0.0/8"), wire.ParseAddr(".example.com")},
	}}})
	if err := l.Verify(); err != nil {
		t.Fatal(err)
	}
	h := l.Hash()
	vecCheck(t, "controller pub", vecHex(r.Pub), "ea4a6c63e29c520abef5507b132ec5f9954776aebebe7b92421eea691446d22c")
	vecCheck(t, "roster", vecHex(r.Encode()), "0000000000000001010473656c66001868747470733a2f2f6c6f66742e6578616d706c652e636f6d0e6f726967696e2e6578616d706c6502036765740277730064102f7374617469632f76332f6275696c6420cccb563311f0de9f0c3dd28b1450d978bdabfc63ca6d73cf561f2ff3ecc0944320ea4a6c63e29c520abef5507b132ec5f9954776aebebe7b92421eea691446d22c004013bd9272db764ecd3aa9335db1c1aece3af0838f7df21a46e9b1d9391d52ecc48a19838063efcc622b3f7edb6e5d019c9f6d52d9ca3183e5b842178c80e1be0b")
	vecCheck(t, "manifest", string(man), "{\"v\":1,\"p\":\"AAAAAAAAAAEBBHNlbGYAGGh0dHBzOi8vbG9mdC5leGFtcGxlLmNvbQ5vcmlnaW4uZXhhbXBsZQIDZ2V0AndzAGQQL3N0YXRpYy92My9idWlsZCDMy1YzEfDenww90osUUNl4vav8Y8ptc89WHy_z7MCUQyDqSmxj4pxSCr71UHsTLsX5lUd2rr6-e5JCHuppFEbSLABAE72Sctt2Ts06qTNdscGuzjrwg4998hpG6bHZOR1S7MSKGYOAY-_MYis_fttuXQGcn21S2coxg-W4QheMgOG-Cw\"}")
	vecCheck(t, "lists", vecHex(l.Encode()), "000000000000000101036c616e000203000a31302e302e302e302f3802000b6578616d706c652e636f6d20ea4a6c63e29c520abef5507b132ec5f9954776aebebe7b92421eea691446d22c0040541d93536cb973e7188e50ba6d12bf34bf9ed123f063d00c59135802a86699c44fc71673500af47eb1f675588b44587569bbb8fad4e96980cf8166bcf3f99802")
	vecCheck(t, "lists hash", vecHex(h[:]), "eb289e3f96097285b9c703641b0467935938ca480af998847f2e3607e2348d25")
}

// TestCookie85Vector locks §14.7: Z85 pads u16be length || blob with zeros to a multiple of 4.
func TestCookie85Vector(t *testing.T) {
	frame, _ := hex.DecodeString("000202000000016869")
	vecCheck(t, "z85", wire.EncodeUplink(frame, wire.UplinkCookie85), "~00@Sb0SSi20CDfE")
}

package transport

import (
	"bytes"
	"encoding/hex"
	"testing"

	"golang.org/x/crypto/curve25519"
)

// TestPublishedVectors locks the sid v3, cert serial, and QUIC v3 mark
// printed in docs/protocol.md §16.
func TestPublishedVectors(t *testing.T) {
	node := []byte("loft-vector-node")
	var priv [32]byte
	for i := range priv {
		priv[i] = byte(i + 1)
	}
	pub, err := curve25519.X25519(priv[:], curve25519.Basepoint)
	if err != nil {
		t.Fatal(err)
	}
	random := bytes.Repeat([]byte{0x11}, 32)
	h := clientHello{Random: random, SNI: "origin.example", KeyShare: pub}
	maskPub := NodeMaskPub(node)
	sid := sid3FromPriv(priv[:], maskPub, h)
	shared := authSharedFromPub(priv[:], maskPub)
	serial := certSerial(shared, random)
	// QUIC v3: client_random is the ephemeral public (top bit left clear).
	qRand := append([]byte(nil), pub...)
	scid := quicMark("v3", shared, qRand)

	got := hex.EncodeToString(maskPub) + " " + hex.EncodeToString(pub) + " " + hex.EncodeToString(sid) + " " + hex.EncodeToString(serial) + " " + hex.EncodeToString(scid)
	const want = "cccb563311f0de9f0c3dd28b1450d978bdabfc63ca6d73cf561f2ff3ecc09443 07a37cbc142093c8b755dc1b10e86cb426374ad16aa853ed0bdfc0b2b86d1c7c 699f169d38b0f02b98ade7ad3234aab2006d7cb3da449842d3a0d791b12dfb87 46112462b0f8327be74b9dac22a7ce55 25b8c53ebe475334b474445e3ee3253691b18faa"
	if got != want {
		t.Fatalf("vector\n got %s\nwant %s", got, want)
	}
}

// TestSidV2Vector locks sid v2 in docs/protocol.md §16.8: same ClientHello
// inputs as sid v3, member key of §16.2 as ikm.
func TestSidV2Vector(t *testing.T) {
	var priv [32]byte
	for i := range priv {
		priv[i] = byte(i + 1)
	}
	pub, err := curve25519.X25519(priv[:], curve25519.Basepoint)
	if err != nil {
		t.Fatal(err)
	}
	h := clientHello{Random: bytes.Repeat([]byte{0x11}, 32), SNI: "origin.example", KeyShare: pub}
	got := hex.EncodeToString(sessionHMAC([]byte("lt_4576657279206c6574746572206973207072697661746521"), h))
	const want = "d2953bbc3bcad53751c4eebc631d4a48425731080439c99eae5bcd148a79b9ca"
	if got != want {
		t.Fatalf("sid v2\n got %s\nwant %s", got, want)
	}
}

// TestLFTVector locks the 42-byte P2P preface in docs/protocol.md §16.8.
func TestLFTVector(t *testing.T) {
	local := []byte("Privacy is necessary. Hughes '93")
	peer := []byte("This is for everyone. TimBL 2012")
	peerPub, err := curve25519.X25519(peer, curve25519.Basepoint)
	if err != nil {
		t.Fatal(err)
	}
	shared, err := curve25519.X25519(local, peerPub)
	if err != nil {
		t.Fatal(err)
	}
	src := []byte{10, 77, 0, 2}
	nonce := []byte("Art.23 Konst. RF")
	buf := make([]byte, lftSize)
	copy(buf[0:4], lftMagic[:])
	copy(buf[4:8], src)
	copy(buf[8:24], lftMAC(shared, nonce, src, 8080))
	copy(buf[24:40], nonce)
	buf[40], buf[41] = 8080>>8, 8080&0xff
	got := hex.EncodeToString(peerPub) + " " + hex.EncodeToString(buf)
	const want = "2a4cd76bd42aaa397c78d84f0e3fa3643dfad73d128237b66e8572688fc8a306 4c4654000a4d00026986af299cbcb6931c57169b90268c704172742e3233204b6f6e73742e2052461f90"
	if got != want {
		t.Fatalf("lft\n got %s\nwant %s", got, want)
	}
}

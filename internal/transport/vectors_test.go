package transport

import (
	"bytes"
	"encoding/hex"
	"testing"

	"golang.org/x/crypto/curve25519"
)

// TestPublishedVectors locks the sid v3, cert serial, and QUIC v3 mark
// printed in docs/protocol.md §14.
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

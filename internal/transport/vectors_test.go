package transport

import (
	"bytes"
	"crypto/ecdh"
	"encoding/hex"
	"io"
	"net"
	"testing"
	"time"

	"github.com/a3app/loft/internal/wire"
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

// TestP2PVector locks the sealed P2P handshake in docs/protocol.md §16.8:
// both messages with fixed keys, time and padding, and the first record.
func TestP2PVector(t *testing.T) {
	key := func(seed string) *ecdh.PrivateKey {
		k, err := ecdh.X25519().NewPrivateKey([]byte(seed))
		if err != nil {
			t.Fatal(err)
		}
		return k
	}
	local := key("Privacy is necessary. Hughes '93") // initiator member key
	peer := key("This is for everyone. TimBL 2012")  // responder member key
	e := key("Art.23 Konst. RF secrecy of mail")     // initiator fresh key
	r := key("Article 12 UDHR: no interference")     // responder fresh key
	ePub, rPub := e.PublicKey().Bytes(), r.PublicKey().Bytes()
	static, _ := local.ECDH(peer.PublicKey())
	es, _ := e.ECDH(peer.PublicKey())
	ee, _ := e.ECDH(r.PublicKey())
	hello := p2pHelloMsg(ePub, es, static, []byte{10, 77, 0, 2}, 8080, 2524608000, wire.InnerChaCha, []byte("pad"))
	reply := p2pReplyMsg(rPub, es, ePub, wire.InnerChaCha, nil)
	ir, _ := p2pDataKeys(ee, es, static, ePub, rPub)
	var rec bytes.Buffer
	pc := newP2PConn(&bufConn{&rec}, ir, ir)
	pc.Write([]byte("ping"))
	got := hex.EncodeToString(hello) + " " + hex.EncodeToString(reply) + " " + hex.EncodeToString(rec.Bytes())
	const want = "1222bc2690224afdf79af2353f4b1e6a413509a02b55edf17ee77b120c97f02ff329464388923d4616c229688f928cd3171fcbc3a2c9cda759cc35c2a7acedf68456d92f248fe6fa61b6abe0a6bb909c706164 24c5c85b756c65ee52638724ee40049b69916e77d878bc507b72d04211f5543ecb44343ff260d15fdd3a7e12c2cc6e8592c9 39b9fc9869bfe0580b40de9f12a7c1a3dd87f6469543822a1385ea6e9364ee21edd26e9e8d7b"
	if got != want {
		t.Fatalf("p2p\n got %s\nwant %s", got, want)
	}
}

// bufConn is a net.Conn that only writes into a buffer.
type bufConn struct{ w *bytes.Buffer }

func (b *bufConn) Read([]byte) (int, error)         { return 0, io.EOF }
func (b *bufConn) Write(p []byte) (int, error)      { return b.w.Write(p) }
func (b *bufConn) Close() error                     { return nil }
func (b *bufConn) LocalAddr() net.Addr              { return nil }
func (b *bufConn) RemoteAddr() net.Addr             { return nil }
func (b *bufConn) SetDeadline(time.Time) error      { return nil }
func (b *bufConn) SetReadDeadline(time.Time) error  { return nil }
func (b *bufConn) SetWriteDeadline(time.Time) error { return nil }

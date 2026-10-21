package transport

// LFT pairwise ECDH: two member keys, one shared MAC, hub still the fallback.

import (
	"bytes"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/binary"
	"net"
	"testing"

	"github.com/a3app/loft/internal/wire"
)

func TestLftPairwiseMAC(t *testing.T) {
	alice, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	bob, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	sa, err := alice.ECDH(bob.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	sb, err := bob.ECDH(alice.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(sa, sb) {
		t.Fatal("ecdh mismatch")
	}
	src := net.IPv4(10, 8, 0, 2).To4()
	nonce := bytes.Repeat([]byte{1}, 16)
	if !bytes.Equal(lftMAC(sa, nonce, src, 22), lftMAC(sb, nonce, src, 22)) {
		t.Fatal("mac mismatch")
	}
}

func TestLFTReadHello(t *testing.T) {
	t.Cleanup(resetP2PKeyForTest)
	t.Cleanup(func() { RememberVis(wire.Status{}) })
	alice, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	bob, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	shared, err := alice.ECDH(bob.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	src := net.IPv4(10, 8, 0, 2).To4()
	nonce := bytes.Repeat([]byte{7}, 16)
	buf := make([]byte, lftSize)
	copy(buf[0:4], lftMagic[:])
	copy(buf[4:8], src)
	copy(buf[8:24], lftMAC(shared, nonce, src, 22))
	copy(buf[24:40], nonce)
	binary.BigEndian.PutUint16(buf[40:42], 22)

	p2pKeyMu.Lock()
	p2pKey = bob
	p2pKeyMu.Unlock()
	RememberVis(wire.Status{PeerHints: []wire.PeerHint{{IP: "10.8.0.2", Pub: alice.PublicKey().Bytes()}}})

	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()
	go func() { _, _ = c1.Write(buf) }()
	port, src, ok := p2pReadHello(c2)
	if !ok || port != 22 || src.String() != "10.8.0.2" {
		t.Fatalf("port=%d src=%v ok=%v", port, src, ok)
	}
}

func TestLFTRejectsForeignKey(t *testing.T) {
	t.Cleanup(resetP2PKeyForTest)
	t.Cleanup(func() { RememberVis(wire.Status{}) })
	alice, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	bob, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	eve, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	shared, err := eve.ECDH(bob.PublicKey())
	if err != nil {
		t.Fatal(err)
	}
	src := net.IPv4(10, 8, 0, 2).To4()
	nonce := bytes.Repeat([]byte{3}, 16)
	buf := make([]byte, lftSize)
	copy(buf[0:4], lftMagic[:])
	copy(buf[4:8], src)
	copy(buf[8:24], lftMAC(shared, nonce, src, 22))
	copy(buf[24:40], nonce)
	binary.BigEndian.PutUint16(buf[40:42], 22)

	p2pKeyMu.Lock()
	p2pKey = bob
	p2pKeyMu.Unlock()
	RememberVis(wire.Status{PeerHints: []wire.PeerHint{{IP: "10.8.0.2", Pub: alice.PublicKey().Bytes()}}})

	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()
	go func() { _, _ = c1.Write(buf) }()
	if _, _, ok := p2pReadHello(c2); ok {
		t.Fatal("accepted eve")
	}
}

func TestP2PPermitDefaultDeny(t *testing.T) {
	t.Cleanup(func() { RememberVis(wire.Status{}) })
	RememberVis(wire.Status{
		IP: "10.8.0.2", Gateway: "10.8.0.1", Peers: 1, P2P: 1,
		Policy: wire.Policy{Present: true, ACLDefault: 1, OwnTags: []string{"ops"}},
	})
	src := net.ParseIP("10.8.0.2")
	dst := net.ParseIP("10.8.0.5")
	if p2pPermit(src, dst, "tcp", 22, 0, 0) {
		t.Fatal("default deny")
	}
	RememberVis(wire.Status{
		IP: "10.8.0.2", Gateway: "10.8.0.1", Peers: 1, P2P: 1,
		Policy: wire.Policy{
			Present: true, ACLDefault: 1, OwnTags: []string{"ops"},
			Rules: []wire.PolicyRule{{Action: 1, SrcTag: "ops", DstTag: "db", Proto: 1, Dport: []uint16{5432}}},
			Peers: []wire.PeerTags{{IP: "10.8.0.5", Tags: []string{"db"}}},
		},
	})
	if !p2pPermit(src, dst, "tcp", 5432, 0, 0) {
		t.Fatal("permit tag")
	}
	if p2pPermit(src, dst, "tcp", 22, 0, 0) {
		t.Fatal("port")
	}
	RememberVis(wire.Status{IP: "10.8.0.2", Peers: 1, P2P: 1})
	if !p2pPermit(src, dst, "tcp", 22, 0, 0) {
		t.Fatal("old node")
	}
}

func TestTryP2PHonorsPolicy(t *testing.T) {
	t.Cleanup(func() {
		SetP2PClient(false)
		RememberVis(wire.Status{})
	})
	SetP2PClient(true)
	pub := make([]byte, 32)
	pub[0] = 9
	RememberVis(wire.Status{
		IP: "10.8.0.2", Peers: 1, P2P: 1,
		Policy:    wire.Policy{Present: true, ACLDefault: 1},
		PeerHints: []wire.PeerHint{{IP: "10.8.0.5", Host: "127.0.0.1", Port: 9, Pub: pub}},
	})
	if c := tryP2P("10.8.0.5:22"); c != nil {
		c.Close()
		t.Fatal("denied dial")
	}
}

func TestLFTRejectsOldMagic(t *testing.T) {
	for _, mag := range []string{"LFT2", "LFT3"} {
		buf := make([]byte, lftSize)
		copy(buf[0:4], mag)
		c1, c2 := net.Pipe()
		go func() { _, _ = c1.Write(buf) }()
		ok := false
		func() {
			defer c1.Close()
			defer c2.Close()
			_, _, ok = p2pReadHello(c2)
		}()
		if ok {
			t.Fatalf("accepted %s", mag)
		}
	}
}

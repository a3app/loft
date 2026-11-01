package transport

// Sealed direct P2P: handshake, records, refusals; hub stays the fallback.

import (
	"bytes"
	"crypto/ecdh"
	"crypto/rand"
	"io"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/a3app/loft/internal/wire"
)

// recConn records what is written to it.
type recConn struct {
	net.Conn
	mu  sync.Mutex
	out []byte
}

func (r *recConn) Write(b []byte) (int, error) {
	r.mu.Lock()
	r.out = append(r.out, b...)
	r.mu.Unlock()
	return r.Conn.Write(b)
}

func p2pKeys(t *testing.T, n int) []*ecdh.PrivateKey {
	var ks []*ecdh.PrivateKey
	for i := 0; i < n; i++ {
		k, err := ecdh.X25519().GenerateKey(rand.Reader)
		if err != nil {
			t.Fatal(err)
		}
		ks = append(ks, k)
	}
	return ks
}

// p2pPair runs the handshake between initiator key a (at 10.8.0.2) and
// responder key b, whose hints name hinted as the key of 10.8.0.2.
func p2pPair(t *testing.T, a, b *ecdh.PrivateKey, hinted []byte) (ic, rc net.Conn, port uint16, src net.IP, wire *recConn, err error) {
	c1, c2 := net.Pipe()
	t.Cleanup(func() { c1.Close(); c2.Close() })
	wire = &recConn{Conn: c1}
	type res struct {
		c   net.Conn
		err error
	}
	done := make(chan res, 1)
	go func() {
		c, err := p2pDialAs(wire, a, net.IPv4(10, 8, 0, 2).To4(), 22, b.PublicKey().Bytes())
		if err != nil {
			c1.Close()
		}
		done <- res{c, err}
	}()
	pubOf := func(ip string) []byte {
		if ip == "10.8.0.2" {
			return hinted
		}
		return nil
	}
	rc, port, src, err = p2pAcceptAs(c2, b, pubOf)
	if err != nil {
		c2.Close()
	}
	r := <-done
	if err == nil {
		err = r.err
	}
	return r.c, rc, port, src, wire, err
}

func TestP2PSealedRoundTrip(t *testing.T) {
	k := p2pKeys(t, 2)
	ic, rc, port, src, w, err := p2pPair(t, k[0], k[1], k[0].PublicKey().Bytes())
	if err != nil || port != 22 || src.String() != "10.8.0.2" {
		t.Fatalf("port=%d src=%v err=%v", port, src, err)
	}
	big := bytes.Repeat([]byte("SSH-2.0-OpenSSH_9.6 "), 2000) // more than one record
	go func() { ic.Write(big) }()
	got := make([]byte, len(big))
	if _, err := io.ReadFull(rc, got); err != nil || !bytes.Equal(got, big) {
		t.Fatalf("initiator → responder: %v", err)
	}
	go func() { rc.Write([]byte("pong")) }()
	back := make([]byte, 4)
	if _, err := io.ReadFull(ic, back); err != nil || string(back) != "pong" {
		t.Fatalf("responder → initiator: %q %v", back, err)
	}
	w.mu.Lock()
	out := append([]byte(nil), w.out...)
	w.mu.Unlock()
	if bytes.Contains(out, []byte("SSH-2.0")) || bytes.Contains(out, []byte{10, 8, 0, 2}) || bytes.Contains(out, []byte("LFT")) {
		t.Fatal("plaintext on the wire")
	}
}

// TestP2PHelloLooksRandom: two hellos share no prefix and differ in length
// most of the time.
func TestP2PHelloLooksRandom(t *testing.T) {
	k := p2pKeys(t, 2)
	lens := map[int]bool{}
	var first [][]byte
	for i := 0; i < 8; i++ {
		_, _, _, _, w, err := p2pPair(t, k[0], k[1], k[0].PublicKey().Bytes())
		if err != nil {
			t.Fatal(err)
		}
		w.mu.Lock()
		lens[len(w.out)] = true
		first = append(first, append([]byte(nil), w.out[:4]...))
		w.mu.Unlock()
	}
	if len(lens) < 2 {
		t.Fatal("hello length is fixed")
	}
	for i := 1; i < len(first); i++ {
		if !bytes.Equal(first[i], first[0]) {
			return
		}
	}
	t.Fatal("hellos share a fixed prefix")
}

func TestP2PRejectsForeignKey(t *testing.T) {
	k := p2pKeys(t, 3) // alice, bob, eve
	// eve claims 10.8.0.2, whose key in bob's hints is alice's.
	if _, _, _, _, _, err := p2pPair(t, k[2], k[1], k[0].PublicKey().Bytes()); err == nil {
		t.Fatal("accepted eve")
	}
}

func TestP2PRejectsReplayAndOldHello(t *testing.T) {
	k := p2pKeys(t, 2)
	_, _, _, _, w, err := p2pPair(t, k[0], k[1], k[0].PublicKey().Bytes())
	if err != nil {
		t.Fatal(err)
	}
	w.mu.Lock()
	hello := append([]byte(nil), w.out...)
	w.mu.Unlock()
	pubOf := func(string) []byte { return k[0].PublicKey().Bytes() }
	c1, c2 := net.Pipe()
	defer c1.Close()
	go func() { c1.Write(hello); io.Copy(io.Discard, c1) }()
	if _, _, _, err := p2pAcceptAs(c2, k[1], pubOf); err == nil {
		t.Fatal("a recorded hello was accepted again")
	}
	c2.Close()

	// A correct hello made two minutes ago.
	e := p2pKeys(t, 1)[0]
	es, _ := e.ECDH(k[1].PublicKey())
	static, _ := k[0].ECDH(k[1].PublicKey())
	old := p2pHelloMsg(e.PublicKey().Bytes(), es, static, []byte{10, 8, 0, 2}, 22, uint64(time.Now().Add(-2*p2pSkew).Unix()), wire.InnerChaCha, nil)
	c3, c4 := net.Pipe()
	defer c3.Close()
	go func() { c3.Write(old); io.Copy(io.Discard, c3) }()
	if _, _, _, err := p2pAcceptAs(c4, k[1], pubOf); err == nil {
		t.Fatal("a hello from two minutes ago was accepted")
	}
	c4.Close()
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

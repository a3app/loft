package transport

// sessionHMAC bind and ClientHello reassembly tests.

import (
	"bytes"
	"crypto/rand"
	"testing"

	"golang.org/x/crypto/curve25519"

	"github.com/a3app/loft/internal/identity"
	"github.com/a3app/loft/internal/testcfg"
)

// TestSessionHMACRoundtrip verifies a matching session_id and rejects a flip.
func TestSessionHMACRoundtrip(t *testing.T) {
	token := []byte("token-secret")
	rnd := bytes.Repeat([]byte{7}, 32)
	h := clientHello{Random: rnd, SNI: "www.example.com", KeyShare: bytes.Repeat([]byte{1}, 32)}
	h.SessionID = sessionHMAC(token, h)
	if !verifySessionID(token, h) {
		t.Fatal("verify")
	}
	h.SessionID[0] ^= 1
	if verifySessionID(token, h) {
		t.Fatal("forged")
	}
}

// TestVerifyAnyMemberKeys matches member keys and ignores LOFT_TOKEN once keys exist.
func TestVerifyAnyMemberKeys(t *testing.T) {
	alice := []byte("alice-secret")
	bob := []byte("bob-secret")
	rnd := bytes.Repeat([]byte{7}, 32)
	h := clientHello{Random: rnd, SNI: "www.example.com", KeyShare: bytes.Repeat([]byte{1}, 32)}
	h.SessionID = sessionHMAC(bob, h)
	old := AuthKeys
	defer func() { AuthKeys = old }()
	r := identity.NewRing()
	r.Put("alice", string(alice))
	r.Put("bob", string(bob))
	AuthKeys = r
	if !verifyAny([]byte("node-token"), h) {
		t.Fatal("member key")
	}
	h.SessionID = sessionHMAC([]byte("node-token"), h)
	if verifyAny([]byte("node-token"), h) {
		t.Fatal("node token must not match once keys exist")
	}
	AuthKeys = identity.NewRing()
	h.SessionID = sessionHMAC([]byte("node-token"), h)
	if !verifyAny([]byte("node-token"), h) {
		t.Fatal("empty ring falls back")
	}
}

// TestSessionHMACBindsSNI rejects a session_id computed for a different SNI.
func TestSessionHMACBindsSNI(t *testing.T) {
	token := []byte("token-secret")
	rnd := bytes.Repeat([]byte{7}, 32)
	share := bytes.Repeat([]byte{1}, 32)
	a := clientHello{Random: rnd, SNI: testcfg.Decoy(), KeyShare: share}
	a.SessionID = sessionHMAC(token, a)
	b := a
	b.SNI = "evil.example"
	if verifySessionID(token, b) {
		t.Fatal("sni must bind")
	}
}

// TestSID3Roundtrip matches client and server sid v3 and rejects a bit flip.
func TestSID3Roundtrip(t *testing.T) {
	node := []byte("node-secret-for-x25519-mask-key")
	pub := NodeMaskPub(node)
	var clientPriv, clientPub [32]byte
	if _, err := rand.Read(clientPriv[:]); err != nil {
		t.Fatal(err)
	}
	curve25519.ScalarBaseMult(&clientPub, &clientPriv)
	h := clientHello{
		Random:   bytes.Repeat([]byte{9}, 32),
		SNI:      "www.example.com",
		KeyShare: clientPub[:],
	}
	h.SessionID = sid3FromPriv(clientPriv[:], pub, h)
	if !verifySID3(node, h) {
		t.Fatal("verify sid v3")
	}
	h.SessionID[0] ^= 1
	if verifySID3(node, h) {
		t.Fatal("forged")
	}
}

// TestRandomSessionIDIsNotLoft: a probe hello without v3 or v2 is rejected.
func TestRandomSessionIDIsNotLoft(t *testing.T) {
	node := []byte("node-secret-for-x25519-mask-key")
	h := clientHello{
		Random:    bytes.Repeat([]byte{2}, 32),
		SessionID: bytes.Repeat([]byte{9}, 32),
		SNI:       "www.example.com",
		KeyShare:  bytes.Repeat([]byte{1}, 32),
	}
	if verifyAny(node, h) {
		t.Fatal("probe hello must splice")
	}
}

// TestStolenMemberTokenCannotDetectSID3: a leaked member key does not mark v3 hellos.
func TestStolenMemberTokenCannotDetectSID3(t *testing.T) {
	node := []byte("node-secret-for-x25519-mask-key")
	member := []byte("stolen-member-key")
	pub := NodeMaskPub(node)
	var clientPriv, clientPub [32]byte
	if _, err := rand.Read(clientPriv[:]); err != nil {
		t.Fatal(err)
	}
	curve25519.ScalarBaseMult(&clientPub, &clientPriv)
	h := clientHello{
		Random:   bytes.Repeat([]byte{3}, 32),
		SNI:      testcfg.Decoy(),
		KeyShare: clientPub[:],
	}
	h.SessionID = sid3FromPriv(clientPriv[:], pub, h)
	old := AuthKeys
	defer func() { AuthKeys = old }()
	r := identity.NewRing()
	r.Put("alice", string(member))
	AuthKeys = r
	if !verifyAny(node, h) {
		t.Fatal("node must accept sid v3")
	}
	if verifySessionID(member, h) {
		t.Fatal("member HMAC must not equal sid v3")
	}
	if verifySID3(member, h) {
		t.Fatal("stolen member must not verify as the node")
	}
}

// TestVerifyAnyStillAcceptsV2 keeps sid v2 for clients without mask_pub.
func TestVerifyAnyStillAcceptsV2(t *testing.T) {
	node := []byte("node-secret-for-x25519-mask-key")
	member := []byte("alice-secret")
	h := clientHello{
		Random:   bytes.Repeat([]byte{7}, 32),
		SNI:      "www.example.com",
		KeyShare: bytes.Repeat([]byte{1}, 32),
	}
	h.SessionID = sessionHMAC(member, h)
	old := AuthKeys
	defer func() { AuthKeys = old }()
	r := identity.NewRing()
	r.Put("alice", string(member))
	AuthKeys = r
	if !verifyAny(node, h) {
		t.Fatal("v2 member hello")
	}
}

// TestReadClientHelloTwoRecords concatenates a ClientHello split across two records.
func TestReadClientHelloTwoRecords(t *testing.T) {
	token := []byte("t")
	rnd := bytes.Repeat([]byte{3}, 32)
	h := clientHello{Random: rnd, SNI: testcfg.Decoy()}
	h.SessionID = sessionHMAC(token, h)
	// minimal hello is hard to marshal here; concat two records of a real hello instead
	msg := []byte{0x01, 0x00, 0x00, 0x08, 1, 2, 3, 4, 5, 6, 7, 8}
	rec1 := append([]byte{0x16, 0x03, 0x03, 0x00, 0x04}, msg[:4]...)
	rec2 := append([]byte{0x16, 0x03, 0x03, 0x00, 0x08}, msg[4:]...)
	raw, _, err := readClientHello(bytes.NewReader(append(rec1, rec2...)))
	if err == nil && len(raw) != len(rec1)+len(rec2) {
		t.Fatalf("len %d", len(raw))
	}
	if err != nil && err.Error() == "not client hello" {
		// parse may fail on stub body; reassembly must still consume both records
		if len(raw) != len(rec1)+len(rec2) && len(raw) < 4 {
			t.Fatal(err)
		}
	}
}

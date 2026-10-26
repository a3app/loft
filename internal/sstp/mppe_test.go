package sstp

// MPPE stateless mode against the RFC 3078/3079 steps, the counter wrap,
// loss, and late packets.

import (
	"bytes"
	"crypto/rc4" // codeql[go/weak-cryptographic-algorithm] MPPE is RC4 (RFC 3078); the test spells it out
	"crypto/sha1"
	"testing"
)

var mppeStart = []byte("MPPE start key 1") // 16 bytes

// rfcRekey is one key update written out from RFC 3078 section 7.3 with the
// standard library: SHA-1 over start key, pad1, session key, pad2, then RC4
// of that digest keyed by itself.
func rfcRekey(t *testing.T, start, sess []byte, initial bool) []byte {
	t.Helper()
	h := sha1.New()
	h.Write(start)
	h.Write(make([]byte, 40))
	h.Write(sess)
	h.Write(bytes.Repeat([]byte{0xf2}, 40))
	k := h.Sum(nil)[:16]
	if initial {
		return k
	}
	c, err := rc4.NewCipher(k)
	if err != nil {
		t.Fatal(err)
	}
	out := make([]byte, 16)
	c.XORKeyStream(out, k)
	return out
}

// TestMPPEFirstPacketMatchesRFC: header 0x9000 | counter 0, and the payload
// is RC4 under the key after the initial and one regular update, over the
// PPP protocol field 0x0021 followed by the IP packet.
func TestMPPEFirstPacketMatchesRFC(t *testing.T) {
	ip := []byte{0x45, 0, 0, 20, 1, 2, 3, 4}
	got := newMPPE(mppeStart).crypt(ip)

	sess := rfcRekey(t, mppeStart, mppeStart, true)
	sess = rfcRekey(t, mppeStart, sess, false)
	c, _ := rc4.NewCipher(sess)
	want := []byte{0x90, 0x00, 0, 0}
	c.XORKeyStream(want[2:], []byte{0x00, 0x21})
	enc := make([]byte, len(ip))
	c.XORKeyStream(enc, ip)
	want = append(want[:4], enc...)
	if !bytes.Equal(got, want) {
		t.Fatalf("first packet\n got %x\nwant %x", got, want)
	}
}

func mppePacket(i int) []byte { return []byte{0x45, byte(i >> 8), byte(i), 0xaa} }

// TestMPPERoundTripAcrossWrap: 9000 packets cross the 4095→0 wrap twice and
// every one decrypts; the key chain keeps going rather than starting over.
func TestMPPERoundTripAcrossWrap(t *testing.T) {
	tx, rx := newMPPE(mppeStart), newMPPE(mppeStart)
	for i := 0; i < 9000; i++ {
		got, err := rx.decrypt(tx.crypt(mppePacket(i)))
		if err != nil || !bytes.Equal(got, mppePacket(i)) {
			t.Fatalf("packet %d: %x %v", i, got, err)
		}
	}
}

// TestMPPELossAndLatePacket: a gap of lost packets is skipped over; a late
// packet is dropped and does not break the packets after it. The old code
// restarted the chain from the start key here, which is wrong once the
// counter has wrapped.
func TestMPPELossAndLatePacket(t *testing.T) {
	tx, rx := newMPPE(mppeStart), newMPPE(mppeStart)
	var sent [][]byte
	for i := 0; i < 5000; i++ {
		sent = append(sent, tx.crypt(mppePacket(i)))
	}
	for i := 0; i < 4500; i++ { // past one wrap
		if _, err := rx.decrypt(sent[i]); err != nil {
			t.Fatalf("packet %d: %v", i, err)
		}
	}
	if got, err := rx.decrypt(sent[4700]); err != nil || !bytes.Equal(got, mppePacket(4700)) {
		t.Fatalf("after loss: %x %v", got, err)
	}
	if _, err := rx.decrypt(sent[4600]); err == nil {
		t.Fatal("late packet accepted")
	}
	for i := 4701; i < 5000; i++ {
		got, err := rx.decrypt(sent[i])
		if err != nil || !bytes.Equal(got, mppePacket(i)) {
			t.Fatalf("packet %d after a late one: %x %v", i, got, err)
		}
	}
}

// TestMPPERejectsUnencryptedHeader: a header without the encrypted bit is
// not a stateless MPPE packet.
func TestMPPERejectsUnencryptedHeader(t *testing.T) {
	p := newMPPE(mppeStart).crypt(mppePacket(1))
	p[0] &^= 0x10
	if _, err := newMPPE(mppeStart).decrypt(p); err == nil {
		t.Fatal("packet without the encrypted bit accepted")
	}
}

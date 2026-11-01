package sstp

// MPPE (RFC 3078/3079) 128-bit stateless, as Linux ppp_mppe / Windows SSTP.

import (
	"crypto/sha1"
	"encoding/binary"
	"fmt"
)

// rc4cipher is plain RC4, the stream cipher MPPE encrypts with.
type rc4cipher struct {
	s    [256]byte
	i, j byte
}

func newRC4(key []byte) *rc4cipher {
	c := &rc4cipher{}
	for i := 0; i < 256; i++ {
		c.s[i] = byte(i)
	}
	var j byte
	for i := 0; i < 256; i++ {
		j += c.s[i] + key[i%len(key)]
		c.s[i], c.s[j] = c.s[j], c.s[i]
	}
	return c
}

func (c *rc4cipher) xor(dst, src []byte) {
	for n := range src {
		c.i++
		c.j += c.s[c.i]
		c.s[c.i], c.s[c.j] = c.s[c.j], c.s[c.i]
		dst[n] = src[n] ^ c.s[c.s[c.i]+c.s[c.j]]
	}
}

// MPPE header bits (RFC 3078). In stateless mode every packet is "flushed"
// (a fresh key) and "encrypted"; the low 12 bits are the coherency count.
const (
	mppeFlushed   = 0x8000
	mppeEncrypted = 0x1000
	mppeCount     = 0x0fff
)

// mppe is one direction of MPPE. In stateless mode every packet carries a
// 12-bit counter and the session key changes for every counter value, so a
// lost packet does not break the following ones. The key chain never starts
// over: after the counter wraps from 4095 to 0 it simply keeps going.
type mppe struct {
	master []byte
	sess   []byte
	count  uint16
}

// newMPPE starts one direction from its start key (RFC 3079). The initial
// session key is one key update without the RC4 step, and the counter is set
// to 4095 so the first packet, counter 0, is one update on.
func newMPPE(master []byte) *mppe {
	m := &mppe{master: append([]byte(nil), master...), count: mppeCount}
	m.sess = append([]byte(nil), m.master...)
	m.rekey(true)
	return m
}

// getNewKeyFromSHA is the first half of the MPPE key update: SHA-1 of the
// start key, padding, the current session key and other padding, cut to
// 128 bits.
func getNewKeyFromSHA(master, sess []byte) []byte {
	pad1 := make([]byte, 40)
	pad2 := bytesOf(0xf2, 40)
	s := sha1.New()
	s.Write(master)
	s.Write(pad1)
	s.Write(sess)
	s.Write(pad2)
	return s.Sum(nil)[:16]
}

// rekey is the MPPE key update (RFC 3078 section 7.3, as Linux ppp_mppe):
// the SHA-1 result is then encrypted with RC4 keyed by itself. Only the very
// first key skips the RC4 step.
func (m *mppe) rekey(initial bool) {
	k := getNewKeyFromSHA(m.master, m.sess)
	if initial {
		m.sess = k
		return
	}
	next := make([]byte, len(k))
	newRC4(k).xor(next, k)
	m.sess = next
}

// crypt encrypts one IP packet with the next counter. What is encrypted is
// the PPP protocol field (IPv4, 0x0021) followed by the packet; the 2-byte
// header in front carries the flushed and encrypted bits and the counter.
func (m *mppe) crypt(ip []byte) []byte {
	m.count = (m.count + 1) & mppeCount
	m.rekey(false)
	plain := make([]byte, 2+len(ip))
	binary.BigEndian.PutUint16(plain, pppIP)
	copy(plain[2:], ip)
	out := make([]byte, 2+len(plain))
	binary.BigEndian.PutUint16(out[:2], mppeFlushed|mppeEncrypted|m.count)
	newRC4(m.sess).xor(out[2:], plain)
	return out
}

// decrypt opens one packet and returns the IP packet inside. The key moves
// forward one update per counter step, also across the 4095 to 0 wrap. A
// counter more than half the space behind is a late (reordered) packet: it
// is dropped and the key chain is left as it is, otherwise every packet
// after it would be decrypted with the wrong key.
func (m *mppe) decrypt(p []byte) ([]byte, error) {
	if len(p) < 4 {
		return nil, fmt.Errorf("mppe short")
	}
	hdr := binary.BigEndian.Uint16(p[:2])
	if hdr&mppeEncrypted == 0 || hdr&mppeFlushed == 0 {
		return nil, fmt.Errorf("mppe: not a stateless encrypted packet")
	}
	c := hdr & mppeCount
	delta := (int(c) - int(m.count)) & mppeCount
	if delta > (mppeCount+1)/2 {
		return nil, fmt.Errorf("mppe: late packet")
	}
	for ; delta > 0; delta-- {
		m.count = (m.count + 1) & mppeCount
		m.rekey(false)
	}
	plain := make([]byte, len(p)-2)
	newRC4(m.sess).xor(plain, p[2:])
	// The protocol field may be compressed to one byte (odd first byte).
	var proto uint16
	if plain[0]&1 == 1 {
		proto, plain = uint16(plain[0]), plain[1:]
	} else {
		proto, plain = binary.BigEndian.Uint16(plain), plain[2:]
	}
	if proto != pppIP {
		return nil, fmt.Errorf("mppe: protocol %#04x inside", proto)
	}
	return plain, nil
}

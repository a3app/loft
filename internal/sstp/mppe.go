package sstp

// MPPE (RFC 3078/3079) 128-bit stateless, as Linux ppp_mppe / Windows SSTP.

import (
	"crypto/sha1"
	"encoding/binary"
	"fmt"
)

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

type mppe struct {
	master []byte
	sess   []byte
	count  uint16
	inited bool
}

func newMPPE(master []byte) *mppe {
	m := &mppe{master: append([]byte(nil), master...), count: 0x0fff}
	m.resetSess()
	return m
}

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

func (m *mppe) resetSess() {
	m.sess = append([]byte(nil), m.master...)
	m.sess = getNewKeyFromSHA(m.master, m.sess)
	m.count = 0x0fff
	m.inited = true
}

func (m *mppe) rekey() {
	m.sess = getNewKeyFromSHA(m.master, m.sess)
}

func (m *mppe) sync(c uint16) {
	if !m.inited {
		m.resetSess()
	}
	delta := (int(c) - int(m.count)) & 0x0fff
	if delta == 0 {
		return
	}
	if delta > 2048 {
		m.resetSess()
		delta = (int(c) - int(m.count)) & 0x0fff
	}
	for i := 0; i < delta; i++ {
		m.count = (m.count + 1) & 0x0fff
		m.rekey()
	}
}

func (m *mppe) crypt(p []byte) []byte {
	m.sync((m.count + 1) & 0x0fff)
	out := make([]byte, 2+len(p))
	hdr := uint16(0x8000) | (m.count & 0x0fff)
	binary.BigEndian.PutUint16(out[:2], hdr)
	newRC4(m.sess).xor(out[2:], p)
	return out
}

func (m *mppe) decrypt(p []byte) ([]byte, error) {
	if len(p) < 2 {
		return nil, fmt.Errorf("mppe short")
	}
	c := binary.BigEndian.Uint16(p[:2]) & 0x0fff
	m.sync(c)
	out := make([]byte, len(p)-2)
	newRC4(m.sess).xor(out, p[2:])
	return out, nil
}

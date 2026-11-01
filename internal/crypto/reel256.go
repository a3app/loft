//go:build reel256 || (!ios && !android && !darwin)

package crypto

// Reel-256 (andhash/reel256, draft-00). Experimental.
// Loft record is still nonce[12]||ct||tag[16]; Init nonce is 4 zero bytes || loft nonce.
// After 32 Init steps every state word is XORed with the key (even k0, odd k1).

import (
	"crypto/subtle"
	"encoding/binary"

	"github.com/a3app/loft/internal/wire"
)

const reelBuilt = true

var (
	reelEncBlocksAsm func(s *blk, off *int, in, out *byte, n uint64)
	reelDecBlocksAsm func(s *blk, off *int, in, out *byte, n uint64)
)

type reelRing struct {
	s   [16]blk
	off int
}

func (r *reelRing) get(i int) blk    { return r.s[(r.off+i)&15] }
func (r *reelRing) set(i int, v blk) { r.s[(r.off+i)&15] = v }
func (r *reelRing) rot()             { r.off = (r.off + 1) & 15 }

func (r *reelRing) step(m blk) blk {
	t := xorBlk(aesl(xorBlk(r.get(0), r.get(3))), m)
	c := xorBlk(t, r.get(8))
	next := xorBlk(t, aesl(xorBlk(r.get(12), r.get(9))))
	r.set(8, xorBlk(r.get(8), m))
	r.rot()
	r.set(15, next)
	return c
}

func (r *reelRing) encBlocks(in, out []byte) int {
	n := len(in) / 16
	if n > len(out)/16 {
		n = len(out) / 16
	}
	if n == 0 {
		return 0
	}
	if reelEncBlocksAsm != nil {
		reelEncBlocksAsm(&r.s[0], &r.off, &in[0], &out[0], uint64(n))
		return n * 16
	}
	i := 0
	for ; i < n*16; i += 16 {
		var x blk
		copy(x[:], in[i:i+16])
		c := r.step(x)
		copy(out[i:i+16], c[:])
	}
	return i
}

func (r *reelRing) decBlocks(in, out []byte) int {
	n := len(in) / 16
	if n > len(out)/16 {
		n = len(out) / 16
	}
	if n == 0 {
		return 0
	}
	if reelDecBlocksAsm != nil {
		reelDecBlocksAsm(&r.s[0], &r.off, &in[0], &out[0], uint64(n))
		return n * 16
	}
	i := 0
	for ; i < n*16; i += 16 {
		ks := r.keystream()
		var m blk
		for j := 0; j < 16; j++ {
			m[j] = in[i+j] ^ ks[j]
		}
		r.step(m)
		copy(out[i:i+16], m[:])
	}
	return i
}

func (r *reelRing) keystream() blk {
	return xorBlk(aesl(xorBlk(r.get(0), r.get(3))), r.get(8))
}

func (r *reelRing) diffuse(x0, x1 blk) {
	for i := 0; i < 16; i++ {
		r.step(x0)
		r.step(x1)
	}
}

func (r *reelRing) init(key, nonce []byte) {
	var k0, k1, n blk
	copy(k0[:], key[:16])
	copy(k1[:], key[16:])
	copy(n[:], nonce)
	z := blk{}
	c0, c1 := piC0, piC1
	r.s = [16]blk{c0, k0, c0, n, z, k0, z, c1, k1, z, xorBlk(n, k1), c0, c1, k1, z, xorBlk(c0, c1)}
	r.off = 0
	r.diffuse(k0, k1)
	for i := 0; i < 16; i++ {
		if i%2 == 0 {
			r.set(i, xorBlk(r.get(i), k0))
		} else {
			r.set(i, xorBlk(r.get(i), k1))
		}
	}
}

func (r *reelRing) absorb(ad []byte) {
	for len(ad) >= 16 {
		var x blk
		copy(x[:], ad[:16])
		r.step(x)
		ad = ad[16:]
	}
	if len(ad) > 0 {
		var x blk
		copy(x[:], ad)
		r.step(x)
	}
}

func (r *reelRing) tag(adLen, msgLen int) blk {
	var t blk
	binary.LittleEndian.PutUint64(t[0:8], uint64(adLen)*8)
	binary.LittleEndian.PutUint64(t[8:16], uint64(msgLen)*8)
	r.diffuse(t, t)
	acc := r.get(0)
	for i := 1; i < 16; i++ {
		acc = xorBlk(acc, r.get(i))
	}
	return acc
}

func reelEncrypt(key, nonce, msg, ad []byte) []byte {
	var r reelRing
	r.init(key, nonce)
	r.absorb(ad)
	ct := make([]byte, len(msg)+16)
	i := r.encBlocks(msg, ct)
	if rest := len(msg) - i; rest > 0 {
		var x blk
		copy(x[:], msg[i:])
		c := r.step(x)
		copy(ct[i:i+rest], c[:rest])
	}
	tag := r.tag(len(ad), len(msg))
	copy(ct[len(msg):], tag[:])
	return ct
}

func reelDecrypt(key, nonce, blob, ad []byte) ([]byte, error) {
	if len(blob) < 16 {
		return nil, ErrAuth
	}
	ct, want := blob[:len(blob)-16], blob[len(blob)-16:]
	var r reelRing
	r.init(key, nonce)
	r.absorb(ad)
	msg := make([]byte, len(ct))
	i := r.decBlocks(ct, msg)
	if rest := len(ct) - i; rest > 0 {
		ks := r.keystream()
		var m blk
		for j := 0; j < rest; j++ {
			m[j] = ct[i+j] ^ ks[j]
		}
		r.step(m)
		copy(msg[i:i+rest], m[:rest])
	}
	got := r.tag(len(ad), len(ct))
	if subtle.ConstantTimeCompare(got[:], want) != 1 {
		return nil, ErrAuth
	}
	return msg, nil
}

func sealReel(key, plaintext, aad []byte) ([]byte, error) {
	n12, err := nextNonce(key)
	if err != nil {
		return nil, err
	}
	body := reelEncrypt(key, aeadNonce16(n12), plaintext, aad)
	out := make([]byte, 0, NonceSize+len(body))
	out = append(out, n12...)
	out = append(out, body...)
	return out, nil
}

func openReel(key, blob, aad []byte) ([]byte, error) {
	if len(blob) < NonceSize+16 {
		return nil, ErrAuth
	}
	return reelDecrypt(key, aeadNonce16(blob[:NonceSize]), blob[NonceSize:], aad)
}

func bindReel(key []byte) {
	innerModes.Store(string(key), wire.InnerReel)
}

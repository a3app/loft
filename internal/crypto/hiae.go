//go:build hiae || (!ios && !android && !darwin)

package crypto

// HiAE (draft-pham-cfrg-hiae-06). CGO-free AESL = MC·SR·SB.
// Loft record is still nonce[12]||ct||tag[16]; Init nonce is 4 zero bytes || loft nonce.

import (
	"crypto/subtle"
	"encoding/binary"

	"github.com/a3app/loft/internal/wire"
)

const hiaeBuilt = true

// hiaeEncBlocksAsm / hiaeDecBlocksAsm are the AES-NI / AESE ring loops.
// nil means the Go update/updateDec path (no AES instructions, or purego).
var (
	hiaeEncBlocksAsm func(s *blk, off *int, in, out *byte, n uint64)
	hiaeDecBlocksAsm func(s *blk, off *int, in, out *byte, n uint64)
)

type hiaeRing struct {
	s   [16]blk
	off int
}

func (r *hiaeRing) get(i int) blk    { return r.s[(r.off+i)&15] }
func (r *hiaeRing) set(i int, v blk) { r.s[(r.off+i)&15] = v }
func (r *hiaeRing) step()            { r.off = (r.off + 1) & 15 }

func (r *hiaeRing) update(x blk) blk {
	t := xorBlk(aesl(xorBlk(r.get(0), r.get(1))), x)
	c := xorBlk(t, r.get(9))
	r.set(0, xorBlk(aesl(r.get(13)), t))
	r.set(3, xorBlk(r.get(3), x))
	r.set(13, xorBlk(r.get(13), x))
	r.step()
	return c
}

func (r *hiaeRing) updateDec(c blk) blk {
	t := xorBlk(c, r.get(9))
	x := xorBlk(aesl(xorBlk(r.get(0), r.get(1))), t)
	r.set(0, xorBlk(aesl(r.get(13)), t))
	r.set(3, xorBlk(r.get(3), x))
	r.set(13, xorBlk(r.get(13), x))
	r.step()
	return x
}

func (r *hiaeRing) encBlocks(in, out []byte) int {
	n := len(in) / 16
	if n > len(out)/16 {
		n = len(out) / 16
	}
	if n == 0 {
		return 0
	}
	if hiaeEncBlocksAsm != nil {
		hiaeEncBlocksAsm(&r.s[0], &r.off, &in[0], &out[0], uint64(n))
		return n * 16
	}
	i := 0
	for ; i < n*16; i += 16 {
		var x blk
		copy(x[:], in[i:i+16])
		c := r.update(x)
		copy(out[i:i+16], c[:])
	}
	return i
}

func (r *hiaeRing) decBlocks(in, out []byte) int {
	n := len(in) / 16
	if n > len(out)/16 {
		n = len(out) / 16
	}
	if n == 0 {
		return 0
	}
	if hiaeDecBlocksAsm != nil {
		hiaeDecBlocksAsm(&r.s[0], &r.off, &in[0], &out[0], uint64(n))
		return n * 16
	}
	i := 0
	for ; i < n*16; i += 16 {
		var c blk
		copy(c[:], in[i:i+16])
		x := r.updateDec(c)
		copy(out[i:i+16], x[:])
	}
	return i
}

func (r *hiaeRing) keystream() blk {
	return xorBlk(aesl(xorBlk(r.get(0), r.get(1))), r.get(9))
}

func (r *hiaeRing) diffuse(x0, x1 blk) {
	for i := 0; i < 16; i++ {
		r.update(x0)
		r.update(x1)
	}
}

func (r *hiaeRing) init(key, nonce []byte) {
	var k0, k1, n blk
	copy(k0[:], key[:16])
	copy(k1[:], key[16:])
	copy(n[:], nonce)
	z := blk{}
	c0, c1 := piC0, piC1
	r.s = [16]blk{c0, k0, c0, n, z, k0, z, c1, k1, z, xorBlk(n, k1), c0, c1, k1, z, xorBlk(c0, c1)}
	r.off = 0
	r.diffuse(k0, k1)
}

func (r *hiaeRing) absorb(ad []byte) {
	for len(ad) >= 16 {
		var x blk
		copy(x[:], ad[:16])
		r.update(x)
		ad = ad[16:]
	}
	if len(ad) > 0 {
		var x blk
		copy(x[:], ad)
		r.update(x)
	}
}

func (r *hiaeRing) tag(adLen, msgLen int) blk {
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

func hiaeEncrypt(key, nonce, msg, ad []byte) []byte {
	ct := make([]byte, len(msg)+16)
	hiaeEncryptTo(ct, key, nonce, msg, ad)
	return ct
}

// hiaeEncryptTo writes ct||tag[16] into out. len(out) is len(msg)+16.
func hiaeEncryptTo(out, key, nonce, msg, ad []byte) {
	var r hiaeRing
	r.init(key, nonce)
	r.absorb(ad)
	i := r.encBlocks(msg, out)
	if rest := len(msg) - i; rest > 0 {
		var x blk
		copy(x[:], msg[i:])
		c := r.update(x)
		copy(out[i:i+rest], c[:rest])
	}
	tag := r.tag(len(ad), len(msg))
	copy(out[len(msg):], tag[:])
}

func hiaeDecrypt(key, nonce, blob, ad []byte) ([]byte, error) {
	if len(blob) < 16 {
		return nil, ErrAuth
	}
	ct, want := blob[:len(blob)-16], blob[len(blob)-16:]
	var r hiaeRing
	r.init(key, nonce)
	r.absorb(ad)
	msg := make([]byte, len(ct))
	i := r.decBlocks(ct, msg)
	if rest := len(ct) - i; rest > 0 {
		ks := r.keystream()
		var in blk
		copy(in[:], ct[i:i+rest])
		copy(in[rest:], ks[rest:])
		x := r.updateDec(in)
		copy(msg[i:i+rest], x[:rest])
	}
	got := r.tag(len(ad), len(ct))
	if subtle.ConstantTimeCompare(got[:], want) != 1 {
		return nil, ErrAuth
	}
	return msg, nil
}

func sealHiAE(key, plaintext, aad []byte) ([]byte, error) {
	return sealHiAEInto(nil, key, plaintext, aad)
}

// sealHiAEInto appends loft nonce[12]||HiAE(ct||tag) to dst.
func sealHiAEInto(dst, key, plaintext, aad []byte) ([]byte, error) {
	var n12 [NonceSize]byte
	if err := putNonce(key, n12[:]); err != nil {
		return nil, err
	}
	off := len(dst)
	need := off + NonceSize + len(plaintext) + AuthSize
	dst = growSlice(dst, need)
	copy(dst[off:off+NonceSize], n12[:])
	n16 := nonce16(n12[:])
	hiaeEncryptTo(dst[off+NonceSize:need], key, n16[:], plaintext, aad)
	return dst[:need], nil
}

func openHiAE(key, blob, aad []byte) ([]byte, error) {
	if len(blob) < NonceSize+16 {
		return nil, ErrAuth
	}
	n16 := nonce16(blob[:NonceSize])
	return hiaeDecrypt(key, n16[:], blob[NonceSize:], aad)
}

func bindHiAE(key []byte) {
	innerModes.Store(string(key), wire.InnerHiAE)
}

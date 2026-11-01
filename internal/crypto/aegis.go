package crypto

// AEGIS-128L (RFC 10032). CGO-free AESRound = AESL XOR rk.
// Data key is HKDF-SHA256(ikm=session, info="loft-v1-aegis128l", L=16).
// Loft record stays nonce[12]||ct||tag[16]; Init nonce is 4 zero bytes || loft nonce.

import (
	"crypto/sha256"
	"crypto/subtle"
	"encoding/binary"
	"io"

	"github.com/a3app/loft/internal/wire"
	"golang.org/x/crypto/hkdf"
)

const aegisInfo = "loft-v1-aegis128l"

var (
	aegisC0 = blk{0x00, 0x01, 0x01, 0x02, 0x03, 0x05, 0x08, 0x0d, 0x15, 0x22, 0x37, 0x59, 0x90, 0xe9, 0x79, 0x62}
	aegisC1 = blk{0xdb, 0x3d, 0x18, 0x55, 0x6d, 0xc2, 0x2f, 0xf1, 0x20, 0x11, 0x31, 0x42, 0x73, 0xb5, 0x28, 0xdd}
)

func aesRound(in, rk blk) blk { return xorBlk(aesl(in), rk) }

func andBlk(a, b blk) blk {
	var c blk
	for i := 0; i < 16; i++ {
		c[i] = a[i] & b[i]
	}
	return c
}

func loadBlk(p []byte) blk {
	var x blk
	copy(x[:], p)
	return x
}

type aegis128L struct {
	s [8]blk
}

// aegisEncBlocksAsm / aegisDecBlocksAsm are the AES-NI / AESE 32-byte loops.
// n is 32-byte units. nil means the Go enc32/dec32 path (no AES, or purego).
var (
	aegisEncBlocksAsm func(s *blk, in, out *byte, n uint64)
	aegisDecBlocksAsm func(s *blk, in, out *byte, n uint64)
)

func (a *aegis128L) update(m0, m1 blk) {
	s0, s1, s2, s3, s4, s5, s6, s7 := a.s[0], a.s[1], a.s[2], a.s[3], a.s[4], a.s[5], a.s[6], a.s[7]
	a.s[0] = aesRound(s7, xorBlk(s0, m0))
	a.s[1] = aesRound(s0, s1)
	a.s[2] = aesRound(s1, s2)
	a.s[3] = aesRound(s2, s3)
	a.s[4] = aesRound(s3, xorBlk(s4, m1))
	a.s[5] = aesRound(s4, s5)
	a.s[6] = aesRound(s5, s6)
	a.s[7] = aesRound(s6, s7)
}

func (a *aegis128L) init(key, nonce []byte) {
	var k, n blk
	copy(k[:], key)
	copy(n[:], nonce)
	kn := xorBlk(k, n)
	a.s = [8]blk{
		kn, aegisC1, aegisC0, aegisC1,
		kn, xorBlk(k, aegisC0), xorBlk(k, aegisC1), xorBlk(k, aegisC0),
	}
	for i := 0; i < 10; i++ {
		a.update(n, k)
	}
}

func (a *aegis128L) absorb(ad []byte) {
	for len(ad) >= 32 {
		a.update(loadBlk(ad), loadBlk(ad[16:]))
		ad = ad[32:]
	}
	if len(ad) > 0 {
		var pad [32]byte
		copy(pad[:], ad)
		a.update(loadBlk(pad[:]), loadBlk(pad[16:]))
	}
}

func (a *aegis128L) stream() (z0, z1 blk) {
	z0 = xorBlk(xorBlk(a.s[1], a.s[6]), andBlk(a.s[2], a.s[3]))
	z1 = xorBlk(xorBlk(a.s[2], a.s[5]), andBlk(a.s[6], a.s[7]))
	return z0, z1
}

func (a *aegis128L) enc32(xi, out []byte) {
	t0, t1 := loadBlk(xi), loadBlk(xi[16:])
	z0, z1 := a.stream()
	o0, o1 := xorBlk(t0, z0), xorBlk(t1, z1)
	copy(out[:16], o0[:])
	copy(out[16:32], o1[:])
	a.update(t0, t1)
}

func (a *aegis128L) dec32(ci, out []byte) {
	t0, t1 := loadBlk(ci), loadBlk(ci[16:])
	z0, z1 := a.stream()
	o0, o1 := xorBlk(t0, z0), xorBlk(t1, z1)
	copy(out[:16], o0[:])
	copy(out[16:32], o1[:])
	a.update(o0, o1)
}

func (a *aegis128L) enc(msg, out []byte) {
	n := len(msg) / 32
	if n > len(out)/32 {
		n = len(out) / 32
	}
	i := 0
	if n > 0 && aegisEncBlocksAsm != nil {
		aegisEncBlocksAsm(&a.s[0], &msg[0], &out[0], uint64(n))
		i = n * 32
	} else {
		for len(msg)-i >= 32 {
			a.enc32(msg[i:i+32], out[i:i+32])
			i += 32
		}
	}
	if rest := len(msg) - i; rest > 0 {
		var pad [32]byte
		copy(pad[:], msg[i:])
		var c [32]byte
		a.enc32(pad[:], c[:])
		copy(out[i:], c[:rest])
	}
}

func (a *aegis128L) dec(ct, out []byte) {
	n := len(ct) / 32
	if n > len(out)/32 {
		n = len(out) / 32
	}
	i := 0
	if n > 0 && aegisDecBlocksAsm != nil {
		aegisDecBlocksAsm(&a.s[0], &ct[0], &out[0], uint64(n))
		i = n * 32
	} else {
		for len(ct)-i >= 32 {
			a.dec32(ct[i:i+32], out[i:i+32])
			i += 32
		}
	}
	if rest := len(ct) - i; rest > 0 {
		var pad [32]byte
		copy(pad[:], ct[i:])
		z0, z1 := a.stream()
		o0, o1 := xorBlk(loadBlk(pad[:]), z0), xorBlk(loadBlk(pad[16:]), z1)
		var xn [32]byte
		copy(xn[:16], o0[:])
		copy(xn[16:], o1[:])
		copy(out[i:], xn[:rest])
		var v [32]byte
		copy(v[:], xn[:rest])
		a.update(loadBlk(v[:]), loadBlk(v[16:]))
	}
}

func (a *aegis128L) tag128(adLen, msgLen int) blk {
	var t blk
	binary.LittleEndian.PutUint64(t[0:8], uint64(adLen)*8)
	binary.LittleEndian.PutUint64(t[8:16], uint64(msgLen)*8)
	t = xorBlk(a.s[2], t)
	for i := 0; i < 7; i++ {
		a.update(t, t)
	}
	acc := a.s[0]
	for i := 1; i < 7; i++ {
		acc = xorBlk(acc, a.s[i])
	}
	return acc
}

func aegis128lEncrypt(key, nonce, msg, ad []byte) []byte {
	out := make([]byte, len(msg)+16)
	aegis128lEncryptTo(out, key, nonce, msg, ad)
	return out
}

// aegis128lEncryptTo writes ct||tag[16] into out. len(out) is len(msg)+16.
func aegis128lEncryptTo(out, key, nonce, msg, ad []byte) {
	var a aegis128L
	a.init(key, nonce)
	a.absorb(ad)
	a.enc(msg, out)
	tag := a.tag128(len(ad), len(msg))
	copy(out[len(msg):], tag[:])
}

func aegis128lDecrypt(key, nonce, blob, ad []byte) ([]byte, error) {
	if len(blob) < 16 {
		return nil, ErrAuth
	}
	ct, want := blob[:len(blob)-16], blob[len(blob)-16:]
	var a aegis128L
	a.init(key, nonce)
	a.absorb(ad)
	msg := make([]byte, len(ct))
	a.dec(ct, msg)
	got := a.tag128(len(ad), len(ct))
	if subtle.ConstantTimeCompare(got[:], want) != 1 {
		for i := range msg {
			msg[i] = 0
		}
		return nil, ErrAuth
	}
	return msg, nil
}

func deriveAEGIS128L(session []byte) []byte {
	k := string(session)
	if v, ok := aegisKeys.Load(k); ok {
		return v.([]byte)
	}
	r := hkdf.New(sha256.New, session, nil, []byte(aegisInfo))
	out := make([]byte, 16)
	if _, err := io.ReadFull(r, out); err != nil {
		return nil
	}
	actual, _ := aegisKeys.LoadOrStore(k, out)
	return actual.([]byte)
}

func sealAEGIS(key, plaintext, aad []byte) ([]byte, error) {
	return sealAEGISInto(nil, key, plaintext, aad)
}

// sealAEGISInto appends loft nonce[12]||AEGIS-128L(ct||tag) to dst.
func sealAEGISInto(dst, key, plaintext, aad []byte) ([]byte, error) {
	k16 := deriveAEGIS128L(key)
	if len(k16) != 16 {
		return nil, ErrAuth
	}
	var n12 [NonceSize]byte
	if err := putNonce(key, n12[:]); err != nil {
		return nil, err
	}
	off := len(dst)
	need := off + NonceSize + len(plaintext) + AuthSize
	dst = growSlice(dst, need)
	copy(dst[off:off+NonceSize], n12[:])
	n16 := nonce16(n12[:])
	aegis128lEncryptTo(dst[off+NonceSize:need], k16, n16[:], plaintext, aad)
	return dst[:need], nil
}

func openAEGIS(key, blob, aad []byte) ([]byte, error) {
	if len(blob) < NonceSize+16 {
		return nil, ErrAuth
	}
	k16 := deriveAEGIS128L(key)
	if len(k16) != 16 {
		return nil, ErrAuth
	}
	n16 := nonce16(blob[:NonceSize])
	return aegis128lDecrypt(k16, n16[:], blob[NonceSize:], aad)
}

func bindAEGIS(key []byte) {
	innerModes.Store(string(key), wire.InnerAEGIS)
}

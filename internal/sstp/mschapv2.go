package sstp

// MS-CHAPv2 (RFC 2759) and MPPE master keys (RFC 3079) for SSTP HLAK.

import (
	"crypto/des"
	"crypto/sha1"
	"encoding/binary"
	"strings"
	"unicode/utf16"

	"golang.org/x/crypto/md4"
)

func ntPasswordHash(pass string) []byte {
	u := utf16.Encode([]rune(pass))
	raw := make([]byte, len(u)*2)
	for i, r := range u {
		binary.LittleEndian.PutUint16(raw[i*2:], r)
	}
	// NT hash is MD4 (RFC 2759). codeql[go/weak-sensitive-data-hashing]
	h := md4.New()
	h.Write(raw)
	return h.Sum(nil)
}

func md4sum(b []byte) []byte {
	// codeql[go/weak-sensitive-data-hashing]
	h := md4.New()
	h.Write(b)
	return h.Sum(nil)
}

func challengeHash(peerChal, authChal []byte, user string) []byte {
	user = samUser(user)
	s := sha1.New()
	s.Write(peerChal)
	s.Write(authChal)
	s.Write([]byte(user))
	return s.Sum(nil)[:8]
}

func samUser(user string) string {
	if i := strings.LastIndexAny(user, `/\`); i >= 0 {
		return user[i+1:]
	}
	return user
}

func desHash(key7, chal []byte) []byte {
	// MS-CHAPv2 challenge DES (RFC 2759). codeql[go/weak-cryptographic-algorithm]
	block, err := des.NewCipher(desKey(key7))
	if err != nil {
		return make([]byte, 8)
	}
	out := make([]byte, 8)
	// codeql[go/weak-cryptographic-algorithm]
	block.Encrypt(out, chal)
	return out
}

func desKey(k7 []byte) []byte {
	in := make([]byte, 8)
	copy(in, k7)
	get := func(start int) byte {
		word := uint(in[start/8]) << 8
		word |= uint(in[start/8+1])
		word >>= uint(15 - (start%8 + 7))
		return byte(word) & 0xfe
	}
	return []byte{get(0), get(7), get(14), get(21), get(28), get(35), get(42), get(49)}
}

func ntChallengeResponse(chal, passHash []byte) []byte {
	z := make([]byte, 21)
	copy(z, passHash)
	var out []byte
	out = append(out, desHash(z[0:7], chal)...)
	out = append(out, desHash(z[7:14], chal)...)
	out = append(out, desHash(z[14:21], chal)...)
	return out
}

func generateNTResponse(authChal, peerChal []byte, user, pass string) []byte {
	ch := challengeHash(peerChal, authChal, user)
	return ntChallengeResponse(ch, ntPasswordHash(pass))
}

func authenticatorResponse(pass string, ntResp, peerChal, authChal []byte, user string) string {
	passHashHash := md4sum(ntPasswordHash(pass))
	magic1 := []byte("Magic server to client signing constant")
	s := sha1.New()
	s.Write(passHashHash)
	s.Write(ntResp)
	s.Write(magic1)
	digest := s.Sum(nil)
	ch := challengeHash(peerChal, authChal, user)
	magic2 := []byte("Pad to make it do more than one iteration")
	s = sha1.New()
	s.Write(digest)
	s.Write(ch)
	s.Write(magic2)
	sum := s.Sum(nil)
	hex := make([]byte, 40)
	const digits = "0123456789ABCDEF"
	for i, b := range sum {
		hex[i*2] = digits[b>>4]
		hex[i*2+1] = digits[b&0xf]
	}
	return "S=" + string(hex)
}

// mppeMasterKeys is client MasterSendKey | MasterReceiveKey (HLAK).
func mppeMasterKeys(pass string, ntResp []byte) (send, recv []byte) {
	phh := md4sum(ntPasswordHash(pass))
	magic := []byte("This is the MPPE Master Key")
	s := sha1.New()
	s.Write(phh)
	s.Write(ntResp)
	s.Write(magic)
	master := s.Sum(nil)[:16]
	send = asymmetricKey(master, true)
	recv = asymmetricKey(master, false)
	return send, recv
}

func asymmetricKey(master []byte, send bool) []byte {
	var magic string
	if send {
		magic = "On the client side, this is the send key; on the server side, it is the receive key."
	} else {
		magic = "On the client side, this is the receive key; on the server side, it is the send key."
	}
	pad1 := make([]byte, 40)
	pad2 := bytesOf(0xf2, 40)
	s := sha1.New()
	s.Write(master)
	s.Write(pad1)
	s.Write([]byte(magic))
	s.Write(pad2)
	return s.Sum(nil)[:16]
}

func bytesOf(b byte, n int) []byte {
	p := make([]byte, n)
	for i := range p {
		p[i] = b
	}
	return p
}

func hlakFromMSCHAPv2(pass string, ntResp []byte) []byte {
	send, recv := mppeMasterKeys(pass, ntResp)
	out := make([]byte, 32)
	copy(out, send)
	copy(out[16:], recv)
	return out
}

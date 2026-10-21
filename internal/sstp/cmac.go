package sstp

// Compound MAC for SSTP_MSG_CALL_CONNECTED (MS-SSTP 3.2.5.2).

import (
	"crypto/hmac"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/binary"
	"hash"
)

const cmkSeed = "SSTP inner method derived CMK"

func prfPlus(h func() hash.Hash, key, seed []byte, n int) []byte {
	var out []byte
	var t []byte
	for i := 1; len(out) < n; i++ {
		mac := hmac.New(h, key)
		mac.Write(t)
		mac.Write(seed)
		ln := make([]byte, 2)
		binary.LittleEndian.PutUint16(ln, uint16(n))
		mac.Write(ln)
		mac.Write([]byte{byte(i)})
		t = mac.Sum(nil)
		out = append(out, t...)
	}
	return out[:n]
}

func compoundMAC(hlak, nonce, certHash []byte, sha256Hash bool) []byte {
	if len(hlak) < 32 {
		z := make([]byte, 32)
		copy(z, hlak)
		hlak = z
	}
	hf := sha1.New
	macLen := 20
	hashID := byte(hashSHA1)
	if sha256Hash {
		hf = sha256.New
		macLen = 32
		hashID = hashSHA256
	}
	cmk := prfPlus(hf, hlak[:32], []byte(cmkSeed), macLen)
	msg := make([]byte, 0x70)
	msg[0] = ver
	msg[1] = 1
	msg[2] = 0
	msg[3] = 0x70
	msg[4] = 0
	msg[5] = msgConnected
	msg[6] = 0
	msg[7] = 1
	msg[8] = 0
	msg[9] = attrCryptoBinding
	msg[10] = 0
	msg[11] = 0x68
	msg[15] = hashID
	copy(msg[16:48], nonce)
	ch := make([]byte, 32)
	copy(ch, certHash)
	copy(msg[48:80], ch)
	// 80:112 compound MAC left zero
	mac := hmac.New(hf, cmk)
	mac.Write(msg)
	sum := mac.Sum(nil)
	out := make([]byte, 32)
	copy(out, sum)
	return out
}

func connectedPkt(nonce, certHash, cmac []byte, sha256Hash bool) pkt {
	hashID := byte(hashSHA1)
	if sha256Hash {
		hashID = hashSHA256
	}
	val := make([]byte, 100)
	val[3] = hashID
	copy(val[4:36], nonce)
	copy(val[36:68], certHash)
	if len(certHash) < 32 {
		// SHA1 hash sits in the first 20 bytes; rest already zero
	}
	copy(val[68:100], cmac)
	return pkt{ctrl: true, typ: msgConnected, attr: []attr{{id: attrCryptoBinding, val: val}}}
}

func parseBindingReq(val []byte) (mask byte, nonce []byte) {
	if len(val) < 36 {
		return 0, nil
	}
	return val[3], append([]byte(nil), val[4:36]...)
}

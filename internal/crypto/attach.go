package crypto

// 0x04 attaches another front to the live session (the 0x02 that holds the mux).
// HMAC uses the session AEAD key, not the member token. No new ECDH.

import (
	"crypto/hmac"
	"crypto/sha256"
	"fmt"
	"io"
	"time"
)

const tagAttach byte = 0x04

const maxAttachSID = 64
const maxAttachFront = 8

// AttachProofLen is the size of a 0x04 blob for sid and front (variable).
func attachLen(sid, front string) int {
	return 1 + 1 + len(sid) + 16 + 1 + len(front) + AuthSize
}

// LooksLikeAttach is tag 0x04 with a plausible length. FinishAttach still
// checks HMAC; GET Cookie 0x02 is LooksLikeAuth.
func LooksLikeAttach(blob []byte) bool {
	if len(blob) < 1+1+1+16+1+AuthSize || blob[0] != tagAttach {
		return false
	}
	n := int(blob[1])
	if n == 0 || n > maxAttachSID || len(blob) < 2+n+16+1 {
		return false
	}
	f := int(blob[2+n+16])
	if f == 0 || f > maxAttachFront {
		return false
	}
	return len(blob) >= 2+n+16+1+f+AuthSize
}

// MakeAttach is 0x04 || sid || nonce || front || HMAC(session_key, "lft-a4"||sid||nonce||front).
func MakeAttach(key []byte, sid, front string) ([]byte, error) {
	if len(key) != KeySize || sid == "" || !attachableFront(front) || len(sid) > maxAttachSID || len(front) > maxAttachFront {
		return nil, ErrAuth
	}
	nonce := make([]byte, 16)
	if _, err := io.ReadFull(entropy, nonce); err != nil {
		return nil, err
	}
	blob := make([]byte, 0, attachLen(sid, front))
	blob = append(blob, tagAttach, byte(len(sid)))
	blob = append(blob, sid...)
	blob = append(blob, nonce...)
	blob = append(blob, byte(len(front)))
	blob = append(blob, front...)
	blob = append(blob, attachMAC(key, sid, nonce, front)...)
	return blob, nil
}

// ParseAttach reads 0x04 without HMAC. FinishAttach also checks the MAC.
func ParseAttach(blob []byte) (sid, front string, nonce []byte, err error) {
	sid, front, nonce, _, err = parseAttach(blob)
	return sid, front, nonce, err
}

// FinishAttach verifies 0x04 under the GET session key.
func FinishAttach(key, blob []byte) (sid, front string, nonce []byte, err error) {
	sid, front, nonce, mac, err := parseAttach(blob)
	if err != nil {
		return "", "", nil, err
	}
	if len(key) != KeySize || !hmac.Equal(mac, attachMAC(key, sid, nonce, front)) {
		return "", "", nil, ErrAuth
	}
	return sid, front, nonce, nil
}

func parseAttach(blob []byte) (sid, front string, nonce, mac []byte, err error) {
	if !LooksLikeAttach(blob) {
		return "", "", nil, nil, ErrAuth
	}
	n := int(blob[1])
	off := 2
	sid = string(blob[off : off+n])
	off += n
	nonce = append([]byte(nil), blob[off:off+16]...)
	off += 16
	f := int(blob[off])
	off++
	front = string(blob[off : off+f])
	off += f
	if off+AuthSize != len(blob) || !attachableFront(front) {
		return "", "", nil, nil, ErrAuth
	}
	mac = blob[off:]
	return sid, front, nonce, mac, nil
}

func attachableFront(front string) bool {
	switch front {
	case "ws", "h2", "quic", "h3":
		return true
	default:
		return false
	}
}

func attachMAC(key []byte, sid string, nonce []byte, front string) []byte {
	m := hmac.New(sha256.New, key)
	m.Write([]byte("lft-a4"))
	m.Write([]byte(sid))
	m.Write(nonce)
	m.Write([]byte(front))
	return m.Sum(nil)[:AuthSize]
}

// AttachNonce remembers 0x04 nonces per session so a replay is refused.
func (n *Nonces) TakeAttach(nonce []byte) error {
	if n == nil {
		return nil
	}
	if len(nonce) != 16 {
		return ErrAuth
	}
	k := "a4:" + string(nonce)
	now := time.Now()
	n.mu.Lock()
	defer n.mu.Unlock()
	n.sweepLocked(now)
	if rec, ok := n.hs[k]; ok && now.Sub(rec.t) < n.ttl {
		return fmt.Errorf("crypto: attach replay")
	}
	n.hs[k] = &hsRec{t: now}
	return nil
}

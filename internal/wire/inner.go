package wire

import (
	"fmt"
	"strings"
)

// Data-cipher ids on the optional Hello/Status byte.
// An absent byte is ChaCha20-Poly1305. A present 0 is no inner AEAD.
//
// "Inner" is the frame encryption that loft adds inside TLS. The client
// asks for a cipher in Hello and the node answers in Status. "clear" turns
// the inner layer off (TLS still protects the link); it is meant for
// direct fronts where the extra encryption only costs CPU.
const (
	InnerClear  byte = 0
	InnerChaCha byte = 1
	InnerAES    byte = 2 // AES-256-GCM, same 12-byte nonce and 16-byte tag as ChaCha
	InnerHiAE   byte = 5 // draft-pham-cfrg-hiae-06; wire nonce[12]||ct||tag[16]
)

// InnerReserved are ids kept for more data ciphers. Until one is defined
// it is an unknown id: asked for, it leaves the session on ChaCha.
// 8 is reserved for an experimental data cipher (not in this tree).
var InnerReserved = []byte{3, 4, 7, 8}

// ParseInnerName reads clear, chacha, aes, or hiae. Aliases none/off, aes-gcm.
func ParseInnerName(s string) (byte, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "clear", "none", "off":
		return InnerClear, nil
	case "chacha", "chacha20", "chacha20-poly1305":
		return InnerChaCha, nil
	case "aes", "aes-gcm", "aes256-gcm", "aes-256-gcm":
		return InnerAES, nil
	case "hiae":
		return InnerHiAE, nil
	default:
		return 0, fmt.Errorf("inner %q: clear, chacha, aes, or hiae", s)
	}
}

// InnerName is the config spelling of a data-cipher id.
func InnerName(id byte) string {
	switch id {
	case InnerClear:
		return "clear"
	case InnerAES:
		return "aes"
	case InnerHiAE:
		return "hiae"
	default:
		return "chacha"
	}
}

// AgreeInner is the node's answer to a Hello ask.
// GET, an absent ask, an unknown id, and an id the policy does not list
// stay on ChaCha and omit the Status byte. The session still comes up.
// ChaCha is always allowed. allow lists only the extra ids (clear, aes, hiae).
func AgreeInner(front string, askSet bool, ask byte, allow []byte) (set bool, id byte) {
	if front == "" || front == FrontGET || !askSet {
		return false, InnerChaCha
	}
	switch ask {
	case InnerClear, InnerAES, InnerHiAE:
		if !allowsInner(allow, ask) {
			return false, InnerChaCha
		}
		return true, ask
	default:
		return false, InnerChaCha
	}
}

// allowsInner reports whether the node policy lists id. ChaCha is always
// allowed because it is the default every peer supports.
func allowsInner(allow []byte, id byte) bool {
	if id == InnerChaCha {
		return true
	}
	for _, a := range allow {
		if a == id {
			return true
		}
	}
	return false
}

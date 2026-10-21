package xtls

// REALITY public-key and short-id decode (Xray share URI pbk / sid).

import (
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strings"
)

// DecodePbk reads a 32-byte X25519 public key as hex or base64 (URL or std).
func DecodePbk(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, fmt.Errorf("reality: pbk")
	}
	if b, err := hex.DecodeString(s); err == nil && len(b) == 32 {
		return b, nil
	}
	for _, enc := range []*base64.Encoding{base64.RawURLEncoding, base64.URLEncoding, base64.RawStdEncoding, base64.StdEncoding} {
		b, err := enc.DecodeString(s)
		if err == nil && len(b) == 32 {
			return b, nil
		}
	}
	return nil, fmt.Errorf("reality: pbk")
}

// DecodeSid reads a Reality short id as hex, 0–8 bytes. Empty is valid.
func DecodeSid(s string) ([]byte, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil, nil
	}
	if len(s)%2 == 1 {
		s = "0" + s
	}
	b, err := hex.DecodeString(s)
	if err != nil || len(b) > 8 {
		return nil, fmt.Errorf("reality: sid")
	}
	return b, nil
}

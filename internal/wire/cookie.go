package wire

// Cookie encoding for GET uplink. Handshake Cookie stays base64url.
// Later GETs MAY use Z85 when Status uplink_enc is 1. A node MAY still
// accept the older X-Request-Id header.

import (
	"encoding/base64"
	"encoding/binary"
	"errors"
)

const (
	// UplinkBase64 is base64url (old clients and the auth GET).
	UplinkBase64 byte = 0
	// UplinkCookie85 is '~' || Z85(u16be length || blob). ~6% more mux
	// in the same encoded header budget; alphabet is Cookie-safe.
	UplinkCookie85 byte = 1
)

const cookie85tag = '~'

// RFC 32 Z85.
const z85alphabet = "0123456789abcdefghijklmnopqrstuvwxyzABCDEFGHIJKLMNOPQRSTUVWXYZ.-:+=^!/*?&<>()[]{}@%$#"

var (
	errCookie = errors.New("wire: cookie uplink")
	z85dec    = func() (d [256]byte) {
		for i := range d {
			d[i] = 0xFF
		}
		for i := 0; i < 85; i++ {
			d[z85alphabet[i]] = byte(i)
		}
		return d
	}()
)

// EncodeUplink is the GET header value for a sealed mux blob.
func EncodeUplink(blob []byte, enc byte) string {
	if enc == UplinkCookie85 {
		return encodeCookie85(blob)
	}
	return base64.RawURLEncoding.EncodeToString(blob)
}

// DecodeUplink accepts base64url or a '~' Z85 value. Empty is nil.
func DecodeUplink(s string) ([]byte, error) {
	if s == "" {
		return nil, nil
	}
	if s[0] == cookie85tag {
		return decodeCookie85(s[1:])
	}
	return base64.RawURLEncoding.DecodeString(s)
}

func encodeCookie85(blob []byte) string {
	n := len(blob)
	if n > 0xffff {
		n = 0xffff
		blob = blob[:n]
	}
	pad := (4 - (2+n)%4) % 4
	buf := make([]byte, 2+n+pad)
	binary.BigEndian.PutUint16(buf[:2], uint16(n))
	copy(buf[2:], blob)
	return string(cookie85tag) + z85Encode(buf)
}

func decodeCookie85(s string) ([]byte, error) {
	raw, err := z85Decode(s)
	if err != nil {
		return nil, err
	}
	if len(raw) < 2 {
		return nil, errCookie
	}
	n := int(binary.BigEndian.Uint16(raw[:2]))
	if 2+n > len(raw) {
		return nil, errCookie
	}
	out := make([]byte, n)
	copy(out, raw[2:2+n])
	return out, nil
}

func z85Encode(src []byte) string {
	n := len(src) / 4
	out := make([]byte, n*5)
	for i := 0; i < n; i++ {
		v := binary.BigEndian.Uint32(src[i*4:])
		for j := 4; j >= 0; j-- {
			out[i*5+j] = z85alphabet[v%85]
			v /= 85
		}
	}
	return string(out)
}

func z85Decode(s string) ([]byte, error) {
	if len(s) == 0 || len(s)%5 != 0 {
		return nil, errCookie
	}
	n := len(s) / 5
	out := make([]byte, n*4)
	for i := 0; i < n; i++ {
		var v uint32
		for j := 0; j < 5; j++ {
			d := z85dec[s[i*5+j]]
			if d == 0xFF {
				return nil, errCookie
			}
			v = v*85 + uint32(d)
		}
		binary.BigEndian.PutUint32(out[i*4:], v)
	}
	return out, nil
}

package wire

// Cookie encoding for GET uplink. Handshake Cookie stays base64url.
// Later GETs MAY use Z85 when Status uplink_enc is 1.
//
// On the GET front the client has no request body to send data in, so
// client-to-node traffic ("uplink") rides in the Cookie header of each GET.
// A header has a size limit (the "header budget"), so the encoding matters:
// Z85 turns 4 bytes into 5 characters, base64 turns 3 into 4, which leaves
// a little more room for mux data in the same header.

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

// cookie85tag marks a Z85 value. '~' never appears in base64url, so the
// node can accept both encodings without being told which one is used.
const cookie85tag = '~'

// z85alphabet is ZeroMQ RFC 32 Z85.
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

// encodeCookie85 is '~' || Z85(u16be length || blob || zero pad). Z85 works
// on 4-byte groups, so the blob is padded and the length prefix tells the
// decoder where the real bytes end.
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

// decodeCookie85 reverses encodeCookie85 (without the leading '~') and
// drops the padding.
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

// z85Encode encodes whole 4-byte groups; the caller pads src first.
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

// z85Decode decodes 5-character groups. A length that is not a multiple of
// 5 or a character outside the alphabet is an error.
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

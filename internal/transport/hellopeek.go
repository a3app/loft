package transport

// ClientHello peek, SNI/key_share parse, and loft session_id (sid v3 / v2).

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/binary"
	"errors"
	"io"
	"strings"

	"golang.org/x/crypto/curve25519"
	"golang.org/x/crypto/hkdf"
)

// clientHello is the fields decoy and mask need from a TLS ClientHello.
type clientHello struct {
	Random    []byte
	SessionID []byte
	SNI       string
	KeyShare  []byte // X25519 public, 32 bytes
	ALPNs     []string
}

// readClientHello concatenates handshake records until one ClientHello is complete.
func readClientHello(r io.Reader) (raw []byte, h clientHello, err error) {
	var hs []byte
	for {
		rec, rerr := readTLSRecord(r)
		raw = append(raw, rec...)
		if rerr != nil {
			return raw, h, rerr
		}
		if rec[0] != 0x16 {
			return raw, h, errors.New("not tls handshake")
		}
		hs = append(hs, rec[5:]...)
		if len(hs) < 4 {
			continue
		}
		n := int(hs[1])<<16 | int(hs[2])<<8 | int(hs[3])
		if n < 0 || n > 16<<10 {
			return raw, h, errors.New("bad hello length")
		}
		if len(hs) >= 4+n {
			h, err = parseClientHello(hs[:4+n])
			return raw, h, err
		}
	}
}

// readTLSRecord reads one TLS record (header plus body).
func readTLSRecord(r io.Reader) ([]byte, error) {
	hdr := make([]byte, 5)
	if _, err := io.ReadFull(r, hdr); err != nil {
		return nil, err
	}
	n := int(binary.BigEndian.Uint16(hdr[3:5]))
	if hdr[0] < 0x14 || hdr[0] > 0x17 || n < 0 || n > (16<<10)+256 {
		return hdr, errors.New("not tls record")
	}
	body := make([]byte, n)
	nread, err := io.ReadFull(r, body)
	if err != nil {
		return append(hdr, body[:nread]...), err
	}
	return append(hdr, body...), nil
}

// writeTLSRecord writes one TLS record of typ.
func writeTLSRecord(w io.Writer, typ byte, payload []byte) error {
	hdr := []byte{typ, 0x03, 0x03, byte(len(payload) >> 8), byte(len(payload))}
	_, err := w.Write(append(hdr, payload...))
	return err
}

// parseClientHello extracts random, session_id, SNI, ALPN, and X25519 key_share.
func parseClientHello(body []byte) (clientHello, error) {
	var h clientHello
	if len(body) < 38 || body[0] != 0x01 {
		return h, errors.New("not client hello")
	}
	// handshake header 4, version 2, random 32
	off := 4 + 2
	if off+32 > len(body) {
		return h, errors.New("short random")
	}
	h.Random = append([]byte(nil), body[off:off+32]...)
	off += 32
	if off >= len(body) {
		return h, errors.New("short sid")
	}
	sidLen := int(body[off])
	off++
	if off+sidLen > len(body) {
		return h, errors.New("short sid body")
	}
	h.SessionID = append([]byte(nil), body[off:off+sidLen]...)
	off += sidLen
	if off+2 > len(body) {
		return h, nil
	}
	csLen := int(binary.BigEndian.Uint16(body[off : off+2]))
	off += 2 + csLen
	if off >= len(body) {
		return h, nil
	}
	cmLen := int(body[off])
	off++
	off += cmLen
	if off+2 > len(body) {
		return h, nil
	}
	extLen := int(binary.BigEndian.Uint16(body[off : off+2]))
	off += 2
	end := off + extLen
	if end > len(body) {
		end = len(body)
	}
	for off+4 <= end {
		typ := binary.BigEndian.Uint16(body[off : off+2])
		ln := int(binary.BigEndian.Uint16(body[off+2 : off+4]))
		off += 4
		if off+ln > end {
			break
		}
		if typ == 0 && ln >= 5 { // SNI
			list := body[off : off+ln]
			if len(list) >= 5 && list[2] == 0 {
				nlen := int(binary.BigEndian.Uint16(list[3:5]))
				if 5+nlen <= len(list) {
					h.SNI = string(list[5 : 5+nlen])
				}
			}
		}
		if typ == 16 { // ALPN
			h.ALPNs = parseALPN(body[off : off+ln])
		}
		if typ == 51 { // key_share
			h.KeyShare = parseX25519Share(body[off : off+ln])
		}
		off += ln
	}
	return h, nil
}

// parseALPN decodes the ALPN extension payload.
func parseALPN(b []byte) []string {
	if len(b) < 2 {
		return nil
	}
	n := int(binary.BigEndian.Uint16(b[0:2]))
	off := 2
	end := 2 + n
	if end > len(b) {
		end = len(b)
	}
	var out []string
	for off < end {
		ln := int(b[off])
		off++
		if off+ln > end {
			break
		}
		out = append(out, string(b[off:off+ln]))
		off += ln
	}
	return out
}

// parseX25519Share returns the 32-byte X25519 public from key_share, or nil.
func parseX25519Share(b []byte) []byte {
	if len(b) < 2 {
		return nil
	}
	n := int(binary.BigEndian.Uint16(b[0:2]))
	off := 2
	end := 2 + n
	if end > len(b) {
		end = len(b)
	}
	for off+4 <= end {
		group := binary.BigEndian.Uint16(b[off : off+2])
		ln := int(binary.BigEndian.Uint16(b[off+2 : off+4]))
		off += 4
		if off+ln > end {
			break
		}
		if group == 0x001d && ln == 32 { // x25519
			return append([]byte(nil), b[off:off+32]...)
		}
		off += ln
	}
	return nil
}

// sessionHMAC is sid v2: HKDF under the member key. Kept so a client that
// has not yet learned mask_pub can still be admitted. A stolen member key
// can recompute this value; sid v3 cannot.
func sessionHMAC(token []byte, h clientHello) []byte {
	info := append([]byte("loft-sid-v2\x00"), []byte(strings.ToLower(h.SNI))...)
	info = append(info, h.KeyShare...)
	out := make([]byte, 32)
	if _, err := io.ReadFull(hkdf.New(sha256.New, token, h.Random, info), out); err != nil {
		m := hmac.New(sha256.New, token)
		m.Write(h.Random)
		return m.Sum(nil)[:32]
	}
	return out
}

// verifySessionID reports whether h.SessionID equals sessionHMAC(token, h).
func verifySessionID(token []byte, h clientHello) bool {
	if len(h.SessionID) != 32 || len(h.Random) != 32 {
		return false
	}
	return hmac.Equal(h.SessionID, sessionHMAC(token, h))
}

// sessionSID3 is sid v3: HKDF under ECDH(node static X25519, client key_share).
// Detection needs the node private key. A stolen member key does not mark it.
func sessionSID3(shared []byte, h clientHello) []byte {
	info := append([]byte("loft-sid-v3\x00"), []byte(strings.ToLower(h.SNI))...)
	out := make([]byte, 32)
	if _, err := io.ReadFull(hkdf.New(sha256.New, shared, h.Random, info), out); err != nil {
		m := hmac.New(sha256.New, shared)
		m.Write(h.Random)
		m.Write(info)
		return m.Sum(nil)
	}
	return out
}

// sid3FromPriv is the client-side sid v3 from the ClientHello X25519 private
// and the node's static public (Status / roster / share-URI pbk).
func sid3FromPriv(clientPriv, nodePub []byte, h clientHello) []byte {
	if len(clientPriv) != 32 || len(nodePub) != 32 {
		return nil
	}
	shared, err := curve25519.X25519(clientPriv, nodePub)
	if err != nil {
		return nil
	}
	return sessionSID3(shared, h)
}

// verifySID3 reports whether h.SessionID is sid v3 under the node secret.
func verifySID3(nodeToken []byte, h clientHello) bool {
	if len(h.SessionID) != 32 || len(h.Random) != 32 || len(h.KeyShare) != 32 || len(nodeToken) == 0 {
		return false
	}
	shared, err := curve25519.X25519(maskX25519Priv(nodeToken), h.KeyShare)
	if err != nil {
		return false
	}
	return hmac.Equal(h.SessionID, sessionSID3(shared, h))
}

// authSharedFromPriv is ECDH(node static, client key_share) for cert serial bind.
func authSharedFromPriv(nodePriv, clientShare []byte) []byte {
	if len(nodePriv) != 32 || len(clientShare) != 32 {
		return nil
	}
	shared, err := curve25519.X25519(nodePriv, clientShare)
	if err != nil {
		return nil
	}
	return shared
}

// authSharedFromPub is ECDH(client eph, node static public).
func authSharedFromPub(clientPriv, nodePub []byte) []byte {
	if len(clientPriv) != 32 || len(nodePub) != 32 {
		return nil
	}
	shared, err := curve25519.X25519(clientPriv, nodePub)
	if err != nil {
		return nil
	}
	return shared
}

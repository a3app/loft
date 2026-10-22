package wire

// L3 inner frames after Status on the l3 front: one type byte, then payload.

import (
	"encoding/binary"
	"fmt"
)

const (
	// L3IP is one IPv4 or IPv6 datagram. The version nibble of payload[0] selects the family.
	L3IP byte = 0x00
	// L3Batch is repeated u16be length || datagram. Amortizes AEAD on a corked CONNECT.
	L3Batch byte = 0x01
)

// EncodeL3 is type || payload. writeMsg already carries the outer length.
func EncodeL3(typ byte, payload []byte) []byte {
	out := make([]byte, 1+len(payload))
	out[0] = typ
	copy(out[1:], payload)
	return out
}

// EncodeL3Batch is type 0x01 || {u16be n || datagram}...
func EncodeL3Batch(pkts [][]byte) []byte {
	n := 1
	for _, p := range pkts {
		n += 2 + len(p)
	}
	out := make([]byte, 0, n)
	out = append(out, L3Batch)
	for _, p := range pkts {
		var hdr [2]byte
		binary.BigEndian.PutUint16(hdr[:], uint16(len(p)))
		out = append(out, hdr[:]...)
		out = append(out, p...)
	}
	return out
}

// DecodeL3 splits an AEAD plaintext into type and payload.
func DecodeL3(plain []byte) (typ byte, payload []byte, err error) {
	if len(plain) < 1 {
		return 0, nil, fmt.Errorf("l3 short")
	}
	return plain[0], plain[1:], nil
}

// SplitL3 returns the IP datagrams in one AEAD plaintext (0x00 or 0x01).
// Unknown types yield a nil slice and no error (ignore).
func SplitL3(plain []byte) ([][]byte, error) {
	typ, payload, err := DecodeL3(plain)
	if err != nil {
		return nil, err
	}
	switch typ {
	case L3IP:
		if len(payload) < 20 {
			return nil, fmt.Errorf("l3 ip short")
		}
		return [][]byte{payload}, nil
	case L3Batch:
		var out [][]byte
		for len(payload) >= 2 {
			n := int(binary.BigEndian.Uint16(payload[:2]))
			payload = payload[2:]
			if n < 20 || n > len(payload) {
				return nil, fmt.Errorf("l3 batch")
			}
			out = append(out, payload[:n])
			payload = payload[n:]
		}
		if len(payload) != 0 {
			return nil, fmt.Errorf("l3 batch tail")
		}
		return out, nil
	default:
		return nil, nil
	}
}

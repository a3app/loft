package wire

// L3 inner frames after Status on the l3 front: one type byte, then payload.
//
// The l3 front does not use the mux at all. It carries raw IP packets (from
// a TUN device) over an HTTP/2 CONNECT stream, one encrypted message per
// packet or per batch of packets. This avoids per-connection streams when
// the client routes a whole device through loft.

import (
	"encoding/binary"
	"fmt"
)

const (
	// L3IP is one IPv4 or IPv6 datagram. The version nibble of payload[0] selects the family.
	L3IP byte = 0x00
	// L3Batch is repeated u16be length || datagram. Amortizes AEAD on a corked CONNECT.
	L3Batch byte = 0x01
	// L3Padded (experimental, only after CapL3Shape is agreed) is a batch
	// ended by a zero length, then padding the receiver skips:
	// {u16be n || datagram}... || 0x0000 || pad. With no datagrams it is a
	// padding-only message. An older receiver ignores the unknown type,
	// which is why it is sent only when both sides agreed.
	L3Padded byte = 0x02
)

// EncodeL3Padded is a type 0x02 message of pkts whose plaintext is at least
// size bytes long: the batch, a zero length, then zero bytes up to size.
// Zeros are fine as padding: the whole plaintext is sealed.
func EncodeL3Padded(pkts [][]byte, size int) []byte {
	n := 1 + 2
	for _, p := range pkts {
		n += 2 + len(p)
	}
	if size < n {
		size = n
	}
	out := make([]byte, 0, size)
	out = append(out, L3Padded)
	for _, p := range pkts {
		var hdr [2]byte
		binary.BigEndian.PutUint16(hdr[:], uint16(len(p)))
		out = append(out, hdr[:]...)
		out = append(out, p...)
	}
	out = append(out, 0, 0)
	return out[:size]
}

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

// SplitL3 returns the IP datagrams in one AEAD plaintext (0x00, 0x01 or 0x02).
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
	case L3Padded:
		var out [][]byte
		for {
			if len(payload) < 2 {
				return nil, fmt.Errorf("l3 padded: no end")
			}
			n := int(binary.BigEndian.Uint16(payload[:2]))
			payload = payload[2:]
			if n == 0 {
				return out, nil // the rest is padding
			}
			if n < 20 || n > len(payload) {
				return nil, fmt.Errorf("l3 padded batch")
			}
			out = append(out, payload[:n])
			payload = payload[n:]
		}
	default:
		return nil, nil
	}
}

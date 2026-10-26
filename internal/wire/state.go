package wire

// State: the files a standby controller needs to take over.
//
// The controller sends its state to a standby (a relay listed as replica)
// on ctl:1: member and relay keys, network and node config, the signing
// seed, the key roll and the mask secret. Each file travels whole, by name.

import (
	"encoding/binary"
	"errors"
	"sort"
)

// EncodeState is u8 n, then n × (u8 name_len || name || u32be len || bytes),
// names in sorted order so the same state always encodes the same way.
func EncodeState(files map[string][]byte) []byte {
	names := make([]string, 0, len(files))
	for n := range files {
		if n != "" && len(n) <= 255 {
			names = append(names, n)
		}
	}
	sort.Strings(names)
	if len(names) > 255 {
		names = names[:255]
	}
	b := []byte{byte(len(names))}
	for _, n := range names {
		b = append(b, byte(len(n)))
		b = append(b, n...)
		b = binary.BigEndian.AppendUint32(b, uint32(len(files[n])))
		b = append(b, files[n]...)
	}
	return b
}

// DecodeState parses EncodeState.
func DecodeState(b []byte) (map[string][]byte, error) {
	bad := errors.New("wire: state form")
	if len(b) < 1 {
		return nil, bad
	}
	n := int(b[0])
	b = b[1:]
	out := make(map[string][]byte, n)
	for i := 0; i < n; i++ {
		if len(b) < 1 || len(b) < 1+int(b[0])+4 {
			return nil, bad
		}
		nl := int(b[0])
		name := string(b[1 : 1+nl])
		b = b[1+nl:]
		dl := int(binary.BigEndian.Uint32(b[:4]))
		b = b[4:]
		if dl > len(b) || name == "" {
			return nil, bad
		}
		out[name] = append([]byte(nil), b[:dl]...)
		b = b[dl:]
	}
	return out, nil
}

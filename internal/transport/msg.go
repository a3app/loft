package transport

// Length-prefixed messages (u32be length || payload, at most 1 MiB) for h2/quic/h3/ctl.
// These fronts give a plain byte stream with no message boundaries, so every
// sealed batch, the 0x02 handshake, Status and 0x04 attach travel with a
// 4-byte big-endian length in front.

import (
	"encoding/binary"
	"fmt"
	"io"
)

// writeMsg writes u32be length then b in one Write (one QUIC/TLS record).
func writeMsg(w io.Writer, b []byte) error {
	buf := make([]byte, 4+len(b))
	binary.BigEndian.PutUint32(buf[:4], uint32(len(b)))
	copy(buf[4:], b)
	_, err := w.Write(buf)
	return err
}

// readMsg reads one length-prefixed message; n > 1 MiB is an error.
// The cap stops a corrupt or hostile length from making us allocate
// gigabytes before any byte is authenticated.
func readMsg(r io.Reader) ([]byte, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n > 1<<20 {
		return nil, fmt.Errorf("msg too large")
	}
	b := make([]byte, n)
	_, err := io.ReadFull(r, b)
	return b, err
}

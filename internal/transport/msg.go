package transport

// Length-prefixed messages (u32be length || payload, at most 1 MiB) for h2/quic/h3/ctl.
// These fronts give a plain byte stream with no message boundaries, so every
// sealed batch, the 0x02 handshake, Status and 0x04 attach travel with a
// 4-byte big-endian length in front.

import (
	"encoding/binary"
	"fmt"
	"io"
	"sync"

	ecrypto "github.com/a3app/loft/internal/crypto"
	"github.com/a3app/loft/internal/wire"
)

// msgPool holds u32be||payload scratch for writeMsg (handshake, attach,
// fleet). Mux pumps use recScratch instead and skip this copy.
// Cap above 256 KiB is dropped so a 1 MiB ctl message does not pin RAM.
var msgPool = sync.Pool{New: func() any {
	b := make([]byte, 0, 4+(48<<10)+ecrypto.SealOverhead)
	return &b
}}

const msgPoolMax = 256 << 10

// writeMsg writes u32be length then b in one Write (one QUIC/TLS record).
// Two writes (hdr, body) would be two TLS records and stall H2 windows.
// w.Write must not retain b after return (net.Conn, bufio, corkWriter copy).
func writeMsg(w io.Writer, b []byte) error {
	p := msgPool.Get().(*[]byte)
	buf := *p
	need := 4 + len(b)
	if cap(buf) < need {
		buf = make([]byte, need)
	} else {
		buf = buf[:need]
	}
	binary.BigEndian.PutUint32(buf[:4], uint32(len(b)))
	copy(buf[4:], b)
	_, err := w.Write(buf)
	if cap(buf) <= msgPoolMax {
		*p = buf
		msgPool.Put(p)
	}
	return err
}

// writeSealed is SealInto into rec[4:] then one Write of u32be||blob.
// rec is grown in place; caller reuses it under a write lock (l3 wmu).
func writeSealed(w io.Writer, rec *[]byte, key, plain, aad []byte) error {
	buf := *rec
	if cap(buf) < 4 {
		buf = make([]byte, 0, 4+len(plain)+ecrypto.SealOverhead)
	}
	buf = buf[:4]
	var err error
	buf, err = ecrypto.SealInto(buf, key, plain, aad)
	if err != nil {
		return err
	}
	binary.BigEndian.PutUint32(buf[:4], uint32(len(buf)-4))
	_, err = w.Write(buf)
	*rec = buf
	return err
}

// recScratch is the write-side buffers of one mux pump.
//
//	plain  EncodeBatchInto (AEAD plaintext)
//	rec    u32be length || nonce[12]||ct||tag[16]
//
// Two slices because AEAD.Seal forbids dst overlapping plaintext.
// Lifetime: pumpGated (gate != nil) Pulls, seals, Writes on one goroutine;
// corkWriter/net.Conn copy rec before Write returns, so the next Pull may
// reuse both slices. pumpWS prefetch (gate == nil) must not share a scratch
// across the out channel — those sites keep allocating.
type recScratch struct {
	plain []byte
	rec   []byte
}

// pack encodes frames and seals them into rec.
// blob is rec[4:] (WS binary message). rec is the length-prefixed record
// (h2/quic/h3 write). aad is empty on those fronts; GET uses sid instead
// and does not go through pack.
func (s *recScratch) pack(key, aad []byte, frames []wire.Frame) (blob, rec []byte, err error) {
	s.plain, err = wire.EncodeBatchInto(s.plain[:0], frames)
	if err != nil {
		return nil, nil, err
	}
	if cap(s.rec) < 4 {
		s.rec = make([]byte, 0, 4+len(s.plain)+ecrypto.SealOverhead)
	}
	s.rec = s.rec[:4]
	s.rec, err = ecrypto.SealInto(s.rec, key, s.plain, aad)
	if err != nil {
		return nil, nil, err
	}
	binary.BigEndian.PutUint32(s.rec[:4], uint32(len(s.rec)-4))
	return s.rec[4:], s.rec, nil
}

// maxMsg is the largest message readMsg takes (1 MiB).
const maxMsg = 1 << 20

// readMsg reads one length-prefixed message; n > 1 MiB is an error.
// The cap stops a corrupt or hostile length from making us allocate
// gigabytes before any byte is authenticated.
func readMsg(r io.Reader) ([]byte, error) {
	var hdr [4]byte
	if _, err := io.ReadFull(r, hdr[:]); err != nil {
		return nil, err
	}
	n := binary.BigEndian.Uint32(hdr[:])
	if n > maxMsg {
		return nil, fmt.Errorf("msg too large")
	}
	b := make([]byte, n)
	_, err := io.ReadFull(r, b)
	return b, err
}

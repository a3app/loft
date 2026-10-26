// Package wire is loft overlay protocol 1.0 (0x0100): Hello/Status handshake,
// mux frames, signed roster/lists, and fleet control. Handshake is
// auth/Hello/Status, not mux. CmdLeave ends the mux session.
//
// This package only turns protocol messages into bytes and back. It does no
// network I/O and no encryption (that is internal/crypto). The order of
// a session is:
//   - the client's 0x02 message (key proof, built in internal/crypto) with
//     a Hello inside: what the client can do;
//   - the node's Status: the answer with a Code, the overlay address,
//     timers, the list of fronts, and a ticket (session id);
//   - then mux frames (Frame) in both directions until CmdLeave.
//
// The binary layouts grow only at the end: a newer field is appended after
// the older ones, so an old decoder stops early and still works. That is why
// most decoders here accept a truncated tail and ignore unknown bytes.
package wire

import (
	"encoding/binary"
	"errors"
	"io"
)

// Multiplex commands. Handshake (auth, Hello, Status) is not mux.
//
// Open starts a stream to host:port, Data carries its bytes, Close ends it
// normally and Reset ends it with an error Code. Ping/Pong measure the
// round trip and keep the path alive. Pad is filler that the receiver
// ignores (for example, what a GET carries when it has no real data).
const (
	CmdOpen   byte = 1
	CmdData   byte = 2
	CmdClose  byte = 3
	CmdPing   byte = 4
	CmdPong   byte = 5
	CmdPad    byte = 6
	CmdReset  byte = 7
	CmdDgram  byte = 8  // UDP datagram: Open-target prefix + payload
	CmdLeave  byte = 9  // end the mux session (stream_id 0, empty payload)
	CmdTune   byte = 10 // front event (client) or budget/inflight set (node)
	CmdReport byte = 11 // GET uplink applied-through seq; stream_id 0
	CmdFront  byte = 12 // which attached front carries bulk mux; stream_id 0
)

// Frame header is 7 bytes; payload is at most 16 KiB.
const (
	// Header is u16be payload length, u8 cmd, u32be stream id.
	HeaderSize = 7
	MaxPayload = 16 << 10 // 16384; mux payload inside AEAD
)

// Frame codec errors. Payload above MaxPayload is ErrTooLarge.
var (
	ErrTooLarge = errors.New("wire: payload too large")
	ErrShort    = errors.New("wire: short frame")
)

// Frame is one mux frame: cmd, stream id, payload (at most MaxPayload).
type Frame struct {
	Cmd      byte
	StreamID uint32
	Payload  []byte
}

// Encode writes u16be length || cmd || u32be stream || payload.
func (f Frame) Encode() ([]byte, error) {
	if len(f.Payload) > MaxPayload {
		return nil, ErrTooLarge
	}
	buf := make([]byte, HeaderSize+len(f.Payload))
	binary.BigEndian.PutUint16(buf[0:2], uint16(len(f.Payload)))
	buf[2] = f.Cmd
	binary.BigEndian.PutUint32(buf[3:7], f.StreamID)
	copy(buf[7:], f.Payload)
	return buf, nil
}

// Decode reads one frame from buf. n is bytes consumed.
func Decode(buf []byte) (Frame, int, error) {
	if len(buf) < HeaderSize {
		return Frame{}, 0, ErrShort
	}
	n := int(binary.BigEndian.Uint16(buf[0:2]))
	if n > MaxPayload {
		return Frame{}, 0, ErrTooLarge
	}
	need := HeaderSize + n
	if len(buf) < need {
		return Frame{}, 0, ErrShort
	}
	f := Frame{Cmd: buf[2], StreamID: binary.BigEndian.Uint32(buf[3:7])}
	if n > 0 {
		f.Payload = append([]byte(nil), buf[7:need]...)
	}
	return f, need, nil
}

// WriteFrame encodes f and writes it to w.
func WriteFrame(w io.Writer, f Frame) error {
	b, err := f.Encode()
	if err != nil {
		return err
	}
	_, err = w.Write(b)
	return err
}

// ReadFrame reads one length-prefixed frame from r.
func ReadFrame(r io.Reader) (Frame, error) {
	hdr := make([]byte, HeaderSize)
	if _, err := io.ReadFull(r, hdr); err != nil {
		return Frame{}, err
	}
	n := int(binary.BigEndian.Uint16(hdr[0:2]))
	if n > MaxPayload {
		return Frame{}, ErrTooLarge
	}
	var payload []byte
	if n > 0 {
		payload = make([]byte, n)
		if _, err := io.ReadFull(r, payload); err != nil {
			return Frame{}, err
		}
	}
	return Frame{Cmd: hdr[2], StreamID: binary.BigEndian.Uint32(hdr[3:7]), Payload: payload}, nil
}

// EncodeBatch concatenates frames. CmdData larger than MaxPayload is split
// so a GET front does not drop the whole AEAD batch. One batch is what the
// front seals into a single encrypted message (AEAD: authenticated
// encryption, so a changed byte makes the whole message fail to open).
func EncodeBatch(frames []Frame) ([]byte, error) {
	var out []byte
	for _, f := range frames {
		if f.Cmd != CmdData || len(f.Payload) <= MaxPayload {
			b, err := f.Encode()
			if err != nil {
				return nil, err
			}
			out = append(out, b...)
			continue
		}
		// io.Copy writes 32 KiB; a single CmdData used to blow MaxPayload
		// and the GET front dropped the whole batch (TLS bad decrypt).
		p := f.Payload
		for len(p) > 0 {
			n := MaxPayload
			if n > len(p) {
				n = len(p)
			}
			b, err := Frame{Cmd: CmdData, StreamID: f.StreamID, Payload: p[:n]}.Encode()
			if err != nil {
				return nil, err
			}
			out = append(out, b...)
			p = p[n:]
		}
	}
	return out, nil
}

// DecodeBatch reads concatenated frames until buf is empty.
func DecodeBatch(buf []byte) ([]Frame, error) {
	var frames []Frame
	for len(buf) > 0 {
		f, n, err := Decode(buf)
		if err != nil {
			return nil, err
		}
		frames = append(frames, f)
		buf = buf[n:]
	}
	return frames, nil
}

// ResetPayload is the CmdReset body: u16be code.
func ResetPayload(c Code) []byte {
	var b [2]byte
	binary.BigEndian.PutUint16(b[:], uint16(c))
	return b[:]
}

// ParseReset reads a CmdReset payload. A short payload is Internal.
func ParseReset(p []byte) Code {
	if len(p) < 2 {
		return Internal
	}
	return Code(binary.BigEndian.Uint16(p))
}

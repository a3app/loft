package wire

import "encoding/binary"

// CmdLists payloads (§6, §10.3). stream_id is 0.
//
// Client ask:  u64be have || u32be offset
// Node piece:  u64be serial || u32be total || u32be offset || bytes
// total 0 means the node has nothing newer than have.

const (
	listsAskLen   = 12
	listsPieceHdr = 16
)

// EncodeListsAsk is the client CmdLists body.
func EncodeListsAsk(have uint64, offset uint32) []byte {
	var b [listsAskLen]byte
	binary.BigEndian.PutUint64(b[0:8], have)
	binary.BigEndian.PutUint32(b[8:12], offset)
	return b[:]
}

// ParseListsAsk reads a 12-byte client ask. Longer payloads are pieces.
func ParseListsAsk(p []byte) (have uint64, offset uint32, ok bool) {
	if len(p) != listsAskLen {
		return 0, 0, false
	}
	return binary.BigEndian.Uint64(p[:8]), binary.BigEndian.Uint32(p[8:12]), true
}

// EncodeListsPiece is the node CmdLists body. body is one slice of the signed blob.
func EncodeListsPiece(serial uint64, total, offset uint32, body []byte) []byte {
	b := make([]byte, listsPieceHdr+len(body))
	binary.BigEndian.PutUint64(b[0:8], serial)
	binary.BigEndian.PutUint32(b[8:12], total)
	binary.BigEndian.PutUint32(b[12:16], offset)
	copy(b[16:], body)
	return b
}

// ParseListsPiece reads a node piece. Short or 12-byte (ask) payloads fail.
func ParseListsPiece(p []byte) (serial uint64, total, offset uint32, body []byte, ok bool) {
	if len(p) < listsPieceHdr {
		return 0, 0, 0, nil, false
	}
	serial = binary.BigEndian.Uint64(p[:8])
	total = binary.BigEndian.Uint32(p[8:12])
	offset = binary.BigEndian.Uint32(p[12:16])
	if len(p) > listsPieceHdr {
		body = p[listsPieceHdr:]
	}
	return serial, total, offset, body, true
}

// ListsAskFrame is CmdLists on stream 0 from the client.
func ListsAskFrame(have uint64, offset uint32) Frame {
	return Frame{Cmd: CmdLists, Payload: EncodeListsAsk(have, offset)}
}

// ListsPieceFrame is CmdLists on stream 0 from the node.
func ListsPieceFrame(serial uint64, total, offset uint32, body []byte) Frame {
	return Frame{Cmd: CmdLists, Payload: EncodeListsPiece(serial, total, offset, body)}
}

// listsPieceMax is how many blob bytes fit in one mux frame.
const listsPieceMax = MaxPayload - listsPieceHdr

// ListsReplyFrame answers one ask. have is the serial the client already
// holds. total 0 when the node has nothing newer. One piece per ask; the
// client asks again from the next offset.
func ListsReplyFrame(have uint64, offset uint32, serial uint64, blob []byte) Frame {
	if serial == 0 || have >= serial {
		return ListsPieceFrame(serial, 0, 0, nil)
	}
	total := uint32(len(blob))
	if offset > total {
		offset = total
	}
	end := offset + uint32(listsPieceMax)
	if end > total {
		end = total
	}
	return ListsPieceFrame(serial, total, offset, blob[offset:end])
}

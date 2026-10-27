package wire

import (
	"encoding/binary"
	"testing"
)

func TestListsAskRoundTrip(t *testing.T) {
	p := EncodeListsAsk(20261027, 0)
	if len(p) != 12 {
		t.Fatalf("len %d", len(p))
	}
	have, off, ok := ParseListsAsk(p)
	if !ok || have != 20261027 || off != 0 {
		t.Fatalf("%d %d %v", have, off, ok)
	}
	if _, _, ok := ParseListsAsk(nil); ok {
		t.Fatal("short")
	}
	if _, _, ok := ParseListsAsk(EncodeListsPiece(1, 0, 0, nil)); ok {
		t.Fatal("piece parsed as ask")
	}
}

func TestListsPieceRoundTrip(t *testing.T) {
	body := []byte{1, 2, 3}
	p := EncodeListsPiece(9, 100, 4, body)
	ser, total, off, got, ok := ParseListsPiece(p)
	if !ok || ser != 9 || total != 100 || off != 4 || string(got) != string(body) {
		t.Fatalf("%d %d %d %v %v", ser, total, off, got, ok)
	}
	if _, _, _, _, ok := ParseListsPiece(EncodeListsAsk(1, 0)); ok {
		t.Fatal("ask parsed as piece")
	}
}

func TestListsReplyNothingNewer(t *testing.T) {
	f := ListsReplyFrame(7, 0, 7, []byte("x"))
	if f.Cmd != CmdLists || f.StreamID != 0 {
		t.Fatalf("%+v", f)
	}
	ser, total, _, body, ok := ParseListsPiece(f.Payload)
	if !ok || ser != 7 || total != 0 || len(body) != 0 {
		t.Fatalf("%d %d %v", ser, total, body)
	}
	f = ListsReplyFrame(8, 0, 7, []byte("x"))
	_, total, _, _, _ = ParseListsPiece(f.Payload)
	if total != 0 {
		t.Fatal("older serial still sent")
	}
}

func TestListsReplySlices(t *testing.T) {
	blob := make([]byte, listsPieceMax+10)
	for i := range blob {
		blob[i] = byte(i)
	}
	f := ListsReplyFrame(0, 0, 3, blob)
	ser, total, off, body, ok := ParseListsPiece(f.Payload)
	if !ok || ser != 3 || total != uint32(len(blob)) || off != 0 || len(body) != listsPieceMax {
		t.Fatalf("%d %d %d %d", ser, total, off, len(body))
	}
	f = ListsReplyFrame(0, uint32(listsPieceMax), 3, blob)
	_, total, off, body, ok = ParseListsPiece(f.Payload)
	if !ok || total != uint32(len(blob)) || off != uint32(listsPieceMax) || len(body) != 10 {
		t.Fatalf("tail %d %d %d", total, off, len(body))
	}
}

func TestListsVectorFrames(t *testing.T) {
	// §16.6 req / have / none lengths.
	req := ListsAskFrame(0, 0)
	b, err := req.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if binary.BigEndian.Uint16(b[:2]) != 12 || b[2] != CmdLists {
		t.Fatalf("%x", b)
	}
	none := ListsPieceFrame(20261027, 0, 0, nil)
	b, err = none.Encode()
	if err != nil {
		t.Fatal(err)
	}
	if binary.BigEndian.Uint16(b[:2]) != 16 || b[2] != CmdLists {
		t.Fatalf("none %x", b)
	}
}

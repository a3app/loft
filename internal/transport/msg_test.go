package transport

import (
	"bytes"
	"encoding/binary"
	"io"
	"testing"

	ecrypto "github.com/a3app/loft/internal/crypto"
	"github.com/a3app/loft/internal/wire"
)

func TestWriteMsgOneWrite(t *testing.T) {
	var w countWriter
	payload := []byte("hello-loft")
	if err := writeMsg(&w, payload); err != nil {
		t.Fatal(err)
	}
	if w.n != 1 {
		t.Fatalf("writes %d want 1", w.n)
	}
	if len(w.b) != 4+len(payload) {
		t.Fatalf("len %d", len(w.b))
	}
	if binary.BigEndian.Uint32(w.b[:4]) != uint32(len(payload)) {
		t.Fatalf("hdr")
	}
	got, err := readMsg(bytes.NewReader(w.b))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("roundtrip %q", got)
	}
}

type countWriter struct {
	b []byte
	n int
}

func (w *countWriter) Write(p []byte) (int, error) {
	w.n++
	w.b = append(w.b, p...)
	return len(p), nil
}

func TestRecScratchPackRoundTrip(t *testing.T) {
	key := bytes.Repeat([]byte{0x33}, 32)
	frames := []wire.Frame{{Cmd: wire.CmdData, StreamID: 1, Payload: []byte("hello")}}
	var sc recScratch
	blob, rec, err := sc.pack(key, nil, frames)
	if err != nil {
		t.Fatal(err)
	}
	if binary.BigEndian.Uint32(rec[:4]) != uint32(len(blob)) {
		t.Fatalf("hdr %d blob %d", binary.BigEndian.Uint32(rec[:4]), len(blob))
	}
	if len(rec) != 4+len(blob) {
		t.Fatalf("rec %d blob %d", len(rec), len(blob))
	}
	plain, err := ecrypto.Open(key, blob, nil)
	if err != nil {
		t.Fatal(err)
	}
	out, err := wire.DecodeBatch(plain)
	if err != nil || len(out) != 1 || string(out[0].Payload) != "hello" {
		t.Fatalf("%+v %v", out, err)
	}
	capRec, capPlain := cap(sc.rec), cap(sc.plain)
	_, rec2, err := sc.pack(key, nil, frames)
	if err != nil {
		t.Fatal(err)
	}
	if cap(rec2) != capRec || cap(sc.plain) != capPlain {
		t.Fatalf("realloc rec=%d→%d plain=%d→%d", capRec, cap(rec2), capPlain, cap(sc.plain))
	}
}

func TestWriteSealedRoundTrip(t *testing.T) {
	key := bytes.Repeat([]byte{0x44}, 32)
	var w countWriter
	var rec []byte
	if err := writeSealed(&w, &rec, key, []byte("ip"), nil); err != nil {
		t.Fatal(err)
	}
	if w.n != 1 {
		t.Fatalf("writes %d", w.n)
	}
	got, err := readMsg(bytes.NewReader(w.b))
	if err != nil {
		t.Fatal(err)
	}
	pt, err := ecrypto.Open(key, got, nil)
	if err != nil || string(pt) != "ip" {
		t.Fatalf("%q %v", pt, err)
	}
}

func TestReadMsgTooLarge(t *testing.T) {
	var hdr [4]byte
	binary.BigEndian.PutUint32(hdr[:], 1<<20+1)
	_, err := readMsg(bytes.NewReader(hdr[:]))
	if err == nil {
		t.Fatal("want too large")
	}
	_, err = readMsg(bytes.NewReader(nil))
	if err != io.EOF && err != io.ErrUnexpectedEOF {
		t.Fatalf("empty %v", err)
	}
}

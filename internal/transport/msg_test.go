package transport

import (
	"bytes"
	"encoding/binary"
	"io"
	"testing"
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

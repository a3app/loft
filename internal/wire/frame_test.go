package wire

// Mux frame encode/decode, batch split, and CmdLeave layout.

import (
	"bytes"
	"testing"
)

// TestFrameRoundTrip encodes then decodes a Data frame.
func TestFrameRoundTrip(t *testing.T) {
	f := Frame{Cmd: CmdData, StreamID: 7, Payload: []byte("hello")}
	b, err := f.Encode()
	if err != nil {
		t.Fatal(err)
	}
	got, n, err := Decode(b)
	if err != nil || n != len(b) {
		t.Fatalf("decode: n=%d err=%v", n, err)
	}
	if got.Cmd != f.Cmd || got.StreamID != f.StreamID || !bytes.Equal(got.Payload, f.Payload) {
		t.Fatalf("mismatch %+v", got)
	}
}

// TestBatch concatenates Open+Data and splits them back.
func TestBatch(t *testing.T) {
	in := []Frame{
		{Cmd: CmdOpen, StreamID: 1, Payload: []byte("a.com:443")},
		{Cmd: CmdData, StreamID: 1, Payload: []byte("xyz")},
	}
	b, err := EncodeBatch(in)
	if err != nil {
		t.Fatal(err)
	}
	out, err := DecodeBatch(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 || string(out[0].Payload) != "a.com:443" {
		t.Fatalf("got %+v", out)
	}
}

// TestEncodeBatchSplitsData chunks CmdData above MaxPayload into two frames.
func TestEncodeBatchSplitsData(t *testing.T) {
	big := make([]byte, MaxPayload+100)
	for i := range big {
		big[i] = byte(i)
	}
	b, err := EncodeBatch([]Frame{{Cmd: CmdData, StreamID: 3, Payload: big}})
	if err != nil {
		t.Fatal(err)
	}
	out, err := DecodeBatch(b)
	if err != nil {
		t.Fatal(err)
	}
	if len(out) != 2 {
		t.Fatalf("frames %d", len(out))
	}
	var got []byte
	for _, f := range out {
		if f.Cmd != CmdData || f.StreamID != 3 {
			t.Fatalf("%+v", f)
		}
		if len(f.Payload) > MaxPayload {
			t.Fatalf("chunk %d", len(f.Payload))
		}
		got = append(got, f.Payload...)
	}
	if !bytes.Equal(got, big) {
		t.Fatal("split mismatch")
	}
}

// TestLeaveFrame is cmd 9, stream 0, empty payload.
func TestLeaveFrame(t *testing.T) {
	f := Frame{Cmd: CmdLeave}
	b, err := f.Encode()
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{0x00, 0x00, CmdLeave, 0, 0, 0, 0}
	if !bytes.Equal(b, want) {
		t.Fatalf("%x != %x", b, want)
	}
	got, n, err := Decode(b)
	if err != nil || n != HeaderSize || got.Cmd != CmdLeave || got.StreamID != 0 || len(got.Payload) != 0 {
		t.Fatalf("%+v n=%d err=%v", got, n, err)
	}
}

// TestFrameLayout checks u16be length || cmd || u32be stream || payload.
func TestFrameLayout(t *testing.T) {
	f := Frame{Cmd: CmdData, StreamID: 1, Payload: []byte("hi")}
	b, err := f.Encode()
	if err != nil {
		t.Fatal(err)
	}
	want := []byte{0x00, 0x02, CmdData, 0, 0, 0, 1, 'h', 'i'}
	if !bytes.Equal(b, want) {
		t.Fatalf("%x != %x", b, want)
	}
}

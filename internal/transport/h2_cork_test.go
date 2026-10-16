package transport

import (
	"bytes"
	"testing"
	"time"
)

func TestH2HTTPServerWindows(t *testing.T) {
	s := H2HTTPServer()
	if s.MaxUploadBufferPerStream < h2StreamWindow {
		t.Fatalf("stream window %d", s.MaxUploadBufferPerStream)
	}
	if s.MaxUploadBufferPerConnection < s.MaxUploadBufferPerStream {
		t.Fatalf("conn window %d < stream %d", s.MaxUploadBufferPerConnection, s.MaxUploadBufferPerStream)
	}
	if s.MaxReadFrameSize < 1<<20 {
		t.Fatalf("frame %d", s.MaxReadFrameSize)
	}
}

func TestCorkWriterCloseFlushes(t *testing.T) {
	var buf bytes.Buffer
	c := newCorkWriter(&buf)
	if _, err := c.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	if buf.Len() != 0 {
		t.Fatalf("unflushed %q", buf.Bytes())
	}
	if err := c.Close(); err != nil {
		t.Fatal(err)
	}
	if buf.String() != "hello" {
		t.Fatalf("got %q", buf.String())
	}
}

func TestCorkWriterTicker(t *testing.T) {
	var buf bytes.Buffer
	c := newCorkWriter(&buf)
	defer c.Close()
	if _, err := c.Write([]byte("hi")); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(80 * time.Millisecond)
	for buf.Len() == 0 && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if buf.String() != "hi" {
		t.Fatalf("ticker did not flush: %q", buf.String())
	}
}

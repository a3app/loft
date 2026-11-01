package xtls

import (
	"bytes"
	"io"
	"net"
	"testing"
	"time"
)

func TestRecordGateStopsAtRecordEnd(t *testing.T) {
	a, b := net.Pipe()
	defer a.Close()
	defer b.Close()
	rec1 := []byte{0x17, 0x03, 0x03, 0x00, 0x02, 'A', 'B'}
	rec2 := []byte{0x17, 0x03, 0x03, 0x00, 0x01, 'C'}
	go func() {
		_, _ = b.Write(append(append([]byte{}, rec1...), rec2...))
	}()
	g := NewRecordGate(a)
	_ = g.SetDeadline(time.Now().Add(2 * time.Second))
	got := make([]byte, 32)
	n, err := g.Read(got)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got[:n], rec1) {
		t.Fatalf("first record %x", got[:n])
	}
	head, raw := g.Release()
	if len(head) != 0 {
		t.Fatalf("over-read %x", head)
	}
	_ = raw.SetDeadline(time.Now().Add(2 * time.Second))
	n, err = io.ReadFull(raw, got[:len(rec2)])
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got[:n], rec2) {
		t.Fatalf("second record %x", got[:n])
	}
}

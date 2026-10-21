package mobile

import (
	"io"
	"strings"
	"testing"
	"time"
)

func TestStartInvalidYAML(t *testing.T) {
	e := NewEngine()
	if err := e.Start("not yaml"); err == nil {
		t.Fatal("want error")
	}
	if s := e.Snapshot(); s.State != "error" {
		t.Fatalf("state %q", s.State)
	}
	if err := e.Start("token: x\noutbounds: []\n"); err == nil {
		t.Fatal("want empty outbounds error")
	}
	if err := e.Start("token: x\noutbounds:\n  - name: a\n    type: wireguard\n    url: https://x\n"); err == nil {
		t.Fatal("want unknown type")
	}
}

func TestStopNeverStarted(t *testing.T) {
	e := NewEngine()
	e.Stop()
	s := e.Snapshot()
	if s.State != "idle" {
		t.Fatalf("state %q", s.State)
	}
	if err := e.InjectPacket([]byte{0x45}); err == nil {
		t.Fatal("inject on idle")
	}
	if e.PullPacket() != nil {
		t.Fatal("pull on idle")
	}
	e.NotifyPathChange()
}

func TestPacketInjectPull(t *testing.T) {
	e := NewEngine()
	e.pipe = newPacketPipe()
	defer e.pipe.Close()

	fromOS := []byte{0x45, 0, 0, 20, 0, 0, 0, 0, 64, 1, 0, 0, 10, 0, 0, 1, 10, 0, 0, 2}
	if err := e.InjectPacket(fromOS); err != nil {
		t.Fatal(err)
	}
	if e.PullPacket() != nil {
		t.Fatal("inject is OS→session, not Pull")
	}
	got := make([]byte, 64)
	n, err := e.pipe.Read(got)
	if err != nil {
		t.Fatal(err)
	}
	if string(got[:n]) != string(fromOS) {
		t.Fatalf("read %x", got[:n])
	}

	toOS := []byte{0x45, 0, 0, 28}
	if _, err := e.pipe.Write(toOS); err != nil {
		t.Fatal(err)
	}
	pulled := e.PullPacket()
	if string(pulled) != string(toOS) {
		t.Fatalf("pull %x", pulled)
	}
	if e.PullPacket() != nil {
		t.Fatal("second pull")
	}
}

func TestPacketSink(t *testing.T) {
	e := NewEngine()
	e.pipe = newPacketPipe()
	defer e.pipe.Close()
	ch := make(chan []byte, 1)
	e.SetSink(testSink(func(p []byte) { ch <- p }))
	want := []byte{0x45, 0, 0, 20}
	if _, err := e.pipe.Write(want); err != nil {
		t.Fatal(err)
	}
	select {
	case got := <-ch:
		if string(got) != string(want) {
			t.Fatalf("%x", got)
		}
	case <-time.After(time.Second):
		t.Fatal("sink")
	}
	if e.PullPacket() != nil {
		t.Fatal("sink is exclusive with Pull")
	}
}

func TestPacketQueueFullAndClose(t *testing.T) {
	p := newPacketPipe()
	pkt := []byte{0x45}
	for i := 0; i < pipeCap; i++ {
		if err := p.inject(pkt); err != nil {
			t.Fatalf("fill %d: %v", i, err)
		}
	}
	if err := p.inject(pkt); err != errQueueFull {
		t.Fatalf("want full, got %v", err)
	}
	if err := p.Close(); err != nil {
		t.Fatal(err)
	}
	if err := p.inject(pkt); err != io.ErrClosedPipe {
		t.Fatalf("closed inject %v", err)
	}
	buf := make([]byte, 8)
	for i := 0; i < pipeCap; i++ {
		if _, err := p.Read(buf); err != nil {
			t.Fatalf("drain %d: %v", i, err)
		}
	}
	if _, err := p.Read(buf); err != io.EOF {
		t.Fatalf("eof %v", err)
	}
}

func TestLoftOnlyStripsSSTP(t *testing.T) {
	y, err := ParseShare("token: lt_x\noutbounds:\n  - name: a\n    type: get\n    url: https://a.example.com\n  - name: b\n    type: sstp\n    url: https://b.example.com\n")
	if err != nil {
		t.Fatal(err)
	}
	cfg, _, err := decodeFile([]byte(y))
	if err != nil {
		t.Fatal(err)
	}
	only := loftOnly(cfg)
	if len(only.Outbounds) != 1 || only.Outbounds[0].Type != "get" {
		t.Fatalf("%+v", only.Outbounds)
	}
	if only.P2P == nil || *only.P2P {
		t.Fatal("p2p off")
	}
}

type testSink func([]byte)

func (f testSink) OnPacket(p []byte) { f(p) }

func TestParseShareThenStartRejectsUnknown(t *testing.T) {
	e := NewEngine()
	y := "token: x\noutbounds:\n  - name: a\n    type: ikev2\n    url: https://x\n"
	if err := e.Start(y); err == nil || !strings.Contains(err.Error(), "unknown") {
		t.Fatalf("%v", err)
	}
}

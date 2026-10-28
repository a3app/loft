package transport

import (
	"bytes"
	"context"
	"io"
	"net"
	"strings"
	"testing"
	"time"

	"github.com/a3app/loft/internal/mux"
	"github.com/a3app/loft/internal/testcfg"
)

func TestDriveEventLine(t *testing.T) {
	var buf bytes.Buffer
	d := NewDrive(&buf)
	d.Event("TICK", "if=en0", "ping=ok/12ms")
	s := buf.String()
	if !strings.Contains(s, " TICK if=en0 ping=ok/12ms\n") {
		t.Fatal(s)
	}
	if !strings.Contains(s, "T") || !strings.HasSuffix(s, "\n") {
		t.Fatal(s)
	}
}

func TestFrontHostPort(t *testing.T) {
	if g := frontHostPort(testcfg.FrontHTTPS()); g != testcfg.FrontAddr() {
		t.Fatal(g)
	}
	if g := frontHostPort(testcfg.FrontWSS()); g != testcfg.FrontAddr() {
		t.Fatal(g)
	}
	if g := frontHostPort(testcfg.CDNHTTPS()); g != testcfg.CDN()+":443" {
		t.Fatal(g)
	}
}

func TestMergePathFires(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	a := make(chan struct{}, 1)
	b := make(chan struct{}, 1)
	out := MergePath(ctx, a, b)
	b <- struct{}{}
	select {
	case <-out:
	case <-ctx.Done():
		t.Fatal("no merge")
	}
}

func TestOverlayStallQuietPing(t *testing.T) {
	if overlayStall(true, false, false, true) {
		t.Fatal("recent bytes")
	}
	if !overlayStall(true, false, false, false) {
		t.Fatal("quiet ping")
	}
	if !overlayStall(false, true, true, true) {
		t.Fatal("radio+node")
	}
}

func TestNoteStallFiresAfterPersist(t *testing.T) {
	d := NewDrive(io.Discard)
	if d.noteStall(true) || d.noteStall(true) {
		t.Fatal("early fire")
	}
	if !d.noteStall(true) {
		t.Fatal("need persist")
	}
	if d.noteStall(true) {
		t.Fatal("cooldown")
	}
	d.fails = 0
	if d.noteStall(false) {
		t.Fatal("clear")
	}
	if d.fails != 0 {
		t.Fatal(d.fails)
	}
}

func TestDriveMs(t *testing.T) {
	if driveMs(1500*time.Millisecond) != "1500ms" {
		t.Fatal(driveMs(1500 * time.Millisecond))
	}
}

// TestWatchStallSleepGapFires is a lid-closed laptop: the previous tick
// was 60 s ago on the wall clock, keepalive is 15 s, so two Ping intervals
// were missed and WatchStall must reconnect on this tick, not after three
// 2.5 s mux Ping timeouts.
func TestWatchStallSleepGapFires(t *testing.T) {
	d := NewDrive(io.Discard)
	d.tickUnix = time.Now().UnixMilli() - 60_000
	visKeepaliveMS.Store(15_000)
	t.Cleanup(func() { visKeepaliveMS.Store(0) })
	ms := mux.NewClient()
	h := &Holder{}
	h.Set(&sessDialer{sess: ms, done: make(chan struct{})})
	ch := make(chan struct{}, 1)
	d.tick(context.Background(), h, ch)
	select {
	case <-ch:
	default:
		t.Fatal("sleep gap did not reconnect")
	}
}

func TestWatchStallNilDriveFiresOnClosedMux(t *testing.T) {
	old := stallTick
	stallTick = 15 * time.Millisecond
	t.Cleanup(func() { stallTick = old })

	ms := mux.NewClient()
	ms.Close()
	h := &Holder{}
	h.Set(&sessDialer{sess: ms, done: make(chan struct{})})
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	ch := WatchStall(ctx, h, nil)
	select {
	case <-ch:
	case <-ctx.Done():
		t.Fatal("no mux stall")
	}
}

type sessDialer struct {
	sess *mux.Session
	done chan struct{}
	park int
	cls  int
}

func (s *sessDialer) Open(string) (net.Conn, error) { return nil, net.ErrClosed }
func (s *sessDialer) Session() *mux.Session         { return s.sess }
func (s *sessDialer) Done() <-chan struct{}         { return s.done }
func (s *sessDialer) Park() error                   { s.park++; return nil }
func (s *sessDialer) Close() error {
	s.cls++
	if s.sess != nil {
		s.sess.Close()
	}
	select {
	case <-s.done:
	default:
		close(s.done)
	}
	return nil
}

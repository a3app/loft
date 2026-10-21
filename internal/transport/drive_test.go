package transport

import (
	"bytes"
	"context"
	"io"
	"strings"
	"testing"
	"time"

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

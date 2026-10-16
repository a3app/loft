package transport

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"
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
	if g := frontHostPort("https://loft.iqos.help:444"); g != "loft.iqos.help:444" {
		t.Fatal(g)
	}
	if g := frontHostPort("wss://loft.iqos.help:444/api/v1/sync"); g != "loft.iqos.help:444" {
		t.Fatal(g)
	}
	if g := frontHostPort("https://cdn.iqos.help"); g != "cdn.iqos.help:443" {
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

func TestDriveMs(t *testing.T) {
	if driveMs(1500*time.Millisecond) != "1500ms" {
		t.Fatal(driveMs(1500 * time.Millisecond))
	}
}

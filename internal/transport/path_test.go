package transport

// Path watch: IP-only egress key, fire on address change, ignore ephemeral port.

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/a3app/loft/internal/protect"
)

// TestHostOnlyStripsPort keeps WatchPath keyed on IPv4, not the UDP source port.
func TestIfaceByIPLoopback(t *testing.T) {
	if n := ifaceByIP("127.0.0.1"); n == "" {
		t.Fatal("loopback iface")
	}
}

func TestParseHexLEGateway(t *testing.T) {
	b, err := parseHexLE("0100A8C0")
	if err != nil {
		t.Fatal(err)
	}
	ip := net.IPv4(b[0], b[1], b[2], b[3]).String()
	if ip != "192.168.0.1" {
		t.Fatal(ip)
	}
}

func TestPhysicalAddrUsesBind(t *testing.T) {
	if err := protect.Bind("lo0"); err != nil {
		t.Skip(err)
	}
	t.Cleanup(func() { _ = protect.Bind("") })
	got := physicalAddr()
	if got != "127.0.0.1" {
		t.Fatal(got)
	}
}

func TestOverlaySrc(t *testing.T) {
	if !overlaySrc("10.8.0.2") {
		t.Fatal("overlay")
	}
	if !overlaySrc("169.254.1.1") {
		t.Fatal("linklocal")
	}
	if overlaySrc("172.20.10.7") {
		t.Fatal("hotspot")
	}
}

func TestRadioUpRefused(t *testing.T) {
	if !radioUp(nil) {
		t.Fatal("ok")
	}
	if !radioUp(fmt.Errorf("dial tcp 172.20.10.1:80: connection refused")) {
		t.Fatal("refused")
	}
	if radioUp(fmt.Errorf("dial tcp: no route to host")) {
		t.Fatal("no route")
	}
}

func TestHostOnlyStripsPort(t *testing.T) {
	if hostOnly("10.0.0.1:54321") != "10.0.0.1" {
		t.Fatal(hostOnly("10.0.0.1:54321"))
	}
	if hostOnly("10.0.0.1") != "10.0.0.1" {
		t.Fatal(hostOnly("10.0.0.1"))
	}
}

// TestWatchPathFiresOnIPChange ignores a stable IP and signals a new one.
func TestWatchPathFiresOnIPChange(t *testing.T) {
	var n atomic.Int32
	oldProbe, oldTick := pathProbe, pathTick
	t.Cleanup(func() {
		pathProbe = oldProbe
		pathTick = oldTick
	})
	pathTick = 15 * time.Millisecond
	pathProbe = func() string {
		if n.Load() >= 6 {
			return "10.9.0.2"
		}
		n.Add(1)
		return "10.9.0.1"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ch := WatchPath(ctx)
	select {
	case <-ch:
	case <-ctx.Done():
		t.Fatal("no path event")
	}
	select {
	case <-ch:
		t.Fatal("second event on a stable IP")
	case <-time.After(80 * time.Millisecond):
	}
}

// TestWatchPathIgnoresOverlaySrc: TUN 0/1 makes egress 10.8/24; that is not a path change.
func TestWatchPathIgnoresOverlaySrc(t *testing.T) {
	var n atomic.Int32
	oldProbe, oldTick := pathProbe, pathTick
	t.Cleanup(func() {
		pathProbe = oldProbe
		pathTick = oldTick
	})
	pathTick = 15 * time.Millisecond
	pathProbe = func() string {
		if n.Add(1) >= 4 {
			return "10.8.0.2"
		}
		return "10.9.0.1"
	}
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	ch := WatchPath(ctx)
	select {
	case <-ch:
		t.Fatal("overlay src must not fire")
	case <-ctx.Done():
	}
}

// After one "permission denied" (Android 11+ netlink), interface lookups stop
// trying and answer "" at once; other errors do not latch.
func TestIfaceDeniedLatch(t *testing.T) {
	ifaceDenied.Store(false)
	t.Cleanup(func() { ifaceDenied.Store(false) })
	noteDenied(errors.New("no such interface"))
	if ifaceDenied.Load() {
		t.Fatal("plain error latched")
	}
	noteDenied(&os.SyscallError{Syscall: "netlinkrib", Err: syscall.EACCES})
	if !ifaceDenied.Load() {
		t.Fatal("EACCES not latched")
	}
	if ifaceIPv4("lo") != "" || ifaceByIP("127.0.0.1") != "" {
		t.Fatal("lookup ran after denial")
	}
}

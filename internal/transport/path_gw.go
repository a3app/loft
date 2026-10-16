package transport

// Default IPv4 gateway for drive-debug PATH samples.

import (
	"context"
	"encoding/binary"
	"net"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/a3app/loft/internal/protect"
)

var (
	gwMu   sync.Mutex
	gwAt   time.Time
	gwLast string
	gwIf   string
)

// defaultGateway is the IPv4 default route on -if when set, cached ~2s.
func defaultGateway() string {
	name := protect.BoundName()
	gwMu.Lock()
	if time.Since(gwAt) < 2*time.Second && gwLast != "" && gwIf == name {
		g := gwLast
		gwMu.Unlock()
		return g
	}
	gwMu.Unlock()
	var g string
	switch runtime.GOOS {
	case "darwin":
		if name != "" {
			g = darwinGatewayIf(name)
		}
		if g == "" || g == "127.0.0.1" {
			g = darwinGateway()
		}
	case "linux":
		g = linuxGateway()
	}
	if g == "127.0.0.1" {
		g = ""
	}
	if g != "" {
		gwMu.Lock()
		gwAt, gwLast, gwIf = time.Now(), g, name
		gwMu.Unlock()
	}
	return g
}

func darwinGateway() string {
	return darwinRoute("route", "-n", "get", "-inet", "default")
}

func darwinGatewayIf(name string) string {
	return darwinRoute("route", "-n", "get", "-inet", "-ifscope", name, "default")
}

func darwinRoute(args ...string) string {
	ctx, cancel := context.WithTimeout(context.Background(), 400*time.Millisecond)
	defer cancel()
	out, err := exec.CommandContext(ctx, args[0], args[1:]...).Output()
	if err != nil {
		return ""
	}
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "gateway:") {
			g := strings.TrimSpace(strings.TrimPrefix(line, "gateway:"))
			if net.ParseIP(g) != nil {
				return g
			}
		}
	}
	return ""
}

func linuxGateway() string {
	b, err := os.ReadFile("/proc/net/route")
	if err != nil {
		return ""
	}
	for i, line := range strings.Split(string(b), "\n") {
		if i == 0 {
			continue
		}
		f := strings.Fields(line)
		if len(f) < 3 || f[1] != "00000000" {
			continue
		}
		raw, err := parseHexLE(f[2])
		if err != nil {
			continue
		}
		ip := net.IPv4(raw[0], raw[1], raw[2], raw[3])
		if ip != nil && !ip.IsUnspecified() {
			return ip.String()
		}
	}
	return ""
}

func parseHexLE(s string) ([4]byte, error) {
	var out [4]byte
	var u uint32
	for _, c := range []byte(s) {
		u <<= 4
		switch {
		case c >= '0' && c <= '9':
			u |= uint32(c - '0')
		case c >= 'a' && c <= 'f':
			u |= uint32(c - 'a' + 10)
		case c >= 'A' && c <= 'F':
			u |= uint32(c - 'A' + 10)
		default:
			return out, errGateway
		}
	}
	binary.LittleEndian.PutUint32(out[:], u)
	return out, nil
}

type gwError struct{}

func (gwError) Error() string { return "gateway" }

var errGateway gwError

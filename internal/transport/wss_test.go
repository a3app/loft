package transport

// WebSocket echo and unknown-front Start tests.

import (
	"context"
	"io"
	"net"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/a3app/loft/internal/config"
)

// TestWSSEcho opens a mux TCP echo through the WebSocket front.
func TestWSSEcho(t *testing.T) {
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		for {
			c, err := echo.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_, _ = io.Copy(c, c)
			}(c)
		}
	}()

	const token = "test-token-please-change"
	hs := httptest.NewServer(&WSSServer{Token: []byte(token)})
	defer hs.Close()
	wsURL := "ws" + strings.TrimPrefix(hs.URL, "http")

	w := NewWSS(token, config.Outbound{
		Type:        "ws",
		URL:         wsURL,
		Fingerprint: "go",
		Insecure:    true,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := w.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	conn, err := w.Open(echo.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write([]byte("ping-cdn")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 8)
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != "ping-cdn" {
		t.Fatalf("got %q", buf)
	}
}

// TestStartUnknown rejects an outbound type that is not a known front.
func TestStartUnknown(t *testing.T) {
	_, err := Start(context.Background(), "t", config.Outbound{Type: "nope", URL: "https://x"})
	if err == nil {
		t.Fatal("expected error")
	}
}

// TestWSSThroughput moves 2 MiB each way over WebSocket. The live Mac GUI
// uses -type ws; GET inflight does not apply. Localhost should be far above
// the Yandex ZeroTier bar (~34 Mbit).
func TestWSSThroughput(t *testing.T) {
	src, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	const nbyte = 2 << 20
	go func() {
		c, err := src.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		_, _ = c.Write(make([]byte, nbyte))
		_, _ = io.Copy(io.Discard, c)
	}()

	const token = "test-token-please-change"
	hs := httptest.NewServer(&WSSServer{Token: []byte(token)})
	defer hs.Close()
	wsURL := "ws" + strings.TrimPrefix(hs.URL, "http")

	w := NewWSS(token, config.Outbound{
		Type:        "ws",
		URL:         wsURL,
		Fingerprint: "go",
		Insecure:    true,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := w.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer w.Close()

	down, err := w.Open(src.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer down.Close()
	_ = down.SetDeadline(time.Now().Add(20 * time.Second))
	got := make([]byte, nbyte)
	t0 := time.Now()
	if _, err := io.ReadFull(down, got); err != nil {
		t.Fatal(err)
	}
	downDT := time.Since(t0)
	downM := float64(nbyte) / downDT.Seconds() * 8 / 1e6

	sink, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()
	gotn := make(chan int64, 1)
	go func() {
		c, err := sink.Accept()
		if err != nil {
			gotn <- -1
			return
		}
		defer c.Close()
		n, _ := io.Copy(io.Discard, c)
		gotn <- n
	}()
	up, err := w.Open(sink.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	_ = up.SetDeadline(time.Now().Add(20 * time.Second))
	t1 := time.Now()
	if _, err := up.Write(make([]byte, nbyte)); err != nil {
		t.Fatal(err)
	}
	_ = up.Close()
	nup := <-gotn
	upDT := time.Since(t1)
	if nup != int64(nbyte) {
		t.Fatalf("origin got %d of %d", nup, nbyte)
	}
	upM := float64(nbyte) / upDT.Seconds() * 8 / 1e6
	t.Logf("ws 2MiB down %.2f Mbit/s in %s; up %.2f Mbit/s in %s", downM, downDT, upM, upDT)
	if downM < 80 {
		t.Fatalf("ws download %.2f Mbit/s < 80", downM)
	}
	if upM < 80 {
		t.Fatalf("ws upload %.2f Mbit/s < 80", upM)
	}
}

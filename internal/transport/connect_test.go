package transport

// CONNECT splice tests for HTTP/1.1 ServeSplice and GET mux=off.

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/a3app/loft/internal/config"
	ecrypto "github.com/a3app/loft/internal/crypto"
	"github.com/a3app/loft/internal/wire"
)

const spliceToken = "test-token-please-change"

func spliceCookie(t *testing.T, token string) string {
	t.Helper()
	blob, _, _, err := ecrypto.MakeAuth([]byte(token))
	if err != nil {
		t.Fatal(err)
	}
	return "session=" + wire.EncodeUplink(blob, 0)
}

func spliceHandler() http.Handler {
	return &SpliceServer{Token: []byte(spliceToken), Seen: ecrypto.NewNonces()}
}

// boolp returns a *bool for Outbound.Mux.
func boolp(v bool) *bool { return &v }

// echoTCP listens on 127.0.0.1 and echoes bytes until close.
func echoTCP(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				buf := make([]byte, 32*1024)
				for {
					n, err := c.Read(buf)
					if n > 0 {
						if _, werr := c.Write(buf[:n]); werr != nil {
							return
						}
					}
					if err != nil {
						return
					}
				}
			}(c)
		}
	}()
	return ln
}

// TestGETMuxOffNeedsConnectCap fails Start when mux is off and the node has no connect cap.
func TestGETMuxOffNeedsConnectCap(t *testing.T) {
	gs := NewGETServer(spliceToken, "/static/v3/build")
	gs.Info.Token = []byte(spliceToken)
	gs.Info.Caps = wire.NodeCaps &^ wire.CapConnect
	ts := httptest.NewServer(http.HandlerFunc(gs.ServeHTTP))
	defer ts.Close()
	g, err := NewGET(spliceToken, config.Outbound{
		Type: "get", URL: ts.URL, Path: "/static/v3/build",
		Fingerprint: "go", HeaderBudget: 4096, Inflight: 1, Insecure: true,
		Mux: boolp(false),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	err = g.Start(ctx)
	if err == nil || err.Error() != "node requires mux" {
		t.Fatalf("want node requires mux, got %v", err)
	}
}

func stubSpliceDial(t *testing.T, echo net.Listener) {
	t.Helper()
	old := spliceDial
	spliceDial = func(network, addr string, d time.Duration) (net.Conn, error) {
		return net.DialTimeout("tcp", echo.Addr().String(), d)
	}
	t.Cleanup(func() { spliceDial = old })
}

// TestServeSpliceRejectsNoAuth is 404 without 0x02.
func TestServeSpliceRejectsNoAuth(t *testing.T) {
	ts := httptest.NewServer(spliceHandler())
	defer ts.Close()
	c, err := net.DialTimeout("tcp", ts.Listener.Addr().String(), 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := fmt.Fprintf(c, "CONNECT 1.1.1.1:80 HTTP/1.1\r\nHost: 1.1.1.1:80\r\n\r\n"); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status %d", resp.StatusCode)
	}
}

// TestServeSpliceRejectsLoopback is 403 for 127.0.0.1 even with 0x02.
func TestServeSpliceRejectsLoopback(t *testing.T) {
	shutLoopback(t)
	ts := httptest.NewServer(spliceHandler())
	defer ts.Close()
	c, err := net.DialTimeout("tcp", ts.Listener.Addr().String(), 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := fmt.Fprintf(c, "CONNECT 127.0.0.1:80 HTTP/1.1\r\nHost: 127.0.0.1:80\r\nCookie: %s\r\n\r\n", spliceCookie(t, spliceToken)); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status %d", resp.StatusCode)
	}
}

// TestServeSpliceRejectsMetadata is 403 for 169.254.169.254 (link-local).
func TestServeSpliceRejectsMetadata(t *testing.T) {
	ts := httptest.NewServer(spliceHandler())
	defer ts.Close()
	c, err := net.DialTimeout("tcp", ts.Listener.Addr().String(), 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := fmt.Fprintf(c, "CONNECT 169.254.169.254:80 HTTP/1.1\r\nHost: 169.254.169.254:80\r\nCookie: %s\r\n\r\n", spliceCookie(t, spliceToken)); err != nil {
		t.Fatal(err)
	}
	resp, err := http.ReadResponse(bufio.NewReader(c), nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusForbidden {
		t.Fatalf("status %d", resp.StatusCode)
	}
}

// TestServeSpliceHTTP1 splices HTTP/1.1 CONNECT through ServeSplice.
func TestServeSpliceHTTP1(t *testing.T) {
	echo := echoTCP(t)
	stubSpliceDial(t, echo)
	ts := httptest.NewServer(spliceHandler())
	defer ts.Close()

	c, err := net.DialTimeout("tcp", ts.Listener.Addr().String(), 3*time.Second)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(5 * time.Second))
	cookie := spliceCookie(t, spliceToken)
	// Pipeline the first payload with CONNECT so leftover bytes sit in the
	// hijacked bufio.Reader. io.Copy of that reader until EOF deadlocks.
	if _, err := fmt.Fprintf(c, "CONNECT 1.1.1.1:80 HTTP/1.1\r\nHost: 1.1.1.1:80\r\nCookie: %s\r\n\r\nping-h1", cookie); err != nil {
		t.Fatal(err)
	}
	br := bufio.NewReader(c)
	resp, err := http.ReadResponse(br, nil)
	if err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status %d", resp.StatusCode)
	}
	buf := make([]byte, 7)
	if _, err := io.ReadFull(br, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != "ping-h1" {
		t.Fatalf("got %q", buf)
	}
}

// TestGETConnectSplice opens TCP over GET HTTP/2 CONNECT when mux is off.
func TestGETConnectSplice(t *testing.T) {
	echo := echoTCP(t)
	stubSpliceDial(t, echo)

	gs := NewGETServer(spliceToken, "/static/v3/build")
	gs.Info.Token = []byte(spliceToken)
	gs.Info.Caps = wire.NodeCaps
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodConnect {
			ServeSplice(w, r, []byte(spliceToken), gs.Seen)
			return
		}
		gs.ServeHTTP(w, r)
	})
	ts := httptest.NewUnstartedServer(h)
	ts.EnableHTTP2 = true
	ts.TLS = &tls.Config{NextProtos: []string{"h2", "http/1.1"}}
	ts.StartTLS()
	defer ts.Close()

	g, err := NewGET(spliceToken, config.Outbound{
		Type: "get", URL: ts.URL, Path: "/static/v3/build",
		Fingerprint: "go", HeaderBudget: 4096, Inflight: 1, Insecure: true,
		Mux: boolp(false),
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	if err := g.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer g.Close()

	conn, err := g.Open("1.1.1.1:80")
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.Write([]byte("ping-get")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 8)
	done := make(chan error, 1)
	go func() {
		_, err := io.ReadFull(conn, buf)
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("CONNECT splice read timeout")
	}
	if string(buf) != "ping-get" {
		t.Fatalf("got %q", buf)
	}
}

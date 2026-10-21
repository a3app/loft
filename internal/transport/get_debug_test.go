package transport

// Debug-client GET: same dial path as loft-front / loft CLI (NewGET).

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/a3app/loft/internal/config"
	ecrypto "github.com/a3app/loft/internal/crypto"
	"github.com/a3app/loft/internal/wire"
)

type getCapture struct {
	mu      sync.Mutex
	auth    int
	rid     int
	cookie  int
	newPath int
	hexPath int
}

func (c *getCapture) wrap(next http.Handler, edgeCookie bool) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if edgeCookie {
			cur := append([]string(nil), r.Header.Values("Cookie")...)
			r.Header["Cookie"] = append([]string{"edge=cdn"}, cur...)
		}
		if r.Header.Get("X-Auth") != "" {
			c.mu.Lock()
			c.auth++
			c.mu.Unlock()
		}
		if r.Header.Get("X-Request-Id") != "" {
			c.mu.Lock()
			c.rid++
			c.mu.Unlock()
		}
		if strings.Contains(r.Header.Get("Cookie")+strings.Join(r.Header.Values("Cookie"), ";"), "session=") {
			c.mu.Lock()
			c.cookie++
			c.mu.Unlock()
		}
		sid, _ := parseGETPath("/static/v3/build", r.URL.Path)
		c.mu.Lock()
		if sid == "new" {
			c.newPath++
		} else if validGETSID(sid) {
			c.hexPath++
		}
		c.mu.Unlock()
		next.ServeHTTP(w, r)
	})
}

func (c *getCapture) checkNewClient(t *testing.T) {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.auth != 0 {
		t.Fatalf("X-Auth count %d", c.auth)
	}
	if c.rid != 0 {
		t.Fatalf("X-Request-Id count %d", c.rid)
	}
	if c.cookie == 0 {
		t.Fatal("no Cookie session=")
	}
	if c.newPath != 0 {
		t.Fatalf("path new count %d", c.newPath)
	}
	if c.hexPath == 0 {
		t.Fatal("no 24-hex sid in path")
	}
}

func debugEchoTCP(t *testing.T) net.Listener {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_, _ = io.Copy(c, c)
			}(c)
		}
	}()
	return ln
}

func TestGETDebugClientHeadersAndEcho(t *testing.T) {
	const token = "test-token-please-change"
	echo := debugEchoTCP(t)
	gs := NewGETServer(token, "/static/v3/build")
	cap := &getCapture{}
	ts := httptest.NewServer(cap.wrap(gs, true))
	t.Cleanup(ts.Close)
	g, err := NewGET(token, config.Outbound{
		Type: "get", URL: ts.URL, Path: "/static/v3/build",
		Fingerprint: "chrome", HeaderBudget: 4096, Inflight: 4, Insecure: true,
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
	conn, err := g.Open(echo.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(8 * time.Second))
	if _, err := conn.Write([]byte("debug-get")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 9)
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != "debug-get" {
		t.Fatalf("echo %q", buf)
	}
	cap.checkNewClient(t)
}

func TestGETMuxIgnoresXRequestId(t *testing.T) {
	const token = "test-token-please-change"
	echo := debugEchoTCP(t)
	gs := NewGETServer(token, "/static/v3/build")
	ts := httptest.NewServer(http.HandlerFunc(gs.ServeHTTP))
	t.Cleanup(ts.Close)
	g, err := NewGET(token, config.Outbound{
		Type: "get", URL: ts.URL, Path: "/static/v3/build",
		Fingerprint: "go", HeaderBudget: 4096, Inflight: 1, Insecure: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := g.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	leave, err := wire.EncodeBatch([]wire.Frame{{Cmd: wire.CmdLeave}})
	if err != nil {
		t.Fatal(err)
	}
	blob, err := ecrypto.Seal(g.key, leave, []byte(g.sid))
	if err != nil {
		t.Fatal(err)
	}
	url := ts.URL + "/static/v3/build/" + g.sid + "/99.js"
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("X-Request-Id", wire.EncodeUplink(blob, 0))
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("empty Cookie poll %d", resp.StatusCode)
	}
	conn, err := g.Open(echo.Addr().String())
	if err != nil {
		t.Fatalf("Leave in X-Request-Id ingested: %v", err)
	}
	conn.Close()
}

func TestGETEchoSurvivesH2Rotate(t *testing.T) {
	const token = "test-token-please-change"
	echo := debugEchoTCP(t)
	gs := NewGETServer(token, "/static/v3/build")
	ts := httptest.NewServer(http.HandlerFunc(gs.ServeHTTP))
	t.Cleanup(ts.Close)
	g, err := NewGET(token, config.Outbound{
		Type: "get", URL: ts.URL, Path: "/static/v3/build",
		Fingerprint: "go", HeaderBudget: 4096, Inflight: 2, Insecure: true,
		H2MaxAgeMS: 15000,
	})
	if err != nil {
		t.Fatal(err)
	}
	if g.h2MaxAge != 15*time.Second {
		t.Fatalf("max-age %s", g.h2MaxAge)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	if err := g.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	old := g.pickClient()
	g.poolMu.Lock()
	for i := range g.born {
		g.born[i] = time.Now().Add(-time.Hour)
	}
	g.poolMu.Unlock()
	_ = g.pickClient()
	conn, err := g.Open(echo.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(8 * time.Second))
	if _, err := conn.Write([]byte("rotate")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 6)
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != "rotate" {
		t.Fatalf("echo %q", buf)
	}
	g.poolMu.Lock()
	nDrain := len(g.drain)
	g.poolMu.Unlock()
	if nDrain == 0 && g.pickClient() == old {
		t.Fatal("no rotate")
	}
}

func TestGETHiddenXSessionStillOpens(t *testing.T) {
	const token = "test-token-please-change"
	var n atomic.Int32
	gs := NewGETServer(token, "/static/v3/build")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := httptest.NewRecorder()
		gs.ServeHTTP(rec, r)
		for k, vv := range rec.Header() {
			if k == "X-Session" {
				n.Add(1)
				continue
			}
			w.Header()[k] = vv
		}
		w.WriteHeader(rec.Code)
		_, _ = w.Write(rec.Body.Bytes())
	}))
	t.Cleanup(ts.Close)
	g, err := NewGET(token, config.Outbound{
		Type: "get", URL: ts.URL, Path: "/static/v3/build",
		Fingerprint: "go", HeaderBudget: 4096, Inflight: 1, Insecure: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := g.Start(ctx); err != nil {
		t.Fatal(err)
	}
	g.Close()
	if n.Load() == 0 {
		t.Fatal("node did not send X-Session (echo expected)")
	}
}

package transport

// GET Rebind: egress change keeps sid, mux, seq. No new 0x02.

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/a3app/loft/internal/config"
	"github.com/a3app/loft/internal/mux"
)

func TestGenericTripTimeout(t *testing.T) {
	var g GET
	if g.tripTimeout(false) != 15*time.Second {
		t.Fatal(g.tripTimeout(false))
	}
	if g.tripTimeout(true) != 20*time.Second {
		t.Fatal("auth")
	}
}

func TestGetGiveUp(t *testing.T) {
	t0 := time.Now()
	if getGiveUp(1, t0) {
		t.Fatal("first retry")
	}
	if getGiveUp(getRetryMax, t0) {
		t.Fatal("at cap")
	}
	if !getGiveUp(getRetryMax+1, t0) {
		t.Fatal("past cap")
	}
	if !getGiveUp(1, t0.Add(-getRetryWindow-time.Second)) {
		t.Fatal("past window")
	}
}

func TestRebindFrontRequiresLiveGET(t *testing.T) {
	if rebindFront(nil) {
		t.Fatal("nil")
	}
	stub := &plainDialer{done: make(chan struct{})}
	if rebindFront(stub) {
		t.Fatal("ws-shaped dialer")
	}
	close(stub.done)
	if rebindFront(stub) {
		t.Fatal("dead stub")
	}
}

// plainDialer is a Dialer without Rebind (ws/h2/quic/h3 shape).
type plainDialer struct {
	done chan struct{}
}

func (s *plainDialer) Open(string) (net.Conn, error) { return nil, nil }
func (s *plainDialer) Session() *mux.Session         { return nil }
func (s *plainDialer) Done() <-chan struct{}         { return s.done }
func (s *plainDialer) Close() error {
	select {
	case <-s.done:
	default:
		close(s.done)
	}
	return nil
}

func TestGETRebindKeepsSidAndEcho(t *testing.T) {
	const token = "test-token-please-change"
	echo := echoTCP(t)
	gs := NewGETServer(token, "/static/v3/build")
	var hs atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if cookieHandshake(r) {
			hs.Add(1)
		}
		gs.ServeHTTP(w, r)
	}))
	t.Cleanup(ts.Close)
	g, err := NewGET(token, config.Outbound{
		Type: "get", URL: ts.URL, Path: "/static/v3/build",
		Fingerprint: "go", HeaderBudget: 4096, Inflight: 2, Insecure: true,
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
	if hs.Load() != 1 {
		t.Fatalf("handshake %d", hs.Load())
	}
	sid := g.sid
	old := g.pickClient()
	conn, err := g.Open(echo.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write([]byte("before")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 6)
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != "before" {
		t.Fatalf("echo %q", buf)
	}
	if err := g.Rebind(); err != nil {
		t.Fatal(err)
	}
	if g.sid != sid {
		t.Fatalf("sid %s -> %s", sid, g.sid)
	}
	if hs.Load() != 1 {
		t.Fatalf("handshake after rebind %d", hs.Load())
	}
	if g.pickClient() == old {
		t.Fatal("pool not replaced")
	}
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write([]byte("after!")); err != nil {
		t.Fatal(err)
	}
	buf = make([]byte, 6)
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != "after!" {
		t.Fatalf("echo %q", buf)
	}
}

func TestSupervisePathRebindsGET(t *testing.T) {
	const token = "test-token-please-change"
	echo := echoTCP(t)
	gs := NewGETServer(token, "/static/v3/build")
	var hs atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if cookieHandshake(r) {
			hs.Add(1)
		}
		gs.ServeHTTP(w, r)
	}))
	t.Cleanup(ts.Close)
	off := false
	cfg := &config.ClientConfig{
		Token:  token,
		Select: "auto",
		P2P:    &off,
		Outbounds: []config.Outbound{{
			Name: "n", Type: "get", URL: ts.URL, Path: "/static/v3/build",
			Fingerprint: "go", HeaderBudget: 4096, Inflight: 2, Insecure: true,
		}},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	path := make(chan struct{}, 2)
	ready := make(chan *GET, 1)
	go func() {
		_ = SuperviseOn(ctx, cfg, path, func(d Dialer, _ config.Outbound) {
			g, ok := d.(*GET)
			if !ok {
				t.Error("want GET")
				return
			}
			select {
			case ready <- g:
			default:
			}
		})
	}()
	var g *GET
	select {
	case g = <-ready:
	case <-ctx.Done():
		t.Fatal("start")
	}
	if hs.Load() != 1 {
		t.Fatalf("handshake %d", hs.Load())
	}
	sid := g.sid
	old := g.pickClient()
	rot0 := rotateN.Load()
	path <- struct{}{}
	deadline := time.Now().Add(2 * time.Second)
	rebound := false
	for time.Now().Before(deadline) {
		if hs.Load() != 1 {
			t.Fatalf("handshake after path %d", hs.Load())
		}
		if rotateN.Load() != rot0 {
			t.Fatalf("rotate after path")
		}
		if g.sid != sid {
			t.Fatalf("sid %s -> %s", sid, g.sid)
		}
		if g.pickClient() != old {
			rebound = true
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !rebound {
		t.Fatal("pool not replaced")
	}
	if hs.Load() != 1 {
		t.Fatalf("handshake after path %d", hs.Load())
	}
	if rotateN.Load() != rot0 {
		t.Fatalf("rotate after path")
	}
	conn, err := g.Open(echo.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write([]byte("rebind")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 6)
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != "rebind" {
		t.Fatalf("echo %q", buf)
	}
}

func TestGETRebindAfterCloseFails(t *testing.T) {
	const token = "test-token-please-change"
	gs := NewGETServer(token, "/static/v3/build")
	ts := httptest.NewServer(http.HandlerFunc(gs.ServeHTTP))
	t.Cleanup(ts.Close)
	g, err := NewGET(token, config.Outbound{
		Type: "get", URL: ts.URL, Path: "/static/v3/build",
		Fingerprint: "go", HeaderBudget: 4096, Inflight: 2, Insecure: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := g.Start(ctx); err != nil {
		t.Fatal(err)
	}
	_ = g.Close()
	if err := g.Rebind(); err == nil {
		t.Fatal("expected error")
	}
	if rebindFront(g) {
		t.Fatal("dead GET")
	}
}

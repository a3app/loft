package transport

// GET Rebind: egress change keeps sid, mux, seq. No new 0x02.

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/a3app/loft/internal/config"
	"github.com/a3app/loft/internal/mux"
	"github.com/a3app/loft/internal/wire"
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

func TestSuperviseGET404ResumesFreshHandshake(t *testing.T) {
	t.Cleanup(clearClientHold)
	const token = "test-token-please-change"
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
	ready := make(chan *GET, 4)
	go func() {
		_ = SuperviseOn(ctx, cfg, nil, func(d Dialer, _ config.Outbound) {
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
	var g1 *GET
	select {
	case g1 = <-ready:
	case <-ctx.Done():
		t.Fatal("start")
	}
	sid := g1.sid
	if sid == "" || hs.Load() != 1 {
		t.Fatalf("sid=%q hs=%d", sid, hs.Load())
	}
	gs.drop(sid)
	var g2 *GET
	select {
	case g2 = <-ready:
	case <-ctx.Done():
		t.Fatal("resume")
	}
	if hs.Load() < 2 {
		t.Fatalf("handshakes %d, parked dead sid", hs.Load())
	}
	if g2.sid == "" || g2.sid == sid {
		t.Fatalf("sid %q reused %q", g2.sid, sid)
	}
}

func TestGETDiesWhenMuxCloses(t *testing.T) {
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
	defer g.Close()
	g.sess.Close()
	select {
	case <-g.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("zombie GET after mux close")
	}
}

func TestFatalFront(t *testing.T) {
	if !fatalFront(wire.Fail(wire.AuthBadKey, "")) {
		t.Fatal("bad key")
	}
	if !fatalFront(fmt.Errorf("loft: %w", wire.Fail(wire.AuthRevoked, ""))) {
		t.Fatal("wrapped")
	}
	if fatalFront(fmt.Errorf("GET front: status 404")) {
		t.Fatal("404")
	}
	if fatalFront(wire.Fail(wire.AuthLimit, "")) {
		t.Fatal("limit retries")
	}
}

func TestGETWrongTokenIsAuthBadKey(t *testing.T) {
	gs := NewGETServer("test-token-please-change", "/static/v3/build")
	ts := httptest.NewServer(http.HandlerFunc(gs.ServeHTTP))
	t.Cleanup(ts.Close)
	g, err := NewGET("revoked-key", config.Outbound{
		Type: "get", URL: ts.URL, Path: "/static/v3/build",
		Fingerprint: "go", HeaderBudget: 4096, Inflight: 2, Insecure: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = g.Start(ctx)
	var we wire.Error
	if !errors.As(err, &we) || we.Code != wire.AuthBadKey {
		t.Fatalf("%v", err)
	}
}

func TestGETHandshake404IsAuthBadKey(t *testing.T) {
	ts := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(ts.Close)
	g, err := NewGET("revoked-key", config.Outbound{
		Type: "get", URL: ts.URL, Path: "/static/v3/build",
		Fingerprint: "go", HeaderBudget: 4096, Inflight: 2, Insecure: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	err = g.Start(ctx)
	var we wire.Error
	if !errors.As(err, &we) || we.Code != wire.AuthBadKey {
		t.Fatalf("%v", err)
	}
}

func TestStartSelectedFallsThroughGET404(t *testing.T) {
	dead := httptest.NewServer(http.NotFoundHandler())
	t.Cleanup(dead.Close)
	gs := NewGETServer("test-token-please-change", "/static/v3/build")
	live := httptest.NewServer(http.HandlerFunc(gs.ServeHTTP))
	t.Cleanup(live.Close)
	rotateN.Store(0)
	off := false
	cfg := &config.ClientConfig{
		Token:  "test-token-please-change",
		Select: "auto",
		P2P:    &off,
		Outbounds: []config.Outbound{
			{Name: "cdn", Type: "get", URL: dead.URL, Path: "/static/v3/build", Fingerprint: "go", HeaderBudget: 4096, Inflight: 2, Insecure: true},
			{Name: "node", Type: "get", URL: live.URL, Path: "/static/v3/build", Fingerprint: "go", HeaderBudget: 4096, Inflight: 2, Insecure: true},
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	d, o, err := StartSelected(ctx, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer d.Close()
	if o.Name != "node" {
		t.Fatalf("dialed %s", o.Name)
	}
}

func TestSuperviseAuthBadKeyStops(t *testing.T) {
	gs := NewGETServer("test-token-please-change", "/static/v3/build")
	ts := httptest.NewServer(http.HandlerFunc(gs.ServeHTTP))
	t.Cleanup(ts.Close)
	off := false
	cfg := &config.ClientConfig{
		Token:  "revoked-key",
		Select: "auto",
		P2P:    &off,
		Outbounds: []config.Outbound{{
			Name: "n", Type: "get", URL: ts.URL, Path: "/static/v3/build",
			Fingerprint: "go", HeaderBudget: 4096, Inflight: 2, Insecure: true,
		}},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	t0 := time.Now()
	err := SuperviseOn(ctx, cfg, nil, func(Dialer, config.Outbound) {
		t.Error("live")
	})
	if time.Since(t0) > time.Second {
		t.Fatal("retried")
	}
	var we wire.Error
	if !errors.As(err, &we) || we.Code != wire.AuthBadKey {
		t.Fatalf("%v", err)
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

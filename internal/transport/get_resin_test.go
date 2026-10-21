package transport

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/a3app/loft/internal/config"
	"github.com/a3app/loft/internal/mux"
	"github.com/a3app/loft/internal/wire"
)

func TestNoteAppliedMonotonic(t *testing.T) {
	g := &GET{}
	g.noteApplied(4)
	g.noteApplied(2)
	g.noteApplied(9)
	if g.applied.Load() != 9 {
		t.Fatalf("%d", g.applied.Load())
	}
}

func TestWithReportPrependsApplied(t *testing.T) {
	ss := &serverSess{nextSeq: 8, mux: mux.NewServer()}
	down := ss.withReport([]wire.Frame{{Cmd: wire.CmdPad, Payload: []byte{1}}})
	if len(down) != 2 || down[0].Cmd != wire.CmdReport {
		t.Fatalf("%+v", down)
	}
	a, ok := wire.ParseReport(down[0].Payload)
	if !ok || a != 7 {
		t.Fatalf("applied %d", a)
	}
}

func TestPushOrderedTakesReport(t *testing.T) {
	g := &GET{sess: mux.NewClient(), held: map[uint64][]wire.Frame{}}
	g.started.Store(true)
	g.nextSeq = 3
	g.pushOrdered(3, []wire.Frame{
		wire.ReportFrame(12),
		{Cmd: wire.CmdPad, Payload: []byte{1}},
	})
	if g.applied.Load() != 12 {
		t.Fatalf("applied %d", g.applied.Load())
	}
}

func TestMuxBusyIgnoresReport(t *testing.T) {
	if muxBusy([]wire.Frame{wire.ReportFrame(1), {Cmd: wire.CmdPad}}) {
		t.Fatal("report is not busy")
	}
	if !muxBusy([]wire.Frame{wire.ReportFrame(1), {Cmd: wire.CmdOpen}}) {
		t.Fatal("open is busy")
	}
}

func TestGETReportAppliedAfterEcho(t *testing.T) {
	echo := echoListener(t)
	const token = "test-token-please-change"
	gs := NewGETServer(token, "/static/v3/build")
	ts := httptest.NewServer(http.HandlerFunc(gs.ServeHTTP))
	defer ts.Close()
	g := startGET(t, ts.URL, 2)
	defer g.Close()
	conn, err := g.Open(echo.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write([]byte("ping-get")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 8)
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatal(err)
	}
	if g.applied.Load() == 0 {
		t.Fatal("no CmdReport applied")
	}
}

func TestGETOpenSurvivesFirstMux404(t *testing.T) {
	echo := echoListener(t)
	const token = "test-token-please-change"
	gs := NewGETServer(token, "/static/v3/build")
	var muxN atomic.Int32
	var firstMux, retryMux atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Auth") == "1" {
			gs.ServeHTTP(w, r)
			return
		}
		n := muxN.Add(1)
		if n == 1 {
			firstMux.Store(1)
			http.NotFound(w, r)
			return
		}
		retryMux.Store(1)
		gs.ServeHTTP(w, r)
	}))
	defer ts.Close()
	g := startGET(t, ts.URL, 2)
	defer g.Close()
	conn, err := g.Open(echo.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(8 * time.Second))
	time.Sleep(250 * time.Millisecond)
	if _, err := conn.Write([]byte("ping-get")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 8)
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatal(err)
	}
	if firstMux.Load() == 0 || retryMux.Load() == 0 {
		t.Fatalf("404=%d retry=%d", firstMux.Load(), retryMux.Load())
	}
	if g.applied.Load() == 0 {
		t.Fatal("no CmdReport after 404 retry")
	}
}

func TestGETBurst404DoesNotDie(t *testing.T) {
	const token = "test-token-please-change"
	gs := NewGETServer(token, "/static/v3/build")
	seen := map[string]struct{}{}
	var mu sync.Mutex
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Auth") == "1" {
			gs.ServeHTTP(w, r)
			return
		}
		mu.Lock()
		_, hit := seen[r.URL.Path]
		if !hit && len(seen) < 8 {
			seen[r.URL.Path] = struct{}{}
			mu.Unlock()
			http.NotFound(w, r)
			return
		}
		mu.Unlock()
		gs.ServeHTTP(w, r)
	}))
	defer ts.Close()
	g := startGET(t, ts.URL, 8)
	defer g.Close()
	time.Sleep(time.Second)
	select {
	case <-g.Done():
		t.Fatal("session died on first-attempt 404 burst")
	default:
	}
}

func TestGETHandshakeSetsOverlayIP(t *testing.T) {
	t.Cleanup(func() { RememberVis(wire.Status{}) })
	gs := NewGETServer("test-token-please-change", "/static/v3/build")
	ts := httptest.NewServer(http.HandlerFunc(gs.ServeHTTP))
	defer ts.Close()
	g := startGET(t, ts.URL, 1)
	defer g.Close()
	if OverlayIP() != "10.8.0.2" {
		t.Fatalf("overlay IP %q", OverlayIP())
	}
}

func echoListener(t *testing.T) net.Listener {
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
				_, _ = io.Copy(c, c)
			}(c)
		}
	}()
	return ln
}

func startGET(t *testing.T, url string, inflight int) *GET {
	t.Helper()
	g, err := NewGET("test-token-please-change", config.Outbound{
		Type: "get", URL: url, Path: "/static/v3/build",
		Fingerprint: "go", HeaderBudget: 4096, Inflight: inflight, Insecure: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	t.Cleanup(cancel)
	if err := g.Start(ctx); err != nil {
		t.Fatal(err)
	}
	return g
}

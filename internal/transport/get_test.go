package transport

// GET front tests: handshake, seq order, Leave, idle pacing, and RTT.

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/a3app/loft/internal/cdn"
	"github.com/a3app/loft/internal/config"
	ecrypto "github.com/a3app/loft/internal/crypto"
	"github.com/a3app/loft/internal/mux"
	"github.com/a3app/loft/internal/netconf"
	"github.com/a3app/loft/internal/testcfg"
	"github.com/a3app/loft/internal/wire"
)

func cookieHandshake(r *http.Request) bool {
	c := r.Header.Get("Cookie")
	if !strings.HasPrefix(c, "session=") {
		return false
	}
	blob, _ := wire.DecodeUplink(strings.TrimPrefix(c, "session="))
	return ecrypto.LooksLikeAuth(blob)
}

// TestGETEcho opens a mux TCP echo through the GET front.
func TestGETEcho(t *testing.T) {
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
	gs := NewGETServer(token, "/static/v3/build")
	ts := httptest.NewServer(http.HandlerFunc(gs.ServeHTTP))
	defer ts.Close()

	g, err := NewGET(token, config.Outbound{
		Type:         "get",
		URL:          ts.URL,
		Path:         "/static/v3/build",
		Fingerprint:  "go",
		HeaderBudget: 4096,
		Inflight:     2,
		Insecure:     true,
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
	if string(buf) != "ping-get" {
		t.Fatalf("got %q", buf)
	}
}

// TestGETRejectsLegacyV1Handshake: HMAC-only 0x02, path new, X-Auth are 404.
func TestGETRejectsLegacyV1Handshake(t *testing.T) {
	const token = "test-token-please-change"
	gs := NewGETServer(token, "/static/v3/build")
	ts := httptest.NewServer(http.HandlerFunc(gs.ServeHTTP))
	defer ts.Close()

	nonce := []byte("1234567890abcdef")
	mac := hmac.New(sha256.New, []byte(token))
	mac.Write(nonce)
	proof := append([]byte{0x02}, nonce...)
	proof = append(proof, mac.Sum(nil)[:16]...)
	h := wire.Hello{InstanceID: "legacy-phone", Caps: wire.MemberCaps}
	auth := append(proof, h.Encode()...)

	req, err := http.NewRequest(http.MethodGet, ts.URL+"/static/v3/build/new/1.js", nil)
	if err != nil {
		t.Fatal(err)
	}
	req.Header.Set("Cookie", "session="+wire.EncodeUplink(auth, 0))
	req.Header.Set("X-Auth", "1")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusNotFound {
		t.Fatalf("status %d", resp.StatusCode)
	}
}

// TestGETHandshakeClientSID: 0x02 Cookie, sid in the path, no X-Auth.
// Handshake still works when the edge hides X-Session.
func TestGETHandshakeClientSID(t *testing.T) {
	const token = "test-token-please-change"
	gs := NewGETServer(token, "/static/v3/build")
	var sawAuth, sawRID atomic.Bool
	var pathSID atomic.Value
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Auth") != "" {
			sawAuth.Store(true)
		}
		if r.Header.Get("X-Request-Id") != "" {
			sawRID.Store(true)
		}
		if cookieHandshake(r) {
			sid, _ := parseGETPath("/static/v3/build", r.URL.Path)
			pathSID.Store(sid)
		}
		rec := httptest.NewRecorder()
		gs.ServeHTTP(rec, r)
		for k, vv := range rec.Header() {
			if k == "X-Session" {
				continue
			}
			w.Header()[k] = vv
		}
		w.WriteHeader(rec.Code)
		_, _ = w.Write(rec.Body.Bytes())
	}))
	defer ts.Close()
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
	if sawAuth.Load() {
		t.Fatal("client still sends X-Auth")
	}
	if sawRID.Load() {
		t.Fatal("client still sends X-Request-Id")
	}
	sid, _ := pathSID.Load().(string)
	if !validGETSID(sid) {
		t.Fatalf("path sid %q", sid)
	}
	if g.sid != sid {
		t.Fatalf("client sid %q path %q", g.sid, sid)
	}
}

// TestGETLargeEchoInflight echoes a large payload with inflight > 1.
func TestGETLargeEchoInflight(t *testing.T) {
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
	gs := NewGETServer(token, "/static/v3/build")
	ts := httptest.NewServer(http.HandlerFunc(gs.ServeHTTP))
	defer ts.Close()

	g, err := NewGET(token, config.Outbound{
		Type:         "get",
		URL:          ts.URL,
		Path:         "/static/v3/build",
		Fingerprint:  "go",
		HeaderBudget: 4096,
		Inflight:     4,
		Insecure:     true,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
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
	_ = conn.SetDeadline(time.Now().Add(12 * time.Second))
	payload := make([]byte, 32*1024)
	for i := range payload {
		payload[i] = byte(i)
	}
	errc := make(chan error, 1)
	go func() {
		_, err := conn.Write(payload)
		errc <- err
	}()
	got := make([]byte, len(payload))
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatal(err)
	}
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
	if string(got) != string(payload) {
		t.Fatalf("mismatch n=%d first=%d", len(got), got[0])
	}
}

// TestGETDelayedWrite echoes after the origin pauses mid-stream.
func TestGETDelayedWrite(t *testing.T) {
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
	gs := NewGETServer(token, "/static/v3/build")
	ts := httptest.NewServer(http.HandlerFunc(gs.ServeHTTP))
	defer ts.Close()

	g, err := NewGET(token, config.Outbound{
		Type:         "get",
		URL:          ts.URL,
		Path:         "/static/v3/build",
		Fingerprint:  "go",
		HeaderBudget: 4096,
		Inflight:     4,
		Insecure:     true,
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

	conn, err := g.Open(echo.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	time.Sleep(300 * time.Millisecond)
	_ = conn.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := conn.Write([]byte("ping-get")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 8)
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != "ping-get" {
		t.Fatalf("got %q", buf)
	}
}

// TestGETLargeEcho echoes a large payload on the GET front.
func TestGETLargeEcho(t *testing.T) {
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
	gs := NewGETServer(token, "/static/v3/build")
	ts := httptest.NewServer(http.HandlerFunc(gs.ServeHTTP))
	defer ts.Close()

	g, err := NewGET(token, config.Outbound{
		Type:         "get",
		URL:          ts.URL,
		Path:         "/static/v3/build",
		Fingerprint:  "go",
		HeaderBudget: 4096,
		Inflight:     2,
		Insecure:     true,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
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
	want := make([]byte, 40<<10)
	for i := range want {
		want[i] = byte(i * 3)
	}
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	if _, err := conn.Write(want); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(want))
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatal("large get mismatch")
	}
}

// TestGETPushOrderedDropsLateSeq drops a late body after an empty hole for that seq.
func TestGETPushOrderedDropsLateSeq(t *testing.T) {
	g := &GET{sess: mux.NewClient(), held: map[uint64][]wire.Frame{}, nextSeq: 2}
	g.started.Store(true)
	dg := func(s string) wire.Frame {
		return wire.Frame{Cmd: wire.CmdDgram, Payload: append(mux.EncodeTarget("1.2.3.4", 53), s...)}
	}
	// finishMux(empty) on a timed-out GET used to advance nextSeq. The real
	// body for that seq then arrived late and was dropped — TLS hole.
	g.pushOrdered(2, nil)
	g.pushOrdered(2, []wire.Frame{dg("lost")})
	g.pushOrdered(3, []wire.Frame{dg("ok")})
	_, data, err := g.sess.ReadDgram()
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "ok" {
		t.Fatalf("got %q (late seq after empty hole leaked)", data)
	}
}

func TestGETPipelineSlotCapsInflight(t *testing.T) {
	g := &GET{cfg: config.Outbound{Inflight: 4}}
	g.seq.Store(10)
	g.nextSeq = 7 // ahead = 10-7+1 = 4
	if g.pipelineSlot() {
		t.Fatal("ahead==inflight")
	}
	g.nextSeq = 8 // ahead = 3
	if !g.pipelineSlot() {
		t.Fatal("ahead<inflight")
	}
}

// TestGETPushOrdered applies held downlink in GET seq order.
func TestGETPushOrdered(t *testing.T) {
	g := &GET{sess: mux.NewClient(), held: map[uint64][]wire.Frame{}, nextSeq: 2}
	g.started.Store(true)
	dg := func(s string) wire.Frame {
		return wire.Frame{Cmd: wire.CmdDgram, Payload: append(mux.EncodeTarget("1.2.3.4", 53), s...)}
	}
	g.pushOrdered(4, []wire.Frame{dg("c")})
	g.pushOrdered(3, []wire.Frame{dg("b")})
	select {
	case <-time.After(20 * time.Millisecond):
	default:
	}
	g.pushOrdered(2, []wire.Frame{dg("a")})
	var got string
	for i := 0; i < 3; i++ {
		_, data, err := g.sess.ReadDgram()
		if err != nil {
			t.Fatal(err)
		}
		got += string(data)
	}
	if got != "abc" {
		t.Fatalf("order %q", got)
	}
}

// TestGETLeaveDropsSession forgets the GET session after Close sends CmdLeave.
func TestGETLeaveDropsSession(t *testing.T) {
	const token = "test-token-please-change"
	gs := NewGETServer(token, "/static/v3/build")
	ts := httptest.NewServer(http.HandlerFunc(gs.ServeHTTP))
	defer ts.Close()

	g, err := NewGET(token, config.Outbound{
		Type:         "get",
		URL:          ts.URL,
		Path:         "/static/v3/build",
		Fingerprint:  "go",
		HeaderBudget: 4096,
		Inflight:     2,
		Insecure:     true,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := g.Start(ctx); err != nil {
		t.Fatal(err)
	}
	gs.mu.Lock()
	n := len(gs.sessions)
	gs.mu.Unlock()
	if n != 1 {
		t.Fatalf("sessions after start: %d", n)
	}
	if err := g.Close(); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		gs.mu.Lock()
		n = len(gs.sessions)
		gs.mu.Unlock()
		if n == 0 {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("GET session still live after leave: %d", n)
}

// TestGETCloseAfterHandoverSkipsLeave: Holder.Set closes the old front
// after the next Start. Leave must not drop the server session.
func TestGETCloseAfterHandoverSkipsLeave(t *testing.T) {
	t.Cleanup(clearClientHold)
	const token = "test-token-please-change"
	gs := NewGETServer(token, "/static/v3/build")
	gs.Info.Caps = wire.CapHold
	ts := httptest.NewServer(http.HandlerFunc(gs.ServeHTTP))
	defer ts.Close()

	g, err := NewGET(token, config.Outbound{
		Type:         "get",
		URL:          ts.URL,
		Path:         "/static/v3/build",
		Fingerprint:  "go",
		HeaderBudget: 4096,
		Inflight:     2,
		Insecure:     true,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := g.Start(ctx); err != nil {
		t.Fatal(err)
	}
	sess := g.sess
	_, _ = takeClientHold()
	if err := g.Close(); err != nil {
		t.Fatal(err)
	}
	if sess.Closed() {
		t.Fatal("mux closed")
	}
	time.Sleep(200 * time.Millisecond)
	gs.mu.Lock()
	n := len(gs.sessions)
	gs.mu.Unlock()
	if n != 1 {
		t.Fatalf("sessions %d", n)
	}
}

// TestGETXAuthAdoptsHeldMux reuses the server mux on a hold-gap handshake
// (new GET HTTP sid) so inner TCP survives a hold gap.
func TestGETXAuthAdoptsHeldMux(t *testing.T) {
	t.Cleanup(clearClientHold)
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
	gs := NewGETServer(token, "/static/v3/build")
	gs.Info.Caps = wire.CapHold
	ts := httptest.NewServer(http.HandlerFunc(gs.ServeHTTP))
	defer ts.Close()

	mk := func() *GET {
		g, err := NewGET(token, config.Outbound{
			Type: "get", URL: ts.URL, Path: "/static/v3/build",
			Fingerprint: "go", HeaderBudget: 4096, Inflight: 2, Insecure: true,
		})
		if err != nil {
			t.Fatal(err)
		}
		g.offer = wire.CapHold
		return g
	}
	g1 := mk()
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := g1.Start(ctx); err != nil {
		t.Fatal(err)
	}
	conn, err := g1.Open(echo.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(8 * time.Second))
	if _, err := conn.Write([]byte("ping-get")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 8)
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatal(err)
	}

	if err := g1.Park(); err != nil {
		t.Fatal(err)
	}
	clientHoldMu.Lock()
	clientHold.sid, clientHold.key, clientHold.seq = "", nil, 0
	clientHoldMu.Unlock()

	g2 := mk()
	if err := g2.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer g2.Close()

	gs.mu.Lock()
	n := len(gs.sessions)
	var sm *mux.Session
	for _, ss := range gs.sessions {
		sm = ss.mux
	}
	streams := 0
	if sm != nil {
		streams = sm.StreamCount()
	}
	gs.mu.Unlock()
	if n != 1 {
		t.Fatalf("sessions %d, want 1 (old HTTP sid stolen)", n)
	}
	if streams != 1 {
		t.Fatalf("server streams %d, mux replaced", streams)
	}
	if _, err := conn.Write([]byte("ping-get")); err != nil {
		t.Fatal(err)
	}
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatal(err)
	}
}

// TestGETIdleDoesNotPoll does not storm GETs while mux is idle.
func TestGETIdleDoesNotPoll(t *testing.T) {
	var n atomic.Int32
	const token = "test-token-please-change"
	gs := NewGETServer(token, "/static/v3/build")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		n.Add(1)
		gs.ServeHTTP(w, r)
	}))
	defer ts.Close()
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
	time.Sleep(400 * time.Millisecond)
	got := n.Load()
	if got > 2 {
		t.Fatalf("idle GET storm %d (handshake only)", got)
	}
}

// TestGETMuxUsesCookie puts later uplink in Cookie, not X-Request-Id.
func TestGETMuxUsesCookie(t *testing.T) {
	var lastCookie, lastRID atomic.Value
	lastCookie.Store("")
	lastRID.Store("")
	const token = "test-token-please-change"
	gs := NewGETServer(token, "/static/v3/build")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastCookie.Store(r.Header.Get("Cookie"))
		lastRID.Store(r.Header.Get("X-Request-Id"))
		gs.ServeHTTP(w, r)
	}))
	defer ts.Close()
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		c, err := echo.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		io.Copy(c, c)
	}()
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
	conn, err := g.Open(echo.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(4 * time.Second))
	if _, err := conn.Write([]byte("ping-get")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 8)
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatal(err)
	}
	cookie, _ := lastCookie.Load().(string)
	rid, _ := lastRID.Load().(string)
	if !strings.HasPrefix(cookie, "session=") {
		t.Fatalf("mux GET cookie %q", cookie)
	}
	if rid != "" {
		t.Fatalf("mux GET still uses X-Request-Id")
	}
}

// TestGETMuxYandexUsesCookie puts later uplink in Cookie so the edge can drop X-Request-Id.
func TestGETMuxYandexUsesCookie(t *testing.T) {
	var lastCookie, lastRID atomic.Value
	lastCookie.Store("")
	lastRID.Store("")
	const token = "test-token-please-change"
	gs := NewGETServer(token, "/static/v3/build")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lastCookie.Store(r.Header.Get("Cookie"))
		lastRID.Store(r.Header.Get("X-Request-Id"))
		gs.ServeHTTP(w, r)
	}))
	defer ts.Close()
	echo, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer echo.Close()
	go func() {
		c, err := echo.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		io.Copy(c, c)
	}()
	g, err := NewGET(token, config.Outbound{
		Type: "get", URL: ts.URL, Path: "/static/v3/build", CDN: "yandex",
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
	conn, err := g.Open(echo.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(4 * time.Second))
	if _, err := conn.Write([]byte("ping-cdn")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 8)
	if _, err := io.ReadFull(conn, buf); err != nil {
		t.Fatal(err)
	}
	cookie, _ := lastCookie.Load().(string)
	rid, _ := lastRID.Load().(string)
	if !strings.HasPrefix(cookie, "session=") {
		t.Fatalf("yandex mux GET cookie %q", cookie)
	}
	if rid != "" {
		t.Fatalf("yandex mux GET still uses X-Request-Id")
	}
}

// TestGETBulkThroughput downloads a large echo through GET.
func TestGETBulkThroughput(t *testing.T) {
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
	gs := NewGETServer(token, "/static/v3/build")
	ts := httptest.NewServer(http.HandlerFunc(gs.ServeHTTP))
	defer ts.Close()

	g, err := NewGET(token, config.Outbound{
		Type: "get", URL: ts.URL, Path: "/static/v3/build",
		Fingerprint: "go", HeaderBudget: 16 << 10, Inflight: 4, Insecure: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
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
	_ = conn.SetDeadline(time.Now().Add(15 * time.Second))

	const n = 512 << 10
	payload := make([]byte, n)
	for i := range payload {
		payload[i] = byte(i)
	}
	t0 := time.Now()
	errc := make(chan error, 1)
	go func() {
		_, err := conn.Write(payload)
		errc <- err
	}()
	got := make([]byte, n)
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatal(err)
	}
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
	dt := time.Since(t0)
	bps := float64(n) / dt.Seconds()
	t.Logf("%dB in %s → %.0f B/s (%.2f Mbit/s)", n, dt, bps, bps*8/1e6)
	if bps < 125000 {
		t.Fatalf("GET mux %.0f B/s < 1 Mbit/s", bps)
	}
	if string(got) != string(payload) {
		t.Fatal("payload mismatch")
	}
}

// TestGETBulkUpload uploads a large body through GET.
func TestGETBulkUpload(t *testing.T) {
	sink, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer sink.Close()
	var recvd atomic.Int64
	done := make(chan struct{})
	go func() {
		c, err := sink.Accept()
		if err != nil {
			return
		}
		n, _ := io.Copy(io.Discard, c)
		c.Close()
		recvd.Store(n)
		close(done)
	}()

	const token = "test-token-please-change"
	gs := NewGETServer(token, "/static/v3/build")
	ts := httptest.NewServer(http.HandlerFunc(gs.ServeHTTP))
	defer ts.Close()

	g, err := NewGET(token, config.Outbound{
		Type: "get", URL: ts.URL, Path: "/static/v3/build",
		Fingerprint: "go", HeaderBudget: 16 << 10, Inflight: 4, Insecure: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := g.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer g.Close()

	conn, err := g.Open(sink.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	_ = conn.SetDeadline(time.Now().Add(15 * time.Second))

	const n = 512 << 10
	payload := make([]byte, n)
	for i := range payload {
		payload[i] = byte(i)
	}
	t0 := time.Now()
	if _, err := conn.Write(payload); err != nil {
		t.Fatal(err)
	}
	if err := conn.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-done:
	case <-time.After(15 * time.Second):
		t.Fatalf("sink got %d / %d", recvd.Load(), n)
	}
	dt := time.Since(t0)
	if recvd.Load() != int64(n) {
		t.Fatalf("sink %d want %d", recvd.Load(), n)
	}
	bps := float64(n) / dt.Seconds()
	t.Logf("upload %dB in %s → %.0f B/s (%.2f Mbit/s)", n, dt, bps, bps*8/1e6)
	if bps < 125000 {
		t.Fatalf("GET mux upload %.0f B/s < 1 Mbit/s", bps)
	}
}

// TestPullHoldIdleDoesNotPark keeps keepalive pads from sitting 250ms as Puller.
func TestPullHoldIdleDoesNotPark(t *testing.T) {
	ms := mux.NewServer()
	first, more := pullHold(nil, ms)
	if first != 0 || more != 0 {
		t.Fatalf("idle pad hold %s %s", first, more)
	}
	open := []wire.Frame{{Cmd: wire.CmdOpen, Payload: mux.EncodeTarget("127.0.0.1", 9)}}
	first, more = pullHold(open, ms)
	if first != getDownlinkMore {
		t.Fatalf("open hold %s", first)
	}
	dg := []wire.Frame{{Cmd: wire.CmdDgram}}
	first, _ = pullHold(dg, ms)
	if first != getDgramHold {
		t.Fatalf("dgram hold %s", first)
	}
	if _, err := ms.Open("127.0.0.1:9"); err != nil {
		t.Fatal(err)
	}
	first, more = pullHold(dg, ms)
	if first != 0 || more != getDownlinkFill {
		t.Fatalf("dgram with live TCP hold %s %s", first, more)
	}
}

// TestGETGatewayPingFast answers overlay ICMP echo on the GET front.
func TestGETGatewayPingFast(t *testing.T) {
	n := &netconf.Network{ID: "n", CIDR: "10.8.0.0/24", Gateway: "10.8.0.1", Peers: netconf.PeersHub, Enabled: true}
	o := NewOverlay(n)
	oldKeys, oldSwitch := AuthKeys, NetSwitch()
	AuthKeys = nil
	SetNetSwitch(o)
	defer func() {
		AuthKeys = oldKeys
		SetNetSwitch(oldSwitch)
	}()

	const token = "test-token-please-change"
	gs := NewGETServer(token, "/static/v3/build")
	ts := httptest.NewServer(http.HandlerFunc(gs.ServeHTTP))
	defer ts.Close()

	g, err := NewGET(token, config.Outbound{
		Type: "get", URL: ts.URL, Path: "/static/v3/build",
		Fingerprint: "go", HeaderBudget: 16 << 10, Inflight: 4, Insecure: true,
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

	pkt := makeEchoPkt(net.IPv4(10, 8, 0, 2), net.IPv4(10, 8, 0, 1), []byte("ping"))
	t0 := time.Now()
	if err := g.sess.WriteDgram("10.8.0.1:1", pkt); err != nil {
		t.Fatal(err)
	}
	got := make(chan []byte, 1)
	go func() {
		_, data, err := g.sess.ReadDgram()
		if err != nil {
			close(got)
			return
		}
		got <- data
	}()
	select {
	case data, ok := <-got:
		if !ok {
			t.Fatal("read dgram")
		}
		dt := time.Since(t0)
		t.Logf("GET gateway ping %s", dt)
		if dt > 400*time.Millisecond {
			t.Fatalf("GET gateway ping %s; idle 1s pace or async ICMP", dt)
		}
		if len(data) < 28 {
			t.Fatalf("short icmp %d", len(data))
		}
		ihl := int(data[0]&0x0f) * 4
		if ihl < 20 || len(data) < ihl+8 || data[ihl] != icmpReply {
			t.Fatalf("want echo-reply, type=%d", data[ihl])
		}
	case <-time.After(2 * time.Second):
		t.Fatal("timeout")
	}
}

// startH2GET starts a TLS httptest with HTTP/2 enabled.
func startH2GET(t *testing.T, h http.Handler) *httptest.Server {
	t.Helper()
	ts := httptest.NewUnstartedServer(h)
	ts.EnableHTTP2 = true
	ts.TLS = &tls.Config{NextProtos: []string{"h2", "http/1.1"}}
	ts.StartTLS()
	t.Cleanup(ts.Close)
	return ts
}

// echoFast listens on 127.0.0.1 and echoes bytes until close.
func echoFast(t *testing.T) net.Listener {
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

// withOverlay installs a tiny overlay fabric for the duration of t.
func withOverlay(t *testing.T) {
	t.Helper()
	n := &netconf.Network{ID: "n", CIDR: "10.8.0.0/24", Gateway: "10.8.0.1", Peers: netconf.PeersHub, Enabled: true}
	o := NewOverlay(n)
	oldKeys, oldSwitch := AuthKeys, NetSwitch()
	AuthKeys = nil
	SetNetSwitch(o)
	t.Cleanup(func() {
		AuthKeys = oldKeys
		SetNetSwitch(oldSwitch)
	})
}

// TestGETFrontRTT measures GET RTT with a delayed origin.
func TestGETFrontRTT(t *testing.T) {
	withOverlay(t)
	echo := echoFast(t)

	slow, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer slow.Close()
	const originDelay = 40 * time.Millisecond
	go func() {
		c, err := slow.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		buf := make([]byte, 4096)
		for {
			n, err := c.Read(buf)
			if n > 0 {
				time.Sleep(originDelay)
				if _, werr := c.Write(buf[:n]); werr != nil {
					return
				}
			}
			if err != nil {
				return
			}
		}
	}()

	const token = "test-token-please-change"
	const rtt = 80 * time.Millisecond
	gs := NewGETServer(token, "/static/v3/build")
	var gets atomic.Int64
	var proto atomic.Value
	ts := startH2GET(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gets.Add(1)
		proto.Store(r.Proto)
		time.Sleep(rtt)
		gs.ServeHTTP(w, r)
	}))

	g, err := NewGET(token, config.Outbound{
		Type: "get", URL: ts.URL, Path: "/static/v3/build",
		Fingerprint: "go", HeaderBudget: 16 << 10, Inflight: 8, Insecure: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := g.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer g.Close()

	pkt := makeEchoPkt(net.IPv4(10, 8, 0, 2), net.IPv4(10, 8, 0, 1), []byte("ping"))
	t0 := time.Now()
	if err := g.sess.WriteDgram("10.8.0.1:1", pkt); err != nil {
		t.Fatal(err)
	}
	icmpCh := make(chan []byte, 1)
	go func() {
		_, data, err := g.sess.ReadDgram()
		if err != nil {
			close(icmpCh)
			return
		}
		icmpCh <- data
	}()
	var icmpDT time.Duration
	select {
	case _, ok := <-icmpCh:
		if !ok {
			t.Fatal("icmp read")
		}
		icmpDT = time.Since(t0)
	case <-time.After(3 * time.Second):
		t.Fatal("icmp timeout")
	}
	t.Logf("ICMP gateway h2+%s: %s", rtt, icmpDT)
	if icmpDT > rtt+80*time.Millisecond {
		t.Fatalf("ICMP %s; want ~1 front RTT (%s)", icmpDT, rtt)
	}

	slowConn, err := g.Open(slow.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	_ = slowConn.SetDeadline(time.Now().Add(8 * time.Second))
	t1 := time.Now()
	if _, err := slowConn.Write([]byte("p")); err != nil {
		t.Fatal(err)
	}
	one := make([]byte, 1)
	if _, err := io.ReadFull(slowConn, one); err != nil {
		t.Fatal(err)
	}
	tcpDT := time.Since(t1)
	_ = slowConn.Close()
	t.Logf("TCP 1-byte h2+%s origin+%s: %s", rtt, originDelay, tcpDT)
	if tcpDT > 2*rtt+originDelay+80*time.Millisecond {
		t.Fatalf("TCP echo %s; pad GET should catch origin inside 2 RTT", tcpDT)
	}

	conn, err := g.Open(echo.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
	const nbyte = 512 << 10
	payload := make([]byte, nbyte)
	gets.Store(0)
	t2 := time.Now()
	errc := make(chan error, 1)
	go func() {
		_, err := conn.Write(payload)
		errc <- err
	}()
	got := make([]byte, nbyte)
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatal(err)
	}
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
	dt := time.Since(t2)
	bps := float64(nbyte) / dt.Seconds()
	mbits := bps * 8 / 1e6
	t.Logf("bulk echo %dB in %s → %.2f Mbit/s GETs=%d proto=%v icmp=%s tcp1=%s",
		nbyte, dt, mbits, gets.Load(), proto.Load(), icmpDT, tcpDT)
	if bps < 250000 {
		t.Fatalf("GET h2 RTT bulk echo %.2f Mbit/s < 2 Mbit/s", mbits)
	}
}

// TestGETBulkEchoH1RTT echoes a large payload over HTTP/1.1 with injected RTT.
func TestGETBulkEchoH1RTT(t *testing.T) {
	echo := echoFast(t)
	const token = "test-token-please-change"
	const rtt = 80 * time.Millisecond
	gs := NewGETServer(token, "/static/v3/build")
	var gets atomic.Int64
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gets.Add(1)
		time.Sleep(rtt)
		gs.ServeHTTP(w, r)
	}))
	defer ts.Close()

	g, err := NewGET(token, config.Outbound{
		Type: "get", URL: ts.URL, Path: "/static/v3/build",
		Fingerprint: "go", HeaderBudget: 16 << 10, Inflight: 8, Insecure: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
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
	_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
	const nbyte = 256 << 10
	payload := make([]byte, nbyte)
	gets.Store(0)
	t0 := time.Now()
	errc := make(chan error, 1)
	go func() {
		_, err := conn.Write(payload)
		errc <- err
	}()
	got := make([]byte, nbyte)
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatal(err)
	}
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
	dt := time.Since(t0)
	bps := float64(nbyte) / dt.Seconds()
	mbits := bps * 8 / 1e6
	t.Logf("h1 bulk echo %dB in %s → %.2f Mbit/s GETs=%d", nbyte, dt, mbits, gets.Load())
	if bps < 500000 {
		t.Fatalf("GET h1 RTT bulk %.2f Mbit/s < 4 Mbit/s (pre-fix ~6)", mbits)
	}
}

// TestGETDownloadRTT downloads a large body under injected RTT.
func TestGETDownloadRTT(t *testing.T) {
	src, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	const nbyte = 512 << 10
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
	const rtt = 80 * time.Millisecond
	gs := NewGETServer(token, "/static/v3/build")
	var gets atomic.Int64
	ts := startH2GET(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gets.Add(1)
		time.Sleep(rtt)
		gs.ServeHTTP(w, r)
	}))

	g, err := NewGET(token, config.Outbound{
		Type: "get", URL: ts.URL, Path: "/static/v3/build",
		Fingerprint: "go", HeaderBudget: 16 << 10, Inflight: 8, Insecure: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := g.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer g.Close()

	conn, err := g.Open(src.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
	t0 := time.Now()
	got := make([]byte, nbyte)
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatal(err)
	}
	dt := time.Since(t0)
	bps := float64(nbyte) / dt.Seconds()
	mbits := bps * 8 / 1e6
	t.Logf("download %dB in %s → %.2f Mbit/s GETs=%d", nbyte, dt, mbits, gets.Load())
	if bps < 1e6 {
		t.Fatalf("GET h2 download %.2f Mbit/s < 8 Mbit/s", mbits)
	}
}

// TestGETDownloadNotStalledByTUNDgrams is TUN default-route: Apple QUIC
// (~1200 B UDP) plus ICMP share the exclusive GET Puller with TCP. CmdDgram
// used to jump pri and park Puller 80ms; ACKs starved and 512 KiB crawled.
func TestGETDownloadNotStalledByTUNDgrams(t *testing.T) {
	src, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	const nbyte = 512 << 10
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
	const rtt = 80 * time.Millisecond
	gs := NewGETServer(token, "/static/v3/build")
	var gets atomic.Int64
	ts := startH2GET(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gets.Add(1)
		time.Sleep(rtt)
		gs.ServeHTTP(w, r)
	}))

	g, err := NewGET(token, config.Outbound{
		Type: "get", URL: ts.URL, Path: "/static/v3/build",
		Fingerprint: "go", HeaderBudget: 16 << 10, Inflight: 8, Insecure: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := g.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer g.Close()

	stop := make(chan struct{})
	defer close(stop)
	go func() {
		pkt := make([]byte, 1200)
		for {
			select {
			case <-stop:
				return
			default:
				_ = g.sess.WriteDgram("17.248.214.68:443", pkt)
				time.Sleep(5 * time.Millisecond)
			}
		}
	}()

	conn, err := g.Open(src.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(20 * time.Second))
	t0 := time.Now()
	got := make([]byte, nbyte)
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatal(err)
	}
	dt := time.Since(t0)
	bps := float64(nbyte) / dt.Seconds()
	mbits := bps * 8 / 1e6
	t.Logf("download with TUN QUIC flood %dB in %s → %.2f Mbit/s GETs=%d", nbyte, dt, mbits, gets.Load())
	if bps < 500000 {
		t.Fatalf("GET h2 download under TUN dgrams %.2f Mbit/s < 4 Mbit/s", mbits)
	}
}

// TestGETDownloadGapped: origin pauses mid-body. Empty pads used to clear busy
// and sleep 1s, turning a 150ms hole into ~1.2s (~10× vs 80ms pipeline).
func TestGETDownloadGapped(t *testing.T) {
	src, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer src.Close()
	const nbyte = 512 << 10
	const gap = 150 * time.Millisecond
	go func() {
		c, err := src.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		_, _ = c.Write(make([]byte, 64<<10))
		time.Sleep(gap)
		_, _ = c.Write(make([]byte, nbyte-64<<10))
		_, _ = io.Copy(io.Discard, c)
	}()

	const token = "test-token-please-change"
	const rtt = 80 * time.Millisecond
	gs := NewGETServer(token, "/static/v3/build")
	var gets atomic.Int64
	ts := startH2GET(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gets.Add(1)
		time.Sleep(rtt)
		gs.ServeHTTP(w, r)
	}))

	g, err := NewGET(token, config.Outbound{
		Type: "get", URL: ts.URL, Path: "/static/v3/build",
		Fingerprint: "go", HeaderBudget: 16 << 10, Inflight: 8, Insecure: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	if err := g.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer g.Close()

	conn, err := g.Open(src.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(15 * time.Second))
	t0 := time.Now()
	got := make([]byte, nbyte)
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatal(err)
	}
	dt := time.Since(t0)
	bps := float64(nbyte) / dt.Seconds()
	mbits := bps * 8 / 1e6
	t.Logf("gapped download %dB gap=%s in %s → %.2f Mbit/s GETs=%d", nbyte, gap, dt, mbits, gets.Load())
	if dt > 2*time.Second {
		t.Fatalf("gapped download %s; empty pad must not idle 1s", dt)
	}
	if bps < 500000 {
		t.Fatalf("GET h2 gapped download %.2f Mbit/s < 4 Mbit/s", mbits)
	}
}

// Live Yandex Internet: clean ~60 Mbit, ZeroTier ~34/32 at 96 ms, GET loft
// ~10/5 at 123 ms. This is that path with 100 ms front RTT and inflight 32.
func TestGETThroughput100ms(t *testing.T) {
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
	const rtt = 100 * time.Millisecond
	// GenericGET advertises inflight 8. Status only lowers the client,
	// so a server on that ceiling cannot exercise Inflight 48.
	prof := cdn.GenericGET()
	prof.Inflight = 48
	gs := NewGETServerProfile(token, "/static/v3/build", prof)
	var gets atomic.Int64
	ts := startH2GET(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gets.Add(1)
		time.Sleep(rtt)
		gs.ServeHTTP(w, r)
	}))

	g, err := NewGET(token, config.Outbound{
		Type: "get", URL: ts.URL, Path: "/static/v3/build",
		Fingerprint: "go", HeaderBudget: 16 << 10, Inflight: 48, Insecure: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	if err := g.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer g.Close()

	down, err := g.Open(src.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer down.Close()
	_ = down.SetDeadline(time.Now().Add(30 * time.Second))
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
	up, err := g.Open(sink.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	_ = up.SetDeadline(time.Now().Add(30 * time.Second))
	payload := make([]byte, nbyte)
	t1 := time.Now()
	if _, err := up.Write(payload); err != nil {
		t.Fatal(err)
	}
	_ = up.Close()
	nup := <-gotn
	upDT := time.Since(t1)
	if nup != int64(nbyte) {
		t.Fatalf("origin got %d of %d", nup, nbyte)
	}
	upM := float64(nbyte) / upDT.Seconds() * 8 / 1e6
	t.Logf("100ms RTT 2MiB down %.2f Mbit/s in %s; up %.2f Mbit/s in %s; GETs=%d",
		downM, downDT, upM, upDT, gets.Load())
	if downM < 25 {
		t.Fatalf("download %.2f Mbit/s < 25 (ZeroTier live ~34)", downM)
	}
	if upM < 20 {
		t.Fatalf("upload %.2f Mbit/s < 20 (ZeroTier live ~32)", upM)
	}
}

// TestGETH2UploadNotPullerQueued: HTTP/2 Data GETs must not wait 5s to
// become exclusive Puller. That queue made CDN Cookie TTFB ~1s.
func TestGETH2UploadNotPullerQueued(t *testing.T) {
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

	const token = "test-token-please-change"
	const nbyte = 256 << 10
	gs := NewGETServer(token, "/static/v3/build")
	ts := startH2GET(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(40 * time.Millisecond)
		gs.ServeHTTP(w, r)
	}))

	g, err := NewGET(token, config.Outbound{
		Type: "get", URL: ts.URL, Path: "/static/v3/build",
		Fingerprint: "go", HeaderBudget: 12 << 10, Inflight: 16, Insecure: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if err := g.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer g.Close()

	up, err := g.Open(sink.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	_ = up.SetDeadline(time.Now().Add(15 * time.Second))
	t0 := time.Now()
	if _, err := up.Write(make([]byte, nbyte)); err != nil {
		t.Fatal(err)
	}
	_ = up.Close()
	nup := <-gotn
	dt := time.Since(t0)
	if nup != int64(nbyte) {
		t.Fatalf("origin got %d of %d", nup, nbyte)
	}
	mbits := float64(nbyte) / dt.Seconds() * 8 / 1e6
	t.Logf("h2 upload 256KiB + 40ms/GET: %.2f Mbit/s in %s ttfb_max=%.0fms %s",
		mbits, dt, float64(g.trips.maxTTFBNs.Load())/1e6, g.TripSummary())
	// Serialized Puller queue: ~32 GETs × 40ms ≈ 1.3s (~1.6 Mbit).
	// Pipelined inflight 16: a few 40ms RTTs (>8 Mbit).
	if mbits < 8 {
		t.Fatalf("h2 upload %.2f Mbit/s < 8 (Puller still serializing Data GETs)", mbits)
	}
}

func TestRetryableGET(t *testing.T) {
	if !retryableGET(fmt.Errorf("GET front: header too large (413): lower header_budget")) {
		t.Fatal("413")
	}
	if !retryableGET(fmt.Errorf("GET front: status 404: 404 page not found")) {
		t.Fatal("404")
	}
	if retryableGET(fmt.Errorf("GET front: method not allowed (use type get)")) {
		t.Fatal("405")
	}
	if !retryableGET(fmt.Errorf("read: connection reset by peer")) {
		t.Fatal("reset")
	}
	if !retryableGET(fmt.Errorf("GET front: status 504: timeout")) {
		t.Fatal("504")
	}
	if !retryableGET(fmt.Errorf(`Get "https://cdn.example/x.js": context canceled`)) {
		t.Fatal("canceled")
	}
	if !frontReset(fmt.Errorf("http2: client conn not usable")) {
		t.Fatal("h2 conn")
	}
	calm := fmt.Errorf(`Get "https://cdn.example/x.js": http2: server sent GOAWAY and closed the connection; LastStreamID=1, ErrCode=ENHANCE_YOUR_CALM, debug=""`)
	if !enhanceCalm(calm) || !retryableGET(calm) || !frontReset(calm) {
		t.Fatal("calm")
	}
	noRoute := fmt.Errorf(`Get "%s/x.js": dial tcp4: connect: no route to host`, testcfg.FrontHTTPS())
	if !radioGap(noRoute) || !retryableGET(noRoute) || !frontReset(noRoute) {
		t.Fatal("no route")
	}
	if !radioGap(fmt.Errorf("Get: i/o timeout")) {
		t.Fatal("i/o timeout")
	}
}

// TestDropIdleFrontReplacesClient parks the live Transport so getConn
// cannot stick on a half-dead persistConn after RST.
func TestDropIdleFrontReplacesClient(t *testing.T) {
	g, err := NewGET("test-token-please-change", config.Outbound{
		Type: "get", URL: "http://127.0.0.1:1", Path: "/static/v3/build",
		Fingerprint: "go", HeaderBudget: 4096, Inflight: 1, Insecure: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	old := g.client
	if old == nil || len(g.clients) == 0 {
		t.Fatal("no client")
	}
	g.dropIdleFront()
	if g.client == nil || g.client == old {
		t.Fatal("client not replaced")
	}
	if g.clients[0] != g.client {
		t.Fatal("pool stale")
	}
	g.lastDrop.Store(0)
	g.dropIdleFront()
	if g.client == old {
		t.Fatal("second replace reused first Transport")
	}
}

// TestGETRadioGapKeepsSession retries a dropped GET on the same sid; mux stays.
func TestGETRadioGapKeepsSession(t *testing.T) {
	t.Cleanup(clearClientHold)
	echo := echoListener(t)

	var live, cut atomic.Bool
	const token = "test-token-please-change"
	gs := NewGETServer(token, "/static/v3/build")
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if cookieHandshake(r) {
			gs.ServeHTTP(w, r)
			return
		}
		if live.Load() && cut.CompareAndSwap(false, true) {
			hj, ok := w.(http.Hijacker)
			if !ok {
				t.Error("hijack")
				return
			}
			c, _, err := hj.Hijack()
			if err == nil {
				_ = c.Close()
			}
			return
		}
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
	ping := func(s string) {
		t.Helper()
		_ = conn.SetDeadline(time.Now().Add(8 * time.Second))
		if _, err := conn.Write([]byte(s)); err != nil {
			t.Fatal(err)
		}
		buf := make([]byte, len(s))
		if _, err := io.ReadFull(conn, buf); err != nil {
			t.Fatal(err)
		}
		if string(buf) != s {
			t.Fatalf("got %q", buf)
		}
	}
	ping("hold-gap")
	live.Store(true)
	ping("aftergap")
	select {
	case <-g.Done():
		t.Fatal("front died across radio gap")
	default:
	}
	if !cut.Load() {
		t.Fatal("no cut")
	}
}

func TestDownlinkMaxShrinksOnUplink(t *testing.T) {
	if downlinkMax(nil, nil) != getDownlinkBytes {
		t.Fatal("empty")
	}
	fat := []wire.Frame{{Cmd: wire.CmdData, Payload: make([]byte, 4<<10)}}
	if downlinkMax(nil, fat) != getDownlinkUp {
		t.Fatalf("up %d", downlinkMax(nil, fat))
	}
	pad := []wire.Frame{{Cmd: wire.CmdPad, Payload: make([]byte, 4<<10)}}
	if downlinkMax(nil, pad) != getDownlinkBytes {
		t.Fatal("pad")
	}
}

func TestUplinkBudgetCookie85(t *testing.T) {
	g := &GET{}
	g.budget.Store(16 << 10)
	b64 := g.uplinkBudget()
	g.uplinkEnc.Store(int32(wire.UplinkCookie85))
	z := g.uplinkBudget()
	if z <= b64 {
		t.Fatalf("z85 %d <= b64 %d", z, b64)
	}
	one := wire.HeaderSize + 12<<10
	if z < one {
		t.Fatalf("z85 %d < 12KiB frame", z)
	}
}

func TestPickClientRoundRobin(t *testing.T) {
	a, b := &http.Client{}, &http.Client{}
	g := &GET{clients: []*http.Client{a, b}}
	g.liveH2.Store(2)
	seen := map[*http.Client]int{}
	for i := 0; i < 20; i++ {
		seen[g.pickClient()]++
	}
	if seen[a] == 0 || seen[b] == 0 {
		t.Fatalf("%v", seen)
	}
}

func TestParseH2MaxAge(t *testing.T) {
	if parseH2MaxAge(0) != 0 || parseH2MaxAge(-1) != 0 {
		t.Fatal("off")
	}
	if parseH2MaxAge(1000) != h2MaxAgeMin {
		t.Fatal("floor")
	}
	if parseH2MaxAge(60000) != time.Minute {
		t.Fatal("60s")
	}
	if parseH2MaxAge(int((2*time.Hour)/time.Millisecond)) != h2MaxAgeMax {
		t.Fatal("cap")
	}
}

func TestPickClientRotatesAged(t *testing.T) {
	a := &http.Client{}
	g := &GET{
		cfg:      config.Outbound{Type: "get", URL: "http://127.0.0.1/"},
		clients:  []*http.Client{a},
		born:     []time.Time{time.Now().Add(-time.Hour)},
		h2MaxAge: 15 * time.Second,
	}
	g.liveH2.Store(1)
	got := g.pickClient()
	if got == nil || got == a {
		t.Fatal("did not rotate")
	}
	if len(g.drain) != 1 || g.drain[0].c != a {
		t.Fatalf("drain %+v", g.drain)
	}
	if g.pickClient() == a {
		t.Fatal("still picking aged")
	}
}

func TestPickClientNoRotateWhenOff(t *testing.T) {
	a := &http.Client{}
	g := &GET{
		clients: []*http.Client{a},
		born:    []time.Time{time.Now().Add(-time.Hour)},
	}
	g.liveH2.Store(1)
	if g.pickClient() != a {
		t.Fatal("rotated with max-age 0")
	}
}

func TestSetCDNHeadersVaryCookie(t *testing.T) {
	rec := httptest.NewRecorder()
	setCDNHeaders(rec)
	got := rec.Header().Get("Vary")
	if got != "Cookie" {
		t.Fatalf("Vary=%q", got)
	}
}

func TestUplinkHeaderFindsSessionAmongCookies(t *testing.T) {
	r := httptest.NewRequest(http.MethodGet, "/static/v3/build/ab/1.js", nil)
	r.Header["Cookie"] = []string{"edge=1", "other=x; session=PAYLOAD; trail=1"}
	if got := uplinkHeader(r); got != "PAYLOAD" {
		t.Fatalf("got %q", got)
	}
	r2 := httptest.NewRequest(http.MethodGet, "/", nil)
	r2.Header.Set("X-Request-Id", "OLD")
	if got := uplinkHeader(r2); got != "" {
		t.Fatalf("rid %q", got)
	}
}

func TestYandexUplinkFitsOneChunk(t *testing.T) {
	g := &GET{}
	g.budget.Store(16 << 10)
	n := g.uplinkBudget()
	one := wire.HeaderSize + 12<<10
	if n < 8<<10 {
		t.Fatalf("uplink %d too small", n)
	}
	// Pull takes the first frame even if it overshoots by a header.
	if one > n+256 {
		t.Fatalf("12KiB frame %d >> uplink %d", one, n)
	}
}

func TestNoteCalmBackoff(t *testing.T) {
	g := &GET{}
	if !g.waitCalm(context.Background(), 1) {
		t.Fatal("empty")
	}
	g.noteCalm()
	if g.calmUntil.Load() == 0 {
		t.Fatal("until")
	}
}

func TestHalveHeaderBudget(t *testing.T) {
	g := &GET{}
	g.budget.Store(12 << 10)
	if n := g.halveBudget(); n != 6<<10 {
		t.Fatalf("halve 12KiB -> %d", n)
	}
	if n := g.halveBudget(); n != 3<<10 {
		t.Fatalf("halve 6KiB -> %d", n)
	}
	g.budget.Store(1024)
	if n := g.halveBudget(); n != 1024 {
		t.Fatalf("floor %d", n)
	}
	g.cfg.HeaderBudget = 16 << 10
	g.budget.Store(0)
	if n := g.halveBudget(); n != 8<<10 {
		t.Fatalf("from cfg %d", n)
	}
}

func TestReplyCacheKeepsFirstBody(t *testing.T) {
	ss := &serverSess{replies: map[uint64][]byte{}}
	a := ss.rememberReply(7, []byte("first"))
	b := ss.rememberReply(7, []byte("second"))
	if string(a) != "first" || string(b) != "first" {
		t.Fatalf("overwrite %q %q", a, b)
	}
	if string(ss.replayReply(7)) != "first" {
		t.Fatal("replay")
	}
	if ss.replayReply(8) != nil {
		t.Fatal("miss")
	}
}

func TestGETEchoSurvivesDroppedBody(t *testing.T) {
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
	gs := NewGETServer(token, "/static/v3/build")
	var n atomic.Int32
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rec := httptest.NewRecorder()
		gs.ServeHTTP(rec, r)
		if !cookieHandshake(r) && n.Add(1)%4 == 0 {
			if hj, ok := w.(http.Hijacker); ok {
				c, _, err := hj.Hijack()
				if err == nil {
					_ = c.Close()
					return
				}
			}
		}
		for k, vv := range rec.Header() {
			w.Header()[k] = vv
		}
		w.WriteHeader(rec.Code)
		_, _ = w.Write(rec.Body.Bytes())
	}))
	defer ts.Close()

	g, err := NewGET(token, config.Outbound{
		Type:         "get",
		URL:          ts.URL,
		Path:         "/static/v3/build",
		Fingerprint:  "go",
		HeaderBudget: 4096,
		Inflight:     4,
		Insecure:     true,
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
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
	_ = conn.SetDeadline(time.Now().Add(12 * time.Second))
	want := make([]byte, 8<<10)
	for i := range want {
		want[i] = byte(i)
	}
	if _, err := conn.Write(want); err != nil {
		t.Fatal(err)
	}
	got := make([]byte, len(want))
	if _, err := io.ReadFull(conn, got); err != nil {
		t.Fatal(err)
	}
	if string(got) != string(want) {
		t.Fatal("echo mismatch after dropped GET bodies")
	}
}

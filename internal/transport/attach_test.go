package transport

import (
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/a3app/loft/internal/config"
	ecrypto "github.com/a3app/loft/internal/crypto"
	"github.com/a3app/loft/internal/wire"
)

func attachGET(t *testing.T, token string, caps wire.Cap) (*GET, *GETServer, *httptest.Server) {
	t.Helper()
	gs := NewGETServer(token, "/static/v3/build")
	gs.Info = NodeInfo{Token: []byte(token), Caps: caps}
	ts := httptest.NewServer(http.HandlerFunc(gs.ServeHTTP))
	t.Cleanup(ts.Close)
	g, err := NewGET(token, config.Outbound{
		Type: "get", URL: ts.URL, Path: "/static/v3/build",
		Fingerprint: "go", HeaderBudget: 4096, Inflight: 1, Insecure: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	g.offer = caps
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	t.Cleanup(cancel)
	if err := g.Start(ctx); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = g.Close() })
	return g, gs, ts
}

func attachH2(t *testing.T, g *GET, gs *GETServer) {
	t.Helper()
	h2url := startH2L3(t, &H2Server{Info: gs.Info, Path: "/api/v1/tunnel", Seen: gs.Seen})
	o := attachOutbound(g.cfg, wire.FrontH2, g.fronts)
	o.URL = h2url
	o.Insecure = true
	o.Fingerprint = "go"
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	parent, stream, _, _, err := dialH2CONNECT(ctx, o, g.token)
	if err != nil {
		t.Fatal(err)
	}
	if err := g.runAttach(ctx, pairCloser{ReadWriteCloser: stream, extra: parent}, wire.FrontH2); err != nil {
		t.Fatal(err)
	}
	if !g.attachLive.Load() {
		t.Fatal("attach not live")
	}
}

func pingEcho(t *testing.T, g *GET, addr, want string) {
	t.Helper()
	c, err := g.Open(addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := c.Write([]byte(want)); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, len(want))
	_ = c.SetReadDeadline(time.Now().Add(3 * time.Second))
	if _, err := io.ReadFull(c, buf); err != nil || string(buf) != want {
		t.Fatalf("echo %q %v", buf, err)
	}
}

func TestGETStatusTicket(t *testing.T) {
	const token = "test-token-please-change"
	g, _, _ := attachGET(t, token, wire.CapGET|wire.CapTicket|wire.CapStatus)
	if string(g.ticket) != g.sid || !validGETSID(g.sid) {
		t.Fatalf("ticket %q sid %q", g.ticket, g.sid)
	}
	if len(g.fronts) != 1 || g.fronts[0].Name != wire.FrontGET || g.fronts[0].Path != "/static/v3/build" {
		t.Fatalf("fronts %+v", g.fronts)
	}
}

func TestNodeInfoStatusFronts(t *testing.T) {
	n := NodeInfo{
		Caps:    wire.CapGET | wire.CapWS | wire.CapH2 | wire.CapQUIC | wire.CapStatus,
		GetPath: "/portal/assets",
		WSPath:  "/portal/sync",
		H2Path:  "/portal/tunnel",
	}
	st := n.Status("")
	if len(st.Fronts) != 4 {
		t.Fatalf("fronts %d %+v", len(st.Fronts), st.Fronts)
	}
	if st.Fronts[0].Name != wire.FrontGET || st.Fronts[0].Path != "/portal/assets" {
		t.Fatalf("get %+v", st.Fronts[0])
	}
	if st.Fronts[1].Path != "/portal/sync" || st.Fronts[2].Path != "/portal/tunnel" {
		t.Fatalf("%+v", st.Fronts)
	}
	if st.Fronts[3].Name != wire.FrontQUIC || st.Fronts[3].Path != "" {
		t.Fatalf("quic %+v", st.Fronts[3])
	}
}

func TestAttachOutboundUsesStatusPath(t *testing.T) {
	get := config.Outbound{Type: "get", URL: "https://huy.com"}
	learned := []wire.FrontEP{
		{Name: wire.FrontH2, Path: "/portal/api/tunnel"},
		{Name: wire.FrontWS, Path: "/portal/api/sync"},
	}
	h2 := attachOutbound(get, wire.FrontH2, learned)
	if h2.Path != "/portal/api/tunnel" || h2.URL != "https://huy.com" {
		t.Fatalf("h2 %+v", h2)
	}
	ws := attachOutbound(get, wire.FrontWS, learned)
	if ws.URL != "wss://huy.com/portal/api/sync" {
		t.Fatalf("ws %q", ws.URL)
	}
	def := attachOutbound(get, wire.FrontH2, nil)
	if def.Path != config.DefaultH2Path {
		t.Fatalf("default %q", def.Path)
	}
}

func TestAttachNoTicketSkipsExtras(t *testing.T) {
	const token = "test-token-please-change"
	g, _, _ := attachGET(t, token, wire.CapGET|wire.CapStatus)
	g.attachExtras(context.Background())
	if g.attachLive.Load() {
		t.Fatal("attached without ticket cap")
	}
}

func TestAttachH2SameSession(t *testing.T) {
	echo := echoTCP(t)
	const token = "test-token-please-change"
	caps := wire.CapGET | wire.CapH2 | wire.CapTicket | wire.CapStatus | wire.CapHold
	g, gs, _ := attachGET(t, token, caps)
	attachH2(t, g, gs)
	pingEcho(t, g, echo.Addr().String(), "ping")
}

func TestAttachRejectsBadHMACAndReplay(t *testing.T) {
	const token = "test-token-please-change"
	caps := wire.CapGET | wire.CapH2 | wire.CapTicket | wire.CapStatus
	g, _, _ := attachGET(t, token, caps)
	sa, ca := net.Pipe()
	t.Cleanup(func() { _ = sa.Close(); _ = ca.Close() })
	go func() {
		blob, err := readMsg(sa)
		if err != nil {
			return
		}
		serveAttach(sa, blob, wire.FrontH2)
	}()
	badKey := make([]byte, 32)
	copy(badKey, g.key)
	badKey[0] ^= 0xff
	blob, err := ecrypto.MakeAttach(badKey, g.sid, wire.FrontH2)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeMsg(ca, blob); err != nil {
		t.Fatal(err)
	}
	_ = ca.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := readMsg(ca); err == nil {
		t.Fatal("bad hmac attached")
	}

	sa2, ca2 := net.Pipe()
	t.Cleanup(func() { _ = sa2.Close(); _ = ca2.Close() })
	go func() {
		blob, err := readMsg(sa2)
		if err != nil {
			return
		}
		serveAttach(sa2, blob, wire.FrontH2)
	}()
	good, err := ecrypto.MakeAttach(g.key, g.sid, wire.FrontH2)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeMsg(ca2, good); err != nil {
		t.Fatal(err)
	}
	_ = ca2.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := readMsg(ca2); err != nil {
		t.Fatal(err)
	}
	sa3, ca3 := net.Pipe()
	t.Cleanup(func() { _ = sa3.Close(); _ = ca3.Close() })
	go func() {
		serveAttach(sa3, good, wire.FrontH2)
	}()
	_ = ca3.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := readMsg(ca3); err == nil {
		t.Fatal("replay attached")
	}
}

func TestAttachFrontMismatch(t *testing.T) {
	const token = "test-token-please-change"
	g, _, _ := attachGET(t, token, wire.CapGET|wire.CapTicket|wire.CapStatus)
	blob, err := ecrypto.MakeAttach(g.key, g.sid, wire.FrontWS)
	if err != nil {
		t.Fatal(err)
	}
	sa, ca := net.Pipe()
	t.Cleanup(func() { _ = sa.Close(); _ = ca.Close() })
	go func() {
		b, err := readMsg(sa)
		if err != nil {
			return
		}
		serveAttach(sa, b, wire.FrontH2)
	}()
	if err := writeMsg(ca, blob); err != nil {
		t.Fatal(err)
	}
	_ = ca.SetReadDeadline(time.Now().Add(2 * time.Second))
	if _, err := readMsg(ca); err == nil {
		t.Fatal("ws blob on h2 attached")
	}
}

func TestAttachDropFallsBackToGET(t *testing.T) {
	echo := echoTCP(t)
	const token = "test-token-please-change"
	caps := wire.CapGET | wire.CapH2 | wire.CapTicket | wire.CapStatus | wire.CapHold
	g, gs, _ := attachGET(t, token, caps)
	attachH2(t, g, gs)
	g.closeAttach()
	deadline := time.Now().Add(2 * time.Second)
	for g.attachLive.Load() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	pingEcho(t, g, echo.Addr().String(), "pong")
}

func TestAttachWSSameSession(t *testing.T) {
	echo := echoTCP(t)
	const token = "test-token-please-change"
	caps := wire.CapGET | wire.CapWS | wire.CapTicket | wire.CapStatus | wire.CapHold
	g, gs, _ := attachGET(t, token, caps)
	hs := httptest.NewServer(&WSSServer{Token: []byte(token), Info: gs.Info, Seen: gs.Seen})
	t.Cleanup(hs.Close)
	o := attachOutbound(g.cfg, wire.FrontWS, g.fronts)
	o.URL = "ws" + strings.TrimPrefix(hs.URL, "http")
	o.Insecure = true
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := g.attachWS(ctx, o); err != nil {
		t.Fatal(err)
	}
	if !g.attachLive.Load() {
		t.Fatal("ws attach not live")
	}
	pingEcho(t, g, echo.Addr().String(), "ping-ws")
}

func TestAttachExtrasSkippedWithoutCapTicket(t *testing.T) {
	const token = "test-token-please-change"
	g, _, _ := attachGET(t, token, wire.CapGET|wire.CapH2|wire.CapStatus)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	g.attachExtras(ctx)
	if g.attachLive.Load() {
		t.Fatal("auto-attach without CapTicket")
	}
}

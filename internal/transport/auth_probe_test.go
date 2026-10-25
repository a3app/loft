package transport

import (
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"
	"github.com/quic-go/quic-go"

	"github.com/a3app/loft/internal/config"
	ecrypto "github.com/a3app/loft/internal/crypto"
	"github.com/a3app/loft/internal/wire"
)

// probeAuth is a well-formed 0x02 under a key the node does not hold: the
// prober picks eph_c, so any sealed reply would open for it.
func probeAuth(t *testing.T) []byte {
	t.Helper()
	proof, _, _, err := ecrypto.MakeAuth([]byte("lt_000000000000000000000000000000000000000000000000"))
	if err != nil {
		t.Fatal(err)
	}
	return append(proof, newClientHello(wire.MemberCaps).Encode()...)
}

type probeReply struct {
	code int
	ct   string
	body string
}

func probeGET(t *testing.T, url, cookie string) probeReply {
	t.Helper()
	req, err := http.NewRequest(http.MethodGet, url, nil)
	if err != nil {
		t.Fatal(err)
	}
	if cookie != "" {
		req.Header.Set("Cookie", "session="+cookie)
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return probeReply{resp.StatusCode, resp.Header.Get("Content-Type"), string(b)}
}

// TestGETWrongKeyIsAMiss: a 0x02 Cookie with a foreign key gets the same reply
// as a GET with no Cookie at all (§3.4), not a Status the prober can open.
func TestGETWrongKeyIsAMiss(t *testing.T) {
	gs := NewGETServer("test-token-please-change", "/static/v3/build")
	ts := httptest.NewServer(http.HandlerFunc(gs.ServeHTTP))
	t.Cleanup(ts.Close)
	url := ts.URL + "/static/v3/build/" + ecrypto.RandomID() + "/0.js"

	bare := probeGET(t, url, "")
	probe := probeGET(t, url, wire.EncodeUplink(probeAuth(t), wire.UplinkBase64))
	if probe.code != http.StatusNotFound {
		t.Fatalf("wrong key: HTTP %d, want 404", probe.code)
	}
	if probe != bare {
		t.Fatalf("wrong key reply differs from a miss:\n%+v\n%+v", probe, bare)
	}
}

// TestWSWrongKeyNoStatus: a WebSocket with a foreign key is closed without a
// sealed Status, as h2, quic, and h3 already do.
func TestWSWrongKeyNoStatus(t *testing.T) {
	hs := httptest.NewServer(&WSSServer{Token: []byte("test-token-please-change")})
	t.Cleanup(hs.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	c, _, err := websocket.Dial(ctx, "ws"+strings.TrimPrefix(hs.URL, "http"), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer c.CloseNow()
	if err := c.Write(ctx, websocket.MessageBinary, probeAuth(t)); err != nil {
		t.Fatal(err)
	}
	if _, b, err := c.Read(ctx); err == nil {
		t.Fatalf("node answered a foreign key with %d bytes", len(b))
	}
}

func wantRefused(t *testing.T, err error) {
	t.Helper()
	var we wire.Error
	if !errors.As(err, &we) || we.Code != wire.AuthBadKey || !fatalFront(err) {
		t.Fatalf("want inferred auth_bad_key, got %v", err)
	}
}

// TestWSWrongKeyIsAuthBadKey: the ws client reads the close after 0x02 as a
// key problem although the node sent no Status.
func TestWSWrongKeyIsAuthBadKey(t *testing.T) {
	gs := NewGETServer("test-token-please-change", "/static/v3/build")
	hs := httptest.NewServer(&WSSServer{Token: gs.Token, Info: gs.Info, Seen: gs.Seen})
	t.Cleanup(hs.Close)
	w := NewWSS("revoked-key", config.Outbound{
		Type: "ws", URL: "ws" + strings.TrimPrefix(hs.URL, "http"), Fingerprint: "go", Insecure: true,
	})
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	wantRefused(t, w.Start(ctx))
}

// TestH2WrongKeyIsAuthBadKey: same for HTTP/2 CONNECT.
func TestH2WrongKeyIsAuthBadKey(t *testing.T) {
	gs := NewGETServer("test-token-please-change", "/static/v3/build")
	url := startH2L3(t, &H2Server{Info: gs.Info, Path: "/api/v1/tunnel", Seen: gs.Seen})
	h := NewH2("revoked-key", config.Outbound{
		Type: "h2", URL: url, Path: "/api/v1/tunnel", Fingerprint: "go", Insecure: true,
	}, wire.MemberCaps)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	wantRefused(t, h.Start(ctx))
}

type timeoutErr struct{}

func (timeoutErr) Error() string   { return "i/o timeout" }
func (timeoutErr) Timeout() bool   { return true }
func (timeoutErr) Temporary() bool { return true }

// TestAfterProofKeepsNetworkErrors: only a clean close is a refusal.
func TestAfterProofKeepsNetworkErrors(t *testing.T) {
	var _ net.Error = timeoutErr{}
	for _, err := range []error{
		context.DeadlineExceeded,
		timeoutErr{},
		errors.New("read tcp: connection reset by peer"),
		&quic.ApplicationError{Remote: false, ErrorCode: 0},
		&quic.ApplicationError{Remote: true, ErrorCode: 7},
	} {
		if inferredRefusal(afterProof(err)) {
			t.Fatalf("%v read as a key refusal", err)
		}
	}
	for _, err := range []error{io.EOF, io.ErrUnexpectedEOF, &quic.ApplicationError{Remote: true}} {
		if !inferredRefusal(afterProof(err)) {
			t.Fatalf("%v not read as a key refusal", err)
		}
	}
}

// TestStartSelectedRefusalNeedsEveryFront: one front refuses, another is
// unreachable; that is not a key problem, so Supervise keeps retrying.
func TestStartSelectedRefusalNeedsEveryFront(t *testing.T) {
	gs := NewGETServer("test-token-please-change", "/static/v3/build")
	live := httptest.NewServer(http.HandlerFunc(gs.ServeHTTP))
	t.Cleanup(live.Close)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	dead := "ws://" + ln.Addr().String()
	ln.Close()
	rotateN.Store(0)
	off := false
	cfg := &config.ClientConfig{
		Token:  "revoked-key",
		Select: "auto",
		P2P:    &off,
		Outbounds: []config.Outbound{
			{Name: "node", Type: "get", URL: live.URL, Path: "/static/v3/build", Fingerprint: "go", HeaderBudget: 4096, Inflight: 2, Insecure: true},
			{Name: "down", Type: "ws", URL: dead, Fingerprint: "go", Insecure: true},
		},
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	_, _, err = StartSelected(ctx, cfg)
	if err == nil || fatalFront(err) {
		t.Fatalf("mixed failure must stay retryable, got %v", err)
	}
	cfg.Outbounds = cfg.Outbounds[:1]
	_, _, err = StartSelected(ctx, cfg)
	wantRefused(t, err)
}

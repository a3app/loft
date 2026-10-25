package transport

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/coder/websocket"

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

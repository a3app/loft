package transport

import (
	"context"
	"net"
	"testing"
	"time"

	"github.com/a3app/loft/internal/config"
	ecrypto "github.com/a3app/loft/internal/crypto"
	"github.com/a3app/loft/internal/wire"
)

// shapeSeg is an IPv4 TCP segment from 10.8.0.2:40000 to 1.1.1.1:443 (or
// back when rev) with flags and size bytes of payload.
func shapeSeg(flags byte, size int, rev bool) []byte {
	p := make([]byte, 40+size)
	p[0] = 0x45
	p[2], p[3] = byte(len(p)>>8), byte(len(p))
	p[8] = 64
	p[9] = ipProtoTCP
	a, b := []byte{10, 8, 0, 2}, []byte{1, 1, 1, 1}
	pa, pb := []byte{0x9c, 0x40}, []byte{0x01, 0xbb}
	if rev {
		a, b, pa, pb = b, a, pb, pa
	}
	copy(p[12:16], a)
	copy(p[16:20], b)
	copy(p[20:22], pa)
	copy(p[22:24], pb)
	p[20+12] = 5 << 4
	p[20+13] = flags
	return p
}

func TestL3ShapeFlowStart(t *testing.T) {
	f := newL3ShapeFlows()
	if f.noteSend(shapeSeg(tcpACK, 100, false)) {
		t.Fatal("flow without SYN counted")
	}
	if !f.noteSend(shapeSeg(tcpSYN, 0, false)) {
		t.Fatal("SYN not a start")
	}
	if f.noteSend(shapeSeg(tcpACK, 0, false)) {
		t.Fatal("bare ACK counted as start")
	}
	for i := 1; i <= l3ShapeFirst; i++ {
		if !f.noteSend(shapeSeg(tcpACK|tcpPSH, 300, false)) {
			t.Fatalf("data %d not a start", i)
		}
	}
	if f.noteSend(shapeSeg(tcpACK|tcpPSH, 300, false)) {
		t.Fatal("start did not end")
	}
	// The reply direction maps to the same flow: first data answers once.
	if !f.noteRecv(shapeSeg(tcpACK|tcpPSH, 1400, true)) {
		t.Fatal("first reply data missed")
	}
	if f.noteRecv(shapeSeg(tcpACK|tcpPSH, 1400, true)) {
		t.Fatal("second reply data answered")
	}
	f.noteSend(shapeSeg(tcpFIN|tcpACK, 0, false))
	if len(f.m) != 0 {
		t.Fatal("FIN left the flow")
	}
}

func TestL3PaddedRoundTrip(t *testing.T) {
	p1, p2 := shapeSeg(tcpSYN, 0, false), shapeSeg(tcpACK|tcpPSH, 517, false)
	plain := wire.EncodeL3Padded([][]byte{p1, p2}, 2048)
	if len(plain) != 2048 {
		t.Fatalf("len %d", len(plain))
	}
	got, err := wire.SplitL3(plain)
	if err != nil || len(got) != 2 || len(got[1]) != len(p2) {
		t.Fatalf("got %d err=%v", len(got), err)
	}
	only, err := wire.SplitL3(wire.EncodeL3Padded(nil, 1500))
	if err != nil || len(only) != 0 {
		t.Fatalf("pad-only: %d %v", len(only), err)
	}
}

func TestL3ShapeBucket(t *testing.T) {
	for _, c := range []struct{ in, want int }{{1, 512}, {600, 1024}, {1025, 2048}, {9000, 16384}, {20000, 20000}} {
		if got := l3ShapeBucket(c.in); got != c.want {
			t.Fatalf("bucket(%d)=%d want %d", c.in, got, c.want)
		}
	}
}

// readPlain reads one sealed l3 message from c.
func readPlain(t *testing.T, c net.Conn, key []byte, wait time.Duration) []byte {
	t.Helper()
	_ = c.SetReadDeadline(time.Now().Add(wait))
	raw, err := readMsg(c)
	if err != nil {
		t.Fatal(err)
	}
	plain, err := ecrypto.Open(key, raw, nil)
	if err != nil {
		t.Fatal(err)
	}
	return plain
}

func TestL3ShapeCorksStartAndPads(t *testing.T) {
	a, b := net.Pipe()
	t.Cleanup(func() { _ = a.Close(); _ = b.Close() })
	key := make([]byte, 32)
	s := &l3Sess{conn: a, key: key, in: make(chan []byte, 8), done: make(chan struct{})}
	s.enableShape()
	start := time.Now()
	if err := s.WriteIP(shapeSeg(tcpSYN, 0, false)); err != nil {
		t.Fatal(err)
	}
	plain := readPlain(t, b, key, time.Second)
	if waited := time.Since(start); waited < l3ShapeWait/2 {
		t.Fatalf("SYN flushed at once (%v)", waited)
	}
	if plain[0] != wire.L3Padded || len(plain) != l3ShapeMin {
		t.Fatalf("type %d len %d", plain[0], len(plain))
	}
	// After the start, data goes the usual way: unpadded batches.
	for i := 0; i < l3ShapeFirst; i++ {
		_ = s.WriteIP(shapeSeg(tcpACK|tcpPSH, 200, false))
		readPlain(t, b, key, time.Second)
	}
	_ = s.WriteIP(shapeSeg(tcpACK|tcpPSH, 200, false))
	if p := readPlain(t, b, key, time.Second); p[0] == wire.L3Padded {
		t.Fatal("bulk data padded")
	}
}

func TestL3ShapeAnswersFirstData(t *testing.T) {
	a, b := net.Pipe()
	t.Cleanup(func() { _ = a.Close(); _ = b.Close() })
	key := make([]byte, 32)
	s := &l3Sess{conn: a, key: key, in: make(chan []byte, 8), done: make(chan struct{})}
	s.enableShape()
	s.noteRecv(shapeSeg(tcpSYN, 0, false)) // the peer opens the flow
	s.noteRecv(shapeSeg(tcpACK|tcpPSH, 517, false))
	plain := readPlain(t, b, key, time.Second)
	pk, err := wire.SplitL3(plain)
	if plain[0] != wire.L3Padded || err != nil || len(pk) != 0 || len(plain) < l3ChaffMin {
		t.Fatalf("type %d len %d pkts %d err %v", plain[0], len(plain), len(pk), err)
	}
}

// TestL3ShapeAgreed runs a real l3 session where the client asks for
// shaping: the node echoes the bit, both sides shape, and ping still works.
func TestL3ShapeAgreed(t *testing.T) {
	withOverlay(t)
	EnableHTTP2ExtendedConnect()
	SetL3Shape(true)
	defer SetL3Shape(false)
	const token = "l3-shape-token"
	h2s := &H2Server{Info: NodeInfo{Token: []byte(token), Caps: wire.NodeCaps}, Path: "/api/v1/tunnel"}
	url := startH2L3(t, h2s)
	l := NewL3(token, config.Outbound{Type: "l3", URL: url, Path: "/api/v1/tunnel", Fingerprint: "go", Insecure: true}, wire.CapL3)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	if err := l.Start(ctx); err != nil {
		t.Fatal(err)
	}
	defer l.Close()
	if !l.inner.shape {
		t.Fatal("node did not agree to shaping")
	}
	ip := OverlayIP()
	req := makeEchoPkt(net.ParseIP(ip), net.ParseIP("10.8.0.1"), []byte("ping"))
	if err := l.WriteIP(req); err != nil {
		t.Fatal(err)
	}
	done := make(chan []byte, 1)
	go func() {
		if p, err := l.ReadIP(); err == nil {
			done <- p
		}
	}()
	select {
	case got := <-done:
		ihl := int(got[0]&0x0f) * 4
		if got[ihl] != icmpReply {
			t.Fatalf("type %d", got[ihl])
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timeout")
	}
}

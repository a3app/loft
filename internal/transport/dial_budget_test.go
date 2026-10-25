package transport

import (
	"bytes"
	"context"
	"testing"
	"time"

	"github.com/a3app/loft/internal/config"
	"github.com/a3app/loft/internal/wire"
)

func TestDialTLSContextHonorsCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	start := time.Now()
	_, err := dialTLSContext(ctx, "192.0.2.1:443", "example.com", "chrome", true, nil)
	if err == nil {
		t.Fatal("dial")
	}
	if d := time.Since(start); d > time.Second {
		t.Fatalf("cancel took %s", d)
	}
}

func TestMarkReadySkipsUnmarkedQUIC(t *testing.T) {
	o := config.Outbound{Type: wire.FrontQUIC, URL: "https://quic-mark-ready.test:444"}
	if markReady(o) {
		t.Fatal("quic without mask")
	}
	if !markReady(config.Outbound{Type: wire.FrontWS, URL: o.URL}) {
		t.Fatal("ws")
	}
	pub := bytes.Repeat([]byte{7}, 32)
	RememberMaskPub("quic-mark-ready.test:444", pub)
	if !markReady(o) {
		t.Fatal("cached pub")
	}
	if markReady(config.Outbound{Type: wire.FrontH3, URL: "https://h3-mark-ready-miss.test:9"}) {
		t.Fatal("h3 without mask")
	}
}

func TestPublishMaskStampsPeers(t *testing.T) {
	cdn := config.Outbound{Type: "get", URL: "https://cdn.publish-mask.test"}
	node := config.Outbound{Type: "get", URL: "https://node.publish-mask.test:444"}
	NoteOutbound(cdn)
	NoteOutbound(node)
	pub := bytes.Repeat([]byte{0xab}, 32)
	publishMask(pub)
	ca, err := outboundAddr(cdn)
	if err != nil {
		t.Fatal(err)
	}
	na, err := outboundAddr(node)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(CachedMaskPub(ca), pub) || !bytes.Equal(CachedMaskPub(na), pub) {
		t.Fatalf("cdn %x node %x", CachedMaskPub(ca), CachedMaskPub(na))
	}
}

package transport

// Tests for named-list lookup priority: drop, then direct, then via, then default.

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/a3app/loft/internal/config"
	"github.com/a3app/loft/internal/testcfg"
	"github.com/a3app/loft/internal/wire"
)

// TestLookupPriority checks drop wins over direct on the same host, then default via.
func TestLookupPriority(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	l := wire.Lists{
		Serial: 1,
		Sets: []wire.AddrList{
			{Name: "yandex", Entries: []wire.Addr{wire.ParseAddr(testcfg.Decoy2())}},
			{Name: "drop", Entries: []wire.Addr{wire.ParseAddr(testcfg.Decoy2())}},
			{Name: "via", Entries: []wire.Addr{wire.ParseAddr(testcfg.CDN())}},
		},
	}
	l.Pub = priv.Public().(ed25519.PublicKey)
	l.Sig = ed25519.Sign(priv, l.Body())
	SetUse(&config.ClientConfig{Use: map[string]string{"yandex": "direct", "drop": "drop", "via": "via"}, Default: "via"})
	RememberLists(l)
	if LookupAddr(testcfg.Decoy2(), nil) != ActDrop {
		t.Fatal("drop wins")
	}
	if LookupAddr(testcfg.CDN(), nil) != ActVia {
		t.Fatal("via list")
	}
	if LookupAddr("example.com", nil) != ActVia {
		t.Fatal("default")
	}
	if LookupAddr("", net.ParseIP("8.8.8.8")) != ActVia {
		t.Fatal("ip default")
	}
}

func TestBootstrapListsSkipsNodeWhenEdgeIsSet(t *testing.T) {
	var hit atomic.Bool
	hang := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		hit.Store(true)
		time.Sleep(time.Second)
	}))
	t.Cleanup(hang.Close)
	edge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.NotFound(w, r)
	}))
	t.Cleanup(edge.Close)
	node := "http://" + hang.Listener.Addr().String()
	SetDirectNode(node)
	t.Cleanup(func() { SetDirectNode("") })
	cfg := &config.ClientConfig{
		Outbounds: []config.Outbound{
			{Name: "edge", Type: "get", CDN: "yandex", URL: edge.URL},
			{Name: "node", Type: "get", CDN: "yandex", URL: node},
			{Name: "h2", Type: "h2", CDN: "yandex", URL: node + "/api/v1/tunnel"},
		},
	}
	start := time.Now()
	BootstrapLists(context.Background(), cfg)
	if time.Since(start) > 500*time.Millisecond {
		t.Fatalf("took %s", time.Since(start))
	}
	if hit.Load() {
		t.Fatal("GET lists on node")
	}
}

func TestBootstrapListsSkipsDirectNode(t *testing.T) {
	var hit atomic.Bool
	ts := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		hit.Store(true)
		time.Sleep(time.Second)
	}))
	t.Cleanup(ts.Close)
	origin := "http://" + ts.Listener.Addr().String()
	cfg := &config.ClientConfig{
		Bootstrap: origin + "/static/v3/manifest.json",
		ListsURL:  origin + "/static/v3/lists.json",
		Outbounds: []config.Outbound{{Name: "node", Type: "ws", URL: origin + "/api/v1/sync"}},
	}
	start := time.Now()
	BootstrapLists(context.Background(), cfg)
	if time.Since(start) > 200*time.Millisecond {
		t.Fatalf("took %s", time.Since(start))
	}
	if hit.Load() {
		t.Fatal("GET lists on decoy node")
	}
}

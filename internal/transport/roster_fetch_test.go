package transport

// Tests for fetching a signed roster JSON manifest.

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"

	"github.com/a3app/loft/internal/config"
	"github.com/a3app/loft/internal/wire"
)

// TestFetchRosterManifest verifies a signed manifest and caches the relay list.
func TestFetchRosterManifest(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	r := wire.Roster{Serial: 5, Relays: []wire.RelayInfo{{ID: "ru1", URL: "https://edge.example", Path: "/api/v1/sync"}}}
	r.Pub = priv.Public().(ed25519.PublicKey)
	r.Sig = ed25519.Sign(priv, r.Body())
	j, err := r.ManifestJSON()
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(j)
	}))
	defer ts.Close()
	t.Cleanup(resetRosterForTest)
	resetRosterForTest()
	PinControllerPub(r.Pub)
	if err := FetchRoster(context.Background(), ts.URL); err != nil {
		t.Fatal(err)
	}
	got := CachedRoster()
	if len(got) != 1 || got[0].ID != "ru1" || got[0].Path != "/api/v1/sync" {
		t.Fatalf("%+v", got)
	}
}

func hangingManifest(t *testing.T) (*httptest.Server, *atomic.Bool) {
	t.Helper()
	var hit atomic.Bool
	ts := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {
		hit.Store(true)
		time.Sleep(time.Second)
	}))
	t.Cleanup(ts.Close)
	return ts, &hit
}

func TestBootstrapRosterSkipsDirectNode(t *testing.T) {
	ts, hit := hangingManifest(t)
	t.Cleanup(resetRosterForTest)
	resetRosterForTest()
	cfg := &config.ClientConfig{
		Outbounds: []config.Outbound{{Name: "node", Type: "ws", URL: ts.URL + "/api/v1/sync"}},
	}
	start := time.Now()
	BootstrapRoster(context.Background(), cfg)
	if time.Since(start) > 200*time.Millisecond {
		t.Fatalf("took %s", time.Since(start))
	}
	if hit.Load() {
		t.Fatal("GET to decoy node")
	}
}

func TestBootstrapRosterSkipsBootstrapOnNode(t *testing.T) {
	ts, hit := hangingManifest(t)
	t.Cleanup(resetRosterForTest)
	resetRosterForTest()
	origin := "http://" + ts.Listener.Addr().String()
	cfg := &config.ClientConfig{
		Bootstrap: origin + "/static/v3/manifest.json",
		Outbounds: []config.Outbound{{Name: "node", Type: "get", URL: origin}},
	}
	start := time.Now()
	BootstrapRoster(context.Background(), cfg)
	if time.Since(start) > 200*time.Millisecond {
		t.Fatalf("took %s", time.Since(start))
	}
	if hit.Load() {
		t.Fatal("GET bootstrap on decoy node")
	}
}

func TestBootstrapRosterFetchesCDN(t *testing.T) {
	_, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	r := wire.Roster{Serial: 3, Relays: []wire.RelayInfo{{ID: "cdn1", URL: "https://edge.example"}}}
	r.Pub = priv.Public().(ed25519.PublicKey)
	r.Sig = ed25519.Sign(priv, r.Body())
	j, err := r.ManifestJSON()
	if err != nil {
		t.Fatal(err)
	}
	ts := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(j)
	}))
	defer ts.Close()
	t.Cleanup(resetRosterForTest)
	resetRosterForTest()
	PinControllerPub(r.Pub)
	cfg := &config.ClientConfig{
		Outbounds: []config.Outbound{{Name: "cdn", Type: "get", CDN: "yandex", URL: ts.URL}},
	}
	BootstrapRoster(context.Background(), cfg)
	got := CachedRoster()
	if len(got) != 1 || got[0].ID != "cdn1" {
		t.Fatalf("%+v", got)
	}
}

func TestBootstrapRosterSkipsWhenCached(t *testing.T) {
	ts, hit := hangingManifest(t)
	t.Cleanup(resetRosterForTest)
	resetRosterForTest()
	rosterMu.Lock()
	heldRoster = wire.Roster{Relays: []wire.RelayInfo{{ID: "self", URL: "https://edge.example"}}}
	rosterMu.Unlock()
	cfg := &config.ClientConfig{
		Outbounds: []config.Outbound{{Name: "cdn", Type: "get", CDN: "yandex", URL: ts.URL}},
	}
	start := time.Now()
	BootstrapRoster(context.Background(), cfg)
	if time.Since(start) > 200*time.Millisecond {
		t.Fatalf("took %s", time.Since(start))
	}
	if hit.Load() {
		t.Fatal("refetched with roster already held")
	}
}

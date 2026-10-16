package transport

// Tests for fetching a signed roster JSON manifest.

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"net/http"
	"net/http/httptest"
	"testing"

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
	rosterMu.Lock()
	heldRoster = wire.Roster{}
	pinnedPub = nil
	rosterMu.Unlock()
	if err := FetchRoster(context.Background(), ts.URL); err != nil {
		t.Fatal(err)
	}
	got := CachedRoster()
	if len(got) != 1 || got[0].ID != "ru1" || got[0].Path != "/api/v1/sync" {
		t.Fatalf("%+v", got)
	}
}

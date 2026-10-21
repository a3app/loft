package transport

// Per-member keys by default; AuthShared re-enables LOFT_TOKEN as client secret.

import (
	"os"
	"testing"

	"github.com/a3app/loft/internal/identity"
)

// TestMain keeps front tests on the old empty-ring shortcut (GET/WS lab nodes).
func TestMain(m *testing.M) {
	AuthShared = true
	os.Exit(m.Run())
}

func TestClientTokensRingIgnoresShared(t *testing.T) {
	t.Cleanup(func() {
		AuthKeys = nil
		AuthShared = true
	})
	ring := identity.NewRing()
	ring.Put("alice", "lt_alice")
	AuthKeys = ring
	AuthShared = true
	got := clientTokens([]byte("node-secret"))
	if len(got) != 1 || string(got[0]) != "lt_alice" {
		t.Fatalf("ring mixed node token: %q", got)
	}
}

func TestClientTokensSharedEmptyRing(t *testing.T) {
	t.Cleanup(func() {
		AuthKeys = nil
		AuthShared = true
	})
	AuthKeys = nil
	AuthShared = true
	got := clientTokens([]byte("node-secret"))
	if len(got) != 1 || string(got[0]) != "node-secret" {
		t.Fatalf("shared off? %q", got)
	}
}

func TestClientTokensNoSharedEmptyRing(t *testing.T) {
	t.Cleanup(func() {
		AuthKeys = nil
		AuthShared = true
	})
	AuthKeys = nil
	AuthShared = false
	if got := clientTokens([]byte("node-secret")); len(got) != 0 {
		t.Fatalf("wanted no client tokens, got %q", got)
	}
}

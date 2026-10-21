package transport

// Tests for instance file round-trip and Hello InstanceID.

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// resetInstance clears the process-wide instance id so tests do not leak files.
func resetInstance(t *testing.T) {
	t.Helper()
	instMu.Lock()
	instID = ""
	instFile = ""
	instMu.Unlock()
	t.Cleanup(func() {
		instMu.Lock()
		instID = ""
		instFile = ""
		instMu.Unlock()
	})
}

// TestInstanceFileRoundTrip persists EnsureInstance and RememberInstance across reload.
func TestInstanceFileRoundTrip(t *testing.T) {
	resetInstance(t)
	path := filepath.Join(t.TempDir(), "client.instance")
	SetInstanceFile(path)
	id := EnsureInstance()
	if id == "" {
		t.Fatal("empty")
	}
	b, err := os.ReadFile(path)
	if err != nil || strings.TrimSpace(string(b)) != id {
		t.Fatalf("file %q %v", b, err)
	}
	instMu.Lock()
	instID = ""
	instMu.Unlock()
	SetInstanceFile(path)
	if ClientInstance() != id {
		t.Fatalf("reload %q want %q", ClientInstance(), id)
	}
	RememberInstance("deadbeefcafebabe")
	instMu.Lock()
	instID = ""
	instMu.Unlock()
	SetInstanceFile(path)
	if ClientInstance() != "deadbeefcafebabe" {
		t.Fatalf("status id lost: %q", ClientInstance())
	}
}

// TestHelloCarriesInstance stamps the same EnsureInstance id on every Hello.
func TestHelloCarriesInstance(t *testing.T) {
	resetInstance(t)
	a := newClientHello(0)
	b := newClientHello(0)
	if a.InstanceID == "" || a.InstanceID != b.InstanceID {
		t.Fatalf("%q %q", a.InstanceID, b.InstanceID)
	}
}

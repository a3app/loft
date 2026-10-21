package identity

// members.keys load/save and Filter against network.conf hashes.

import (
	"os"
	"path/filepath"
	"testing"
)

// TestRingLoadSaveFilter drops keys whose SHA-256 is not in want.
func TestRingLoadSaveFilter(t *testing.T) {
	dir := t.TempDir()
	p := filepath.Join(dir, "members.keys")
	plain, sha, err := NewKey()
	if err != nil {
		t.Fatal(err)
	}
	other, otherSHA, err := NewKey()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("alice: "+plain+"\nbob: "+other+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	r, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if r.Len() != 2 {
		t.Fatalf("len %d", r.Len())
	}
	if r.IDFor([]byte(plain)) != "alice" {
		t.Fatal("idfor")
	}
	r.Filter(map[string]string{"alice": sha, "bob": "deadbeef"})
	if r.Len() != 1 || r.IDFor([]byte(plain)) != "alice" || r.IDFor([]byte(other)) != "" {
		t.Fatalf("filter kept %d alice=%q bob=%q", r.Len(), r.IDFor([]byte(plain)), r.IDFor([]byte(other)))
	}
	_ = otherSHA
	if err := r.Save(p); err != nil {
		t.Fatal(err)
	}
	again, err := Load(p)
	if err != nil {
		t.Fatal(err)
	}
	if again.Len() != 1 || again.IDFor([]byte(plain)) != "alice" {
		t.Fatal("reload")
	}
}

// TestRingMissingFile treats a missing members.keys as an empty ring.
func TestRingMissingFile(t *testing.T) {
	r, err := Load(filepath.Join(t.TempDir(), "nope"))
	if err != nil || r.Len() != 0 {
		t.Fatalf("%v len=%d", err, r.Len())
	}
}

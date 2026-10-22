package identity

// members.keys load/save and Filter against network.conf hashes.

import (
	"os"
	"path/filepath"
	"strings"
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

// TestRingReissueKeepsOldUntilFilterMatch: Put of a second token does not
// drop the first; Filter by the old SHA keeps the member; Filter by the new
// SHA keeps only the new token.
func TestRingReissueKeepsOldUntilFilterMatch(t *testing.T) {
	old, oldSHA, err := NewKey()
	if err != nil {
		t.Fatal(err)
	}
	fresh, freshSHA, err := NewKey()
	if err != nil {
		t.Fatal(err)
	}
	r := NewRing()
	r.Put("alice", old)
	r.Put("alice", fresh)
	if r.Len() != 1 || r.IDFor([]byte(old)) != "alice" || r.IDFor([]byte(fresh)) != "alice" {
		t.Fatalf("both tokens for one id: len=%d old=%q new=%q", r.Len(), r.IDFor([]byte(old)), r.IDFor([]byte(fresh)))
	}
	if string(r.TokenFor("alice")) != fresh {
		t.Fatalf("TokenFor want latest")
	}
	dir := t.TempDir()
	p := filepath.Join(dir, "members.keys")
	if err := r.Save(p); err != nil {
		t.Fatal(err)
	}
	raw, _ := os.ReadFile(p)
	got := string(raw)
	if !strings.Contains(got, "alice:") || !strings.Contains(got, "- "+old) || !strings.Contains(got, "- "+fresh) {
		t.Fatalf("save list:\n%s", raw)
	}
	again, err := Load(p)
	if err != nil || again.Len() != 1 || again.IDFor([]byte(old)) != "alice" || again.IDFor([]byte(fresh)) != "alice" {
		t.Fatalf("reload list %v len=%d", err, again.Len())
	}
	again.Filter(map[string]string{"alice": oldSHA})
	if again.Len() != 1 || again.IDFor([]byte(old)) != "alice" || again.IDFor([]byte(fresh)) != "" {
		t.Fatalf("filter old sha dropped the live key: len=%d", again.Len())
	}
	r.Filter(map[string]string{"alice": freshSHA})
	if r.Len() != 1 || r.IDFor([]byte(fresh)) != "alice" || r.IDFor([]byte(old)) != "" {
		t.Fatalf("filter new sha kept old: len=%d", r.Len())
	}
	if err := r.Save(p); err != nil {
		t.Fatal(err)
	}
	raw, _ = os.ReadFile(p)
	if string(raw) != "alice: "+fresh+"\n" {
		t.Fatalf("save scalar after prune:\n%s", raw)
	}
}

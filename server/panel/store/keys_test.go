package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestMemberKeysRoundTrip(t *testing.T) {
	dir := t.TempDir()
	k := OpenMemberKeys(dir)
	if _, ok, err := k.Get("alice"); err != nil || ok {
		t.Fatalf("empty get: ok=%v err=%v", ok, err)
	}
	if err := k.Put("alice", "lt_aaa", "sha-a"); err != nil {
		t.Fatal(err)
	}
	st, err := os.Stat(filepath.Join(dir, MemberKeysFile))
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("mode %v %v", st.Mode(), err)
	}
	got, ok, err := k.Get("alice")
	if err != nil || !ok || got.Key != "lt_aaa" || got.SHA256 != "sha-a" {
		t.Fatalf("get %+v ok=%v err=%v", got, ok, err)
	}
	k2 := OpenMemberKeys(dir)
	got, ok, err = k2.Get("alice")
	if err != nil || !ok || got.Key != "lt_aaa" {
		t.Fatalf("reload %+v ok=%v err=%v", got, ok, err)
	}
	if err := k.Put("bob", "lt_bbb", "sha-b"); err != nil {
		t.Fatal(err)
	}
	if err := k.Delete("alice"); err != nil {
		t.Fatal(err)
	}
	if _, ok, _ := k.Get("alice"); ok {
		t.Fatal("alice still there")
	}
	if _, ok, _ := k.Get("bob"); !ok {
		t.Fatal("bob gone")
	}
	if err := k.Wipe(); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, MemberKeysFile)); !os.IsNotExist(err) {
		t.Fatalf("wipe left the file: %v", err)
	}
	if _, ok, err := k.Get("bob"); err != nil || ok {
		t.Fatalf("after wipe ok=%v err=%v", ok, err)
	}
	if err := k.Wipe(); err != nil {
		t.Fatal(err)
	}
	if err := k.Put("", "lt_x", "s"); err == nil {
		t.Fatal("empty id accepted")
	}
}

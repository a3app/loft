package store

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStoreRoundTrip(t *testing.T) {
	dir := t.TempDir()
	s, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	if !s.Empty() {
		t.Fatal("new store not empty")
	}
	if err := s.Add(User{Login: "admin", Role: RoleOwner, Hash: "h"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Add(User{Login: "bob", Role: RoleOperator, Hash: "h"}); err != nil {
		t.Fatal(err)
	}
	if err := s.Add(User{Login: "bob", Role: RoleViewer}); err != ErrExists {
		t.Fatalf("duplicate: %v", err)
	}
	st, err := os.Stat(filepath.Join(dir, "users.json"))
	if err != nil || st.Mode().Perm() != 0o600 {
		t.Fatalf("users.json mode %v %v", st.Mode(), err)
	}
	s2, err := Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	l := s2.List()
	if len(l) != 2 || l[0].Login != "admin" || l[1].Role != RoleOperator || l[0].Created.IsZero() {
		t.Fatalf("reload %+v", l)
	}
	left, _ := filepath.Glob(filepath.Join(dir, ".users.json.*"))
	if len(left) != 0 {
		t.Fatalf("temp files left: %v", left)
	}
}

func TestStoreValidation(t *testing.T) {
	s, _ := Open(t.TempDir())
	for _, l := range []string{"", "Admin", "1abc", "a b", "алиса", "a234567890123456789012345678901234"} {
		if err := s.Add(User{Login: l, Role: RoleViewer}); err == nil {
			t.Fatalf("login %q accepted", l)
		}
	}
	if err := s.Add(User{Login: "x", Role: "root"}); err == nil {
		t.Fatal("unknown role accepted")
	}
}

func TestStoreLastOwner(t *testing.T) {
	s, _ := Open(t.TempDir())
	s.Add(User{Login: "admin", Role: RoleOwner})
	s.Add(User{Login: "bob", Role: RoleOperator})
	if err := s.Delete("admin"); err != ErrLastOwner {
		t.Fatalf("delete last owner: %v", err)
	}
	if err := s.Update("admin", func(u *User) { u.Role = RoleViewer }); err != ErrLastOwner {
		t.Fatalf("demote last owner: %v", err)
	}
	if err := s.Update("admin", func(u *User) { u.Disabled = true }); err != ErrLastOwner {
		t.Fatalf("disable last owner: %v", err)
	}
	if u, _ := s.Get("admin"); u.Role != RoleOwner || u.Disabled {
		t.Fatalf("refused change was kept: %+v", u)
	}
	if err := s.Update("bob", func(u *User) { u.Role = RoleOwner; u.Login = "evil" }); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Get("evil"); err != ErrNotFound {
		t.Fatal("Update renamed a user")
	}
	if err := s.Delete("admin"); err != nil {
		t.Fatalf("second owner exists: %v", err)
	}
	if err := s.Delete("nobody"); err != ErrNotFound {
		t.Fatal(err)
	}
}

func TestOpenBroken(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "users.json"), []byte("{"), 0o600)
	if _, err := Open(dir); err == nil {
		t.Fatal("broken file accepted")
	}
}

func TestStoreSeesOtherWriter(t *testing.T) {
	dir := t.TempDir()
	a, _ := Open(dir)
	a.Add(User{Login: "admin", Role: RoleOwner, Hash: "old"})
	b, _ := Open(dir) // e.g. loft-panel passwd in another process
	b.Update("admin", func(u *User) { u.Hash = "new-and-longer" })
	b.Add(User{Login: "carol", Role: RoleViewer})
	if u, _ := a.Get("admin"); u.Hash != "new-and-longer" {
		t.Fatalf("running store kept old hash %q", u.Hash)
	}
	if _, err := a.Get("carol"); err != nil {
		t.Fatal("running store does not see the new user")
	}
}

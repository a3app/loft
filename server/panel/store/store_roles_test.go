package store

import (
	"testing"
	"time"
)

func TestRolesAndTokens(t *testing.T) {
	dir := t.TempDir()
	s, _ := Open(dir)
	if s.Perm(RoleOwner, "users") != Change || s.Perm(RoleViewer, "users") != None || s.Perm(RoleOperator, "members") != Change {
		t.Fatal("built-in perms")
	}
	if _, err := s.SaveRole(Role{ID: RoleOwner}); err == nil {
		t.Fatal("owner role changed")
	}
	r, err := s.SaveRole(Role{Name: "support", Perms: map[string]int{"members": View, "main": View}})
	if err != nil || r.ID == "" {
		t.Fatal(r, err)
	}
	if _, err := s.SaveRole(Role{Name: "Support"}); err == nil {
		t.Fatal("duplicate name")
	}
	s.Add(User{Login: "admin", Role: RoleOwner, Hash: "x"})
	s.Add(User{Login: "sup", Role: r.ID, Hash: "x", TokenHash: TokenHash("lpt_abc")})
	if err := s.Add(User{Login: "bad", Role: "nope"}); err == nil {
		t.Fatal("unknown role accepted")
	}
	s2, _ := Open(dir) // reload from disk
	if s2.Perm(r.ID, "members") != View || s2.Perm(r.ID, "network") != None {
		t.Fatal("custom role not kept")
	}
	if u, ok := s2.ByToken("lpt_abc"); !ok || u.Login != "sup" {
		t.Fatal("token lookup")
	}
	if err := s2.DeleteRole(r.ID); err == nil {
		t.Fatal("deleted a role in use")
	}
	s2.Update("sup", func(u *User) {
		u.Role = RoleViewer
		u.InviteHash, u.InviteUntil = TokenHash("inv"), time.Now().Add(time.Hour)
	})
	if _, ok := s2.ByInvite("inv"); !ok {
		t.Fatal("invite lookup")
	}
	if err := s2.DeleteRole(r.ID); err != nil {
		t.Fatal(err)
	}
	a := OpenAudit(dir)
	a.Add(Entry{User: "admin", Op: "POST /api/x"})
	a.Add(Entry{User: "sup", Op: "POST /api/y"})
	if l := a.List("", 10); len(l) != 2 || l[0].User != "sup" {
		t.Fatalf("audit %+v", l)
	}
	if l := a.List("admin", 10); len(l) != 1 {
		t.Fatal("audit filter")
	}
}

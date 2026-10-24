package auth

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestHashVerify(t *testing.T) {
	h, err := Hash("correct horse battery")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(h, "$argon2id$v=19$m=65536,t=3,p=2$") {
		t.Fatalf("unexpected PHC %q", h)
	}
	if !Verify(h, "correct horse battery") {
		t.Fatal("right password rejected")
	}
	if Verify(h, "correct horse batterY") {
		t.Fatal("wrong password accepted")
	}
	h2, _ := Hash("correct horse battery")
	if h == h2 {
		t.Fatal("salt not random")
	}
}

func TestHashShort(t *testing.T) {
	if _, err := Hash("eightchr"); err != ErrShort {
		t.Fatalf("want ErrShort, got %v", err)
	}
	if _, err := Hash("пароль123"); err != nil { // 9 runes, more bytes
		t.Fatalf("9 runes rejected: %v", err)
	}
	if _, err := Hash("пароль12"); err != ErrShort { // 8 runes, 14 bytes
		t.Fatalf("8 runes accepted: %v", err)
	}
}

func TestVerifyGarbage(t *testing.T) {
	for _, s := range []string{"", "$argon2i$v=19$m=1,t=1,p=1$AA$AA", "$argon2id$v=19$m=0,t=1,p=1$AA$AA", "$argon2id$v=19$m=8,t=1,p=1$!!$AA", "plain"} {
		if Verify(s, "x") {
			t.Fatalf("accepted %q", s)
		}
	}
}

func TestSessions(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	s := NewSessions()
	s.now = func() time.Time { return now }
	a := s.Create("admin", "198.51.100.20", "test", false)
	if len(a.ID) != 64 || len(a.CSRF) != 48 {
		t.Fatalf("token sizes %d %d", len(a.ID), len(a.CSRF))
	}
	if g := s.Get(a.ID); g == nil || g.User != "admin" || g.CSRF != a.CSRF {
		t.Fatal("session not found")
	}
	now = now.Add(29 * time.Minute)
	if s.Get(a.ID) == nil {
		t.Fatal("expired before idle limit")
	}
	now = now.Add(31 * time.Minute)
	if s.Get(a.ID) != nil {
		t.Fatal("idle session still alive")
	}
	b := s.Create("bob", "", "", false)
	c := s.Create("bob", "", "", false)
	s.DeleteUser("bob")
	if s.Get(b.ID) != nil || s.Get(c.ID) != nil {
		t.Fatal("DeleteUser left sessions")
	}
	if s.Get("") != nil || s.Get("nope") != nil {
		t.Fatal("unknown id accepted")
	}
}

func TestSessionsMax(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	s := NewSessions()
	s.now = func() time.Time { return now }
	a := s.Create("admin", "", "", false)
	for i := 0; i < 7*24*4+1; i++ { // keep it busy every 15 min past 7 days
		now = now.Add(15 * time.Minute)
		s.Get(a.ID)
	}
	if s.Get(a.ID) != nil {
		t.Fatal("session outlived Max")
	}
}

func TestSessionsRemember(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	s := NewSessions()
	s.now = func() time.Time { return now }
	a := s.Create("admin", "", "", true)
	now = now.Add(31 * time.Minute)
	if s.Get(a.ID) == nil {
		t.Fatal("remembered session died on idle")
	}
	now = now.Add(7 * 24 * time.Hour)
	if s.Get(a.ID) != nil {
		t.Fatal("remembered session outlived Max")
	}
}

func TestLimiter(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	l := NewLimiter()
	l.now = func() time.Time { return now }
	for i := 0; i < 4; i++ {
		if l.Fail("192.0.2.1") {
			t.Fatalf("blocked after %d", i+1)
		}
		now = now.Add(time.Minute)
	}
	if b, _ := l.Blocked("192.0.2.1"); b {
		t.Fatal("blocked below limit")
	}
	if !l.Fail("192.0.2.1") {
		t.Fatal("5th failure did not block")
	}
	if b, until := l.Blocked("192.0.2.1"); !b || until.Sub(now) != 24*time.Hour {
		t.Fatalf("block %v %v", b, until)
	}
	if b, _ := l.Blocked("192.0.2.2"); b {
		t.Fatal("other address blocked")
	}
	now = now.Add(24 * time.Hour)
	if b, _ := l.Blocked("192.0.2.1"); b {
		t.Fatal("block did not expire")
	}
}

func TestLimiterWindow(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	l := NewLimiter()
	l.now = func() time.Time { return now }
	for i := 0; i < 20; i++ { // one failure every 3 minutes never reaches 5 in 10
		if l.Fail("192.0.2.1") {
			t.Fatalf("blocked at %d", i)
		}
		now = now.Add(3 * time.Minute)
	}
	l.Fail("192.0.2.3")
	l.OK("192.0.2.3")
	if len(l.m) != 1 {
		t.Fatal("OK did not clear")
	}
}

func TestSlow(t *testing.T) {
	now := time.Unix(1_800_000_000, 0)
	s := NewSlow()
	s.now = func() time.Time { return now }
	for i := 1; i <= 40; i++ {
		d := s.Fail("login:admin")
		want := time.Duration(i) * time.Second
		if want > 30*time.Second {
			want = 30 * time.Second
		}
		if d != want {
			t.Fatalf("fail %d: delay %v, want %v", i, d, want)
		}
	}
	now = now.Add(16 * time.Minute)
	if d := s.Fail("login:admin"); d != time.Second {
		t.Fatalf("window did not forget: %v", d)
	}
	s.OK("login:admin")
	if d := s.Fail("login:admin"); d != time.Second {
		t.Fatalf("OK did not reset: %v", d)
	}
}

func TestTablesBounded(t *testing.T) {
	l := NewLimiter()
	s := NewSlow()
	for i := 0; i < maxKeys+500; i++ {
		k := "k" + time.Duration(i).String()
		l.Fail(k)
		s.Fail(k)
	}
	if len(l.m) > maxKeys || len(s.m) > maxKeys {
		t.Fatalf("tables grew: %d %d", len(l.m), len(s.m))
	}
}

func TestSessionsPersist(t *testing.T) {
	p := filepath.Join(t.TempDir(), "sessions.json")
	a := NewSessions()
	a.Persist(p)
	ss := a.Create("admin", "198.51.100.1", "test", false)
	b, _ := os.ReadFile(p)
	if strings.Contains(string(b), ss.ID) {
		t.Fatal("cookie stored in the file")
	}
	b2 := NewSessions()
	if err := b2.Persist(p); err != nil {
		t.Fatal(err)
	}
	got := b2.Get(ss.ID)
	if got == nil || got.User != "admin" || got.CSRF != ss.CSRF || got.ID != ss.ID {
		t.Fatalf("after restart: %+v", got)
	}
	b2.Delete(ss.ID)
	b3 := NewSessions()
	b3.Persist(p)
	if b3.Get(ss.ID) != nil {
		t.Fatal("deleted session came back")
	}
}

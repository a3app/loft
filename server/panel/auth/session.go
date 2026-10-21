package auth

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Session is one signed-in browser. The cookie carries ID; CSRF is sent back
// in a header on every state-changing request.
type Session struct {
	ID      string `json:"-"`
	CSRF    string
	User    string
	IP      string
	Agent   string
	Created time.Time
	Seen    time.Time
}

// Sessions keeps sessions in memory and, after Persist, in a file, so a
// panel restart (an update, a package job) does not sign everyone out.
// Idle ends a session without requests; Max ends it regardless.
type Sessions struct {
	Idle  time.Duration
	Max   time.Duration
	now   func() time.Time
	mu    sync.Mutex
	m     map[string]*Session // key: sha256(ID), so neither memory nor the file holds cookies
	path  string
	saved time.Time
}

// Persist loads the sessions saved in path and keeps the file (0600) in step
// from now on. The file holds sha256 of each cookie, not the cookie.
func (s *Sessions) Persist(path string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.path = path
	b, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		return err
	}
	var m map[string]*Session
	if err := json.Unmarshal(b, &m); err != nil {
		return nil // a damaged file only signs people out
	}
	t := s.now()
	for k, ss := range m {
		if ss != nil && len(k) == 64 && t.Sub(ss.Seen) <= s.Idle && t.Sub(ss.Created) <= s.Max {
			s.m[k] = ss
		}
	}
	return nil
}

// SetLimits changes Idle and Max for every session from now on.
func (s *Sessions) SetLimits(idle, max time.Duration) {
	s.mu.Lock()
	s.Idle, s.Max = idle, max
	s.mu.Unlock()
}

// Limits returns Idle and Max.
func (s *Sessions) Limits() (idle, max time.Duration) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.Idle, s.Max
}

// saveLocked writes the file; force=false writes at most once a minute
// (only Seen changed).
func (s *Sessions) saveLocked(force bool) {
	if s.path == "" || (!force && s.now().Sub(s.saved) < time.Minute) {
		return
	}
	s.saved = s.now()
	b, err := json.Marshal(s.m)
	if err != nil {
		return
	}
	tmp := filepath.Join(filepath.Dir(s.path), ".sessions.tmp")
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return
	}
	os.Rename(tmp, s.path)
}

// NewSessions returns a store with 30 min idle and 7 day limits.
func NewSessions() *Sessions {
	return &Sessions{Idle: 30 * time.Minute, Max: 7 * 24 * time.Hour, now: time.Now, m: map[string]*Session{}}
}

// Create starts a session for user.
func (s *Sessions) Create(user, ip, agent string) *Session {
	t := s.now()
	id := token(32)
	ss := &Session{CSRF: token(24), User: user, IP: ip, Agent: agent, Created: t, Seen: t}
	s.mu.Lock()
	s.m[key(id)] = ss
	s.saveLocked(true)
	s.mu.Unlock()
	c := *ss
	c.ID = id
	return &c
}

// Get returns a live session and marks it seen; nil when unknown or expired.
func (s *Sessions) Get(id string) *Session {
	if id == "" {
		return nil
	}
	t := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	ss := s.m[key(id)]
	if ss == nil {
		return nil
	}
	if t.Sub(ss.Seen) > s.Idle || t.Sub(ss.Created) > s.Max {
		delete(s.m, key(id))
		s.saveLocked(true)
		return nil
	}
	ss.Seen = t
	s.saveLocked(false)
	c := *ss
	c.ID = id
	return &c
}

// Delete ends one session.
func (s *Sessions) Delete(id string) {
	s.mu.Lock()
	delete(s.m, key(id))
	s.saveLocked(true)
	s.mu.Unlock()
}

// DeleteUser ends every session of user (password change, user removed).
func (s *Sessions) DeleteUser(user string) {
	s.mu.Lock()
	for k, ss := range s.m {
		if ss.User == user {
			delete(s.m, k)
		}
	}
	s.saveLocked(true)
	s.mu.Unlock()
}

// List returns live sessions, oldest first is not guaranteed.
func (s *Sessions) List() []Session {
	t := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]Session, 0, len(s.m))
	for k, ss := range s.m {
		if t.Sub(ss.Seen) > s.Idle || t.Sub(ss.Created) > s.Max {
			delete(s.m, k)
			continue
		}
		c := *ss
		c.ID, c.CSRF = "", ""
		out = append(out, c)
	}
	return out
}

// Keyed is a session with the key it is stored under (sha256 of the
// cookie: enough to end it, useless to sign in with).
type Keyed struct {
	Key string
	Session
}

// ListKeyed is List with keys.
func (s *Sessions) ListKeyed() []Keyed {
	t := s.now()
	s.mu.Lock()
	defer s.mu.Unlock()
	out := []Keyed{}
	for k, ss := range s.m {
		if t.Sub(ss.Seen) > s.Idle || t.Sub(ss.Created) > s.Max {
			continue
		}
		c := *ss
		c.ID, c.CSRF = "", ""
		out = append(out, Keyed{Key: k, Session: c})
	}
	return out
}

// DeleteKey ends the session stored under k.
func (s *Sessions) DeleteKey(k string) {
	s.mu.Lock()
	delete(s.m, k)
	s.saveLocked(true)
	s.mu.Unlock()
}

// Key is the store key of a cookie value.
func Key(id string) string { return key(id) }

func key(id string) string {
	h := sha256.Sum256([]byte(id))
	return hex.EncodeToString(h[:])
}

// token is n random bytes as hex.
func token(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		panic("auth: no randomness: " + err.Error())
	}
	return hex.EncodeToString(b)
}

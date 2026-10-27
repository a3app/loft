// Package store keeps panel users in one JSON file, written atomically.
// The panel has few users and rare writes; a file is enough and is easy to
// back up, read and repair by hand.
package store

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"sync"
	"time"
)

// Roles a user can have.
const (
	RoleOwner    = "owner"
	RoleOperator = "operator"
	RoleViewer   = "viewer"
)

// User is one panel account. Hash is an auth.Hash PHC string.
type User struct {
	Login    string    `json:"login"`
	Name     string    `json:"name,omitempty"`
	Role     string    `json:"role"`
	Hash     string    `json:"hash"`
	Disabled bool      `json:"disabled,omitempty"`
	Created  time.Time `json:"created"`
	Seen     time.Time `json:"seen,omitempty"`
	// One API token: its SHA-256, the last characters to recognise it.
	TokenHash   string    `json:"token_hash,omitempty"`
	TokenTail   string    `json:"token_tail,omitempty"`
	TokenIssued time.Time `json:"token_issued,omitempty"`
	TokenUsed   time.Time `json:"token_used,omitempty"`
	// An invitation: the user sets a password through a link until InviteUntil.
	InviteHash  string    `json:"invite_hash,omitempty"`
	InviteUntil time.Time `json:"invite_until,omitempty"`
}

// Errors callers match on.
var (
	ErrNotFound  = errors.New("user not found")
	ErrExists    = errors.New("user exists")
	ErrLastOwner = errors.New("the last owner cannot be removed or demoted")
)

var loginRe = regexp.MustCompile(`^[a-z][a-z0-9._-]{0,31}$`)

// ValidLogin is lower-case latin, digits, . _ -, starts with a letter, ≤ 32.
func ValidLogin(s string) bool { return loginRe.MatchString(s) }

// builtinRole reports whether r is the ID of a built-in role.
func builtinRole(r string) bool { return r == RoleOwner || r == RoleOperator || r == RoleViewer }

// validRoleLocked reports a built-in or saved role.
func (s *Store) validRoleLocked(r string) bool {
	_, ok := s.roles[r]
	return builtinRole(r) || ok
}

// file is the on-disk shape of users.json.
type file struct {
	Version int    `json:"version"`
	Users   []User `json:"users"`
	Roles   []Role `json:"roles,omitempty"`
}

// Store is users.json under a data directory.
type Store struct {
	path  string
	mu    sync.RWMutex
	u     map[string]User
	roles map[string]Role
	seen  fileStamp // users.json as last read or written
}

// fileStamp tells whether another process (loft-panel passwd) rewrote the file.
type fileStamp struct {
	mod  time.Time
	size int64
}

// stamp reads the file's modification time and size (zero when missing).
func stamp(path string) fileStamp {
	st, err := os.Stat(path)
	if err != nil {
		return fileStamp{}
	}
	return fileStamp{st.ModTime(), st.Size()}
}

// Open reads dir/users.json; a missing file is an empty store.
func Open(dir string) (*Store, error) {
	s := &Store{path: filepath.Join(dir, "users.json"), u: map[string]User{}, roles: map[string]Role{}}
	if err := s.load(); err != nil {
		return nil, err
	}
	return s, nil
}

// load replaces memory with the file; a missing file is empty.
func (s *Store) load() error {
	st := stamp(s.path)
	b, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		s.u, s.roles, s.seen = map[string]User{}, map[string]Role{}, fileStamp{}
		return nil
	}
	if err != nil {
		return err
	}
	var f file
	if err := json.Unmarshal(b, &f); err != nil {
		return fmt.Errorf("%s: %w", s.path, err)
	}
	m := map[string]User{}
	for _, u := range f.Users {
		m[u.Login] = u
	}
	roles := map[string]Role{}
	for _, r := range f.Roles {
		roles[r.ID] = r
	}
	s.u, s.roles, s.seen = m, roles, st
	return nil
}

// fresh re-reads users.json when someone else changed it. A broken file keeps
// the last good copy in memory (and the next write repairs the file).
func (s *Store) fresh() {
	s.mu.RLock()
	same := stamp(s.path) == s.seen
	s.mu.RUnlock()
	if same {
		return
	}
	s.mu.Lock()
	if stamp(s.path) != s.seen {
		s.load()
	}
	s.mu.Unlock()
}

// Empty reports no users yet (first run: the wizard creates the owner).
func (s *Store) Empty() bool {
	s.fresh()
	s.mu.RLock()
	defer s.mu.RUnlock()
	return len(s.u) == 0
}

// Get returns one user.
func (s *Store) Get(login string) (User, error) {
	s.fresh()
	s.mu.RLock()
	defer s.mu.RUnlock()
	u, ok := s.u[login]
	if !ok {
		return User{}, ErrNotFound
	}
	return u, nil
}

// List returns users sorted by login.
func (s *Store) List() []User {
	s.fresh()
	s.mu.RLock()
	out := make([]User, 0, len(s.u))
	for _, u := range s.u {
		out = append(out, u)
	}
	s.mu.RUnlock()
	sort.Slice(out, func(i, j int) bool { return out[i].Login < out[j].Login })
	return out
}

// Add creates a user.
func (s *Store) Add(u User) error {
	s.fresh()
	if !ValidLogin(u.Login) {
		return fmt.Errorf("login %q: lower-case latin letters, digits, . _ -", u.Login)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.validRoleLocked(u.Role) {
		return fmt.Errorf("role %q: unknown", u.Role)
	}
	if _, ok := s.u[u.Login]; ok {
		return ErrExists
	}
	if u.Created.IsZero() {
		u.Created = time.Now().UTC()
	}
	s.u[u.Login] = u
	return s.saveLocked(func() { delete(s.u, u.Login) })
}

// Update changes one user with fn. It refuses to leave no enabled owner.
func (s *Store) Update(login string, fn func(*User)) error {
	s.fresh()
	s.mu.Lock()
	defer s.mu.Unlock()
	old, ok := s.u[login]
	if !ok {
		return ErrNotFound
	}
	u := old
	fn(&u)
	u.Login = old.Login
	if !s.validRoleLocked(u.Role) {
		return fmt.Errorf("role %q: unknown", u.Role)
	}
	s.u[login] = u
	if s.ownersLocked() == 0 {
		s.u[login] = old
		return ErrLastOwner
	}
	return s.saveLocked(func() { s.u[login] = old })
}

// Delete removes a user, but never the last enabled owner.
func (s *Store) Delete(login string) error {
	s.fresh()
	s.mu.Lock()
	defer s.mu.Unlock()
	old, ok := s.u[login]
	if !ok {
		return ErrNotFound
	}
	delete(s.u, login)
	if s.ownersLocked() == 0 {
		s.u[login] = old
		return ErrLastOwner
	}
	return s.saveLocked(func() { s.u[login] = old })
}

// ownersLocked counts enabled owners. Update and Delete use it to refuse a
// change that would leave the panel without one. The caller holds s.mu.
func (s *Store) ownersLocked() int {
	n := 0
	for _, u := range s.u {
		if u.Role == RoleOwner && !u.Disabled {
			n++
		}
	}
	return n
}

// saveLocked writes users.json via temp file + rename, mode 0600.
// On failure undo restores memory so it matches the disk.
func (s *Store) saveLocked(undo func()) error {
	f := file{Version: 2}
	for _, u := range s.u {
		f.Users = append(f.Users, u)
	}
	for _, r := range s.roles {
		f.Roles = append(f.Roles, r)
	}
	sort.Slice(f.Roles, func(i, j int) bool { return f.Roles[i].ID < f.Roles[j].ID })
	sort.Slice(f.Users, func(i, j int) bool { return f.Users[i].Login < f.Users[j].Login })
	b, err := json.MarshalIndent(f, "", "  ")
	if err != nil {
		undo()
		return err
	}
	if err := WriteAtomic(s.path, append(b, '\n'), 0o600); err != nil {
		undo()
		return err
	}
	s.seen = stamp(s.path)
	return nil
}

// WriteAtomic writes data to path through a temp file in the same directory,
// fsyncs it, and renames it over path.
func WriteAtomic(path string, data []byte, mode os.FileMode) error {
	dir := filepath.Dir(path)
	tmp, err := os.CreateTemp(dir, "."+filepath.Base(path)+".*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	ok := false
	defer func() {
		if !ok {
			os.Remove(name)
		}
	}()
	if err := tmp.Chmod(mode); err != nil {
		tmp.Close()
		return err
	}
	if _, err := tmp.Write(data); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Sync(); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(name, path); err != nil {
		return err
	}
	ok = true
	if d, err := os.Open(dir); err == nil {
		d.Sync()
		d.Close()
	}
	return nil
}

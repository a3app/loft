package store

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"regexp"
	"sort"
	"strings"
	"time"
)

// Areas are the panel's sections; a role gives each one a level.
var Areas = []string{"main", "node", "members", "segments", "network", "domains", "mail", "packages", "users", "system", "settings"}

// Levels of access to an area.
const (
	None   = 0
	View   = 1
	Change = 2
)

// Role names what each area allows. The owner role has everything and
// cannot be changed; operator and viewer are built in and editable.
type Role struct {
	ID      string         `json:"id"`
	Name    string         `json:"name"`
	Perms   map[string]int `json:"perms"`
	Builtin bool           `json:"builtin,omitempty"`
}

func builtinRoles() []Role {
	all := func(v int) map[string]int {
		m := map[string]int{}
		for _, a := range Areas {
			m[a] = v
		}
		return m
	}
	op := all(Change)
	op["node"], op["packages"], op["users"], op["system"], op["settings"] = View, View, None, View, None
	vw := all(View)
	vw["users"] = None
	return []Role{
		{ID: RoleOwner, Name: "owner", Perms: all(Change), Builtin: true},
		{ID: RoleOperator, Name: "operator", Perms: op, Builtin: true},
		{ID: RoleViewer, Name: "viewer", Perms: vw, Builtin: true},
	}
}

var roleIDRe = regexp.MustCompile(`^[a-z][a-z0-9-]{0,31}$`)

// Roles lists the built-in roles first, then the others by name.
func (s *Store) Roles() []Role {
	s.fresh()
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.rolesLocked()
}

func (s *Store) rolesLocked() []Role {
	out := builtinRoles()
	for i := range out {
		if r, ok := s.roles[out[i].ID]; ok && out[i].ID != RoleOwner {
			out[i].Perms = r.Perms
		}
	}
	var custom []Role
	for _, r := range s.roles {
		if !r.Builtin {
			custom = append(custom, r)
		}
	}
	sort.Slice(custom, func(i, j int) bool { return custom[i].Name < custom[j].Name })
	return append(out, custom...)
}

// RoleOf returns the role with id.
func (s *Store) RoleOf(id string) (Role, bool) {
	for _, r := range s.Roles() {
		if r.ID == id {
			return r, true
		}
	}
	return Role{}, false
}

// Perm is the level role id gives on area.
func (s *Store) Perm(id, area string) int {
	r, ok := s.RoleOf(id)
	if !ok {
		return None
	}
	return r.Perms[area]
}

// SaveRole adds or changes a role. ID empty adds one named Name.
func (s *Store) SaveRole(r Role) (Role, error) {
	s.fresh()
	r.Name = strings.TrimSpace(r.Name)
	if r.ID == RoleOwner {
		return Role{}, errors.New("the owner role cannot be changed")
	}
	perms := map[string]int{}
	for _, a := range Areas {
		v := r.Perms[a]
		if v < None || v > Change {
			return Role{}, fmt.Errorf("area %s: level %d", a, v)
		}
		perms[a] = v
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.roles == nil {
		s.roles = map[string]Role{}
	}
	if r.ID == "" {
		if r.Name == "" || len(r.Name) > 40 {
			return Role{}, errors.New("role name: 1–40 characters")
		}
		h := sha256.Sum256([]byte(r.Name + time.Now().String()))
		r.ID = "r-" + hex.EncodeToString(h[:4])
	}
	builtin := r.ID == RoleOperator || r.ID == RoleViewer
	old, had := s.roles[r.ID]
	if !builtin && !had {
		if !roleIDRe.MatchString(r.ID) {
			return Role{}, fmt.Errorf("role %q: unknown", r.ID)
		}
		for _, x := range s.rolesLocked() {
			if strings.EqualFold(x.Name, r.Name) {
				return Role{}, fmt.Errorf("a role named %q exists", r.Name)
			}
		}
	}
	if builtin {
		for _, b := range builtinRoles() {
			if b.ID == r.ID {
				r.Name = b.Name
			}
		}
	} else if r.Name == "" {
		r.Name = old.Name
	}
	r.Perms, r.Builtin = perms, builtin
	s.roles[r.ID] = r
	if err := s.saveLocked(func() {
		if had {
			s.roles[r.ID] = old
		} else {
			delete(s.roles, r.ID)
		}
	}); err != nil {
		return Role{}, err
	}
	return r, nil
}

// DeleteRole removes a custom role nobody has.
func (s *Store) DeleteRole(id string) error {
	s.fresh()
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.roles[id]
	if !ok || r.Builtin || id == RoleOwner || id == RoleOperator || id == RoleViewer {
		return errors.New("built-in roles stay")
	}
	for _, u := range s.u {
		if u.Role == id {
			return fmt.Errorf("%s has this role: give them another one first", u.Login)
		}
	}
	delete(s.roles, id)
	return s.saveLocked(func() { s.roles[id] = r })
}

// TokenHash is how an API token is kept.
func TokenHash(tok string) string {
	h := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(h[:])
}

// ByToken finds the user whose API token is tok.
func (s *Store) ByToken(tok string) (User, bool) {
	if !strings.HasPrefix(tok, "lpt_") {
		return User{}, false
	}
	h := TokenHash(tok)
	s.fresh()
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, u := range s.u {
		if u.TokenHash != "" && u.TokenHash == h {
			return u, true
		}
	}
	return User{}, false
}

// ByInvite finds the user an unexpired invitation was made for.
func (s *Store) ByInvite(tok string) (User, bool) {
	h := TokenHash(tok)
	s.fresh()
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, u := range s.u {
		if u.InviteHash != "" && u.InviteHash == h && time.Now().Before(u.InviteUntil) {
			return u, true
		}
	}
	return User{}, false
}

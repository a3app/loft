package web

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/a3app/loft/server/panel/auth"
	"github.com/a3app/loft/server/panel/store"
)

// Users of the panel: accounts, roles, API tokens, invitations, sessions
// and the log of changes.

type userJSON struct {
	Login       string    `json:"login"`
	Name        string    `json:"name,omitempty"`
	Role        string    `json:"role"`
	Disabled    bool      `json:"disabled,omitempty"`
	Seen        time.Time `json:"seen,omitzero"`
	TokenTail   string    `json:"token_tail,omitempty"`
	TokenIssued time.Time `json:"token_issued,omitzero"`
	TokenUsed   time.Time `json:"token_used,omitzero"`
	Invited     bool      `json:"invited,omitempty"` // waits for the user to set a password
	InviteUntil time.Time `json:"invite_until,omitzero"`
}

func userView(u store.User) userJSON {
	return userJSON{Login: u.Login, Name: u.Name, Role: u.Role, Disabled: u.Disabled, Seen: u.Seen, TokenTail: u.TokenTail,
		TokenIssued: u.TokenIssued, TokenUsed: u.TokenUsed, Invited: u.Hash == "", InviteUntil: u.InviteUntil}
}

func (s *Server) usersList(w http.ResponseWriter, r *http.Request) {
	out := []userJSON{}
	for _, u := range s.Store.List() {
		out = append(out, userView(u))
	}
	ok(w, map[string]any{"users": out, "roles": s.Store.Roles(), "areas": store.Areas})
}

func token(prefix string, n int) string {
	b := make([]byte, n)
	rand.Read(b)
	return prefix + hex.EncodeToString(b)
}

// usersSave adds a user (with a password or an invitation) or changes one.
func (s *Server) usersSave(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Login    string `json:"login"`
		Name     string `json:"name"`
		Role     string `json:"role"`
		Password string `json:"password"`
		Disabled bool   `json:"disabled"`
		New      bool   `json:"new"`
		Invite   bool   `json:"invite"`
	}
	if !readJSON(w, r, 8<<10, &in) {
		return
	}
	in.Login, in.Name = strings.TrimSpace(in.Login), strings.Join(strings.Fields(in.Name), " ")
	auditTarget(r, in.Login)
	if _, ok := s.Store.RoleOf(in.Role); !ok {
		fail(w, http.StatusBadRequest, "unknown role")
		return
	}
	var hash string
	if in.Password != "" {
		if len([]rune(in.Password)) < auth.MinPassword {
			fail(w, http.StatusBadRequest, "password: at least "+strconv.Itoa(auth.MinPassword)+" characters")
			return
		}
		var err error
		if hash, err = auth.Hash(in.Password); err != nil {
			fail(w, http.StatusInternalServerError, err.Error())
			return
		}
	}
	link := ""
	if in.New {
		u := store.User{Login: in.Login, Name: in.Name, Role: in.Role, Hash: hash}
		if hash == "" {
			if !in.Invite {
				fail(w, http.StatusBadRequest, "set a password or make an invitation")
				return
			}
			tok := token("", 24)
			u.InviteHash, u.InviteUntil = store.TokenHash(tok), time.Now().Add(24*time.Hour).UTC()
			link = "#invite/" + tok
		}
		if err := s.Store.Add(u); err != nil {
			if errors.Is(err, store.ErrExists) {
				fail(w, http.StatusConflict, "user "+in.Login+" exists")
				return
			}
			fail(w, http.StatusBadRequest, err.Error())
			return
		}
	} else {
		if in.Login == userOf(r).Login && in.Disabled {
			fail(w, http.StatusBadRequest, "you cannot switch off your own sign-in")
			return
		}
		err := s.Store.Update(in.Login, func(u *store.User) {
			u.Name, u.Role, u.Disabled = in.Name, in.Role, in.Disabled
			if hash != "" {
				u.Hash, u.InviteHash, u.InviteUntil = hash, "", time.Time{}
			}
		})
		if err != nil {
			s.userErr(w, err)
			return
		}
		if hash != "" || in.Disabled {
			s.endSessionsOf(in.Login, r)
		}
	}
	u, _ := s.Store.Get(in.Login)
	ok(w, map[string]any{"user": userView(u), "invite": link})
}

func (s *Server) userErr(w http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, store.ErrNotFound):
		fail(w, http.StatusNotFound, "no such user")
	case errors.Is(err, store.ErrLastOwner):
		fail(w, http.StatusConflict, err.Error())
	default:
		fail(w, http.StatusBadRequest, err.Error())
	}
}

// endSessionsOf signs a user out everywhere, except the caller's own session.
func (s *Server) endSessionsOf(login string, r *http.Request) {
	mine := ""
	if c, err := r.Cookie(cookieName); err == nil {
		mine = auth.Key(c.Value)
	}
	for _, k := range s.Sessions.ListKeyed() {
		if k.User == login && k.Key != mine {
			s.Sessions.DeleteKey(k.Key)
		}
	}
}

func (s *Server) usersDelete(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Login string `json:"login"`
	}
	if !readJSON(w, r, 1<<10, &in) {
		return
	}
	auditTarget(r, in.Login)
	if in.Login == userOf(r).Login {
		fail(w, http.StatusBadRequest, "you cannot delete yourself")
		return
	}
	if err := s.Store.Delete(in.Login); err != nil {
		s.userErr(w, err)
		return
	}
	s.Sessions.DeleteUser(in.Login)
	ok(w, struct{}{})
}

// usersToken issues (or reissues) a user's API token, shown once, or revokes it.
func (s *Server) usersToken(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Login  string `json:"login"`
		Revoke bool   `json:"revoke"`
	}
	if !readJSON(w, r, 1<<10, &in) {
		return
	}
	auditTarget(r, in.Login)
	tok := ""
	err := s.Store.Update(in.Login, func(u *store.User) {
		if in.Revoke {
			u.TokenHash, u.TokenTail, u.TokenIssued, u.TokenUsed = "", "", time.Time{}, time.Time{}
			return
		}
		tok = token("lpt_", 24)
		u.TokenHash, u.TokenTail, u.TokenIssued, u.TokenUsed = store.TokenHash(tok), tok[len(tok)-4:], time.Now().UTC(), time.Time{}
	})
	if err != nil {
		s.userErr(w, err)
		return
	}
	ok(w, map[string]string{"token": tok})
}

func (s *Server) rolesSave(w http.ResponseWriter, r *http.Request) {
	var in store.Role
	if !readJSON(w, r, 8<<10, &in) {
		return
	}
	auditTarget(r, firstNonEmpty(in.Name, in.ID))
	ro, err := s.Store.SaveRole(in)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	ok(w, ro)
}

func (s *Server) rolesDelete(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID string `json:"id"`
	}
	if !readJSON(w, r, 1<<10, &in) {
		return
	}
	auditTarget(r, in.ID)
	if err := s.Store.DeleteRole(in.ID); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	ok(w, struct{}{})
}

type sessionJSON struct {
	Key     string    `json:"key"`
	User    string    `json:"user"`
	IP      string    `json:"ip"`
	Agent   string    `json:"agent"`
	Created time.Time `json:"created"`
	Seen    time.Time `json:"seen"`
	Me      bool      `json:"me,omitempty"`
}

func (s *Server) sessionsList(w http.ResponseWriter, r *http.Request) {
	mine := ""
	if c, err := r.Cookie(cookieName); err == nil {
		mine = auth.Key(c.Value)
	}
	out := []sessionJSON{}
	for _, k := range s.Sessions.ListKeyed() {
		out = append(out, sessionJSON{Key: k.Key, User: k.User, IP: k.IP, Agent: k.Agent, Created: k.Created, Seen: k.Seen, Me: k.Key == mine})
	}
	var toks []map[string]any
	for _, u := range s.Store.List() {
		if u.TokenHash != "" {
			toks = append(toks, map[string]any{"user": u.Login, "tail": u.TokenTail, "issued": u.TokenIssued, "used": u.TokenUsed})
		}
	}
	ok(w, map[string]any{"sessions": out, "tokens": toks})
}

func (s *Server) sessionsEnd(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Key string `json:"key"`
	}
	if !readJSON(w, r, 1<<10, &in) {
		return
	}
	for _, k := range s.Sessions.ListKeyed() {
		if k.Key == in.Key {
			auditTarget(r, k.User)
		}
	}
	s.Sessions.DeleteKey(in.Key)
	ok(w, struct{}{})
}

func (s *Server) auditList(w http.ResponseWriter, r *http.Request) {
	ok(w, s.Audit.List(r.URL.Query().Get("user"), 500))
}

// inviteGet tells the invitation page whom it is for.
func (s *Server) inviteGet(w http.ResponseWriter, r *http.Request) {
	u, found := s.Store.ByInvite(r.PathValue("token"))
	if !found || u.Hash != "" {
		fail(w, http.StatusNotFound, "the invitation is used or expired")
		return
	}
	ok(w, map[string]string{"login": u.Login, "name": u.Name})
}

// inviteAccept sets the password and signs the user in.
func (s *Server) inviteAccept(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Token    string `json:"token"`
		Password string `json:"password"`
	}
	if !readJSON(w, r, 4<<10, &in) {
		return
	}
	u, found := s.Store.ByInvite(in.Token)
	if !found || u.Hash != "" {
		fail(w, http.StatusNotFound, "the invitation is used or expired")
		return
	}
	if len([]rune(in.Password)) < auth.MinPassword {
		fail(w, http.StatusBadRequest, "password: at least "+strconv.Itoa(auth.MinPassword)+" characters")
		return
	}
	hash, err := auth.Hash(in.Password)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.Store.Update(u.Login, func(x *store.User) {
		x.Hash, x.InviteHash, x.InviteUntil, x.Seen = hash, "", time.Time{}, time.Now().UTC()
	}); err != nil {
		s.userErr(w, err)
		return
	}
	s.Audit.Add(store.Entry{User: u.Login, Op: "POST /api/invite", From: clientIP(r)})
	s.startSession(w, r, u.Login)
}

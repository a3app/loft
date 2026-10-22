package web

import (
	"net/http"
	"strconv"
	"time"

	"github.com/a3app/loft/server/panel/auth"
	"github.com/a3app/loft/server/panel/store"
)

// The signed-in user's own page (#me): profile and rights, password, API
// token and sessions. Every role may do this to itself, without the users
// area.

func (s *Server) myKey(r *http.Request) string {
	if c, err := r.Cookie(cookieName); err == nil {
		return auth.Key(c.Value)
	}
	return ""
}

func (s *Server) meGet(w http.ResponseWriter, r *http.Request) {
	u, _ := s.Store.Get(userOf(r).Login)
	mine := s.myKey(r)
	sess := []sessionJSON{}
	for _, k := range s.Sessions.ListKeyed() {
		if k.User == u.Login {
			sess = append(sess, sessionJSON{Key: k.Key, User: k.User, IP: k.IP, Agent: k.Agent, Created: k.Created, Seen: k.Seen, Me: k.Key == mine})
		}
	}
	out := map[string]any{"me": s.me(u, ""), "sessions": sess, "seen": u.Seen}
	if u.TokenHash != "" {
		out["token"] = map[string]any{"tail": u.TokenTail, "issued": u.TokenIssued, "used": u.TokenUsed}
	}
	ok(w, out)
}

// mePassword needs the current password; the other sessions end.
func (s *Server) mePassword(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Current  string `json:"current"`
		Password string `json:"password"`
	}
	if !readJSON(w, r, 4<<10, &in) {
		return
	}
	login := userOf(r).Login
	u, _ := s.Store.Get(login)
	if !auth.Verify(u.Hash, in.Current) {
		s.sleep(s.slow().Fail("me:" + login))
		fail(w, http.StatusBadRequest, "the current password is wrong")
		return
	}
	s.slow().OK("me:" + login)
	if len([]rune(in.Password)) < auth.MinPassword {
		fail(w, http.StatusBadRequest, "password: at least "+strconv.Itoa(auth.MinPassword)+" characters")
		return
	}
	h, err := auth.Hash(in.Password)
	if err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	if err := s.Store.Update(login, func(x *store.User) { x.Hash = h }); err != nil {
		s.userErr(w, err)
		return
	}
	s.endMine(login, s.myKey(r), "")
	ok(w, struct{}{})
}

func (s *Server) meToken(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Revoke bool `json:"revoke"`
	}
	if !readJSON(w, r, 1<<10, &in) {
		return
	}
	tok := ""
	err := s.Store.Update(userOf(r).Login, func(u *store.User) {
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

// meSessionsEnd ends one own session by key, or with "others" every own
// session but this one.
func (s *Server) meSessionsEnd(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Key    string `json:"key"`
		Others bool   `json:"others"`
	}
	if !readJSON(w, r, 1<<10, &in) {
		return
	}
	login, mine := userOf(r).Login, s.myKey(r)
	if !in.Others && in.Key == "" {
		fail(w, http.StatusBadRequest, "which session?")
		return
	}
	if in.Others {
		s.endMine(login, mine, "")
	} else {
		s.endMine(login, "", in.Key)
	}
	ok(w, struct{}{})
}

// endMine ends login's sessions: all but keep, or only the one keyed only.
func (s *Server) endMine(login, keep, only string) {
	for _, k := range s.Sessions.ListKeyed() {
		if k.User != login || k.Key == keep || (only != "" && k.Key != only) {
			continue
		}
		s.Sessions.DeleteKey(k.Key)
	}
}

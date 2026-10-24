// Package web is loft-panel's HTTP side: sign-in, the JSON API under /api/,
// and the embedded UI. It runs without privileges; anything that touches the
// node goes through loft-panel-adm.
package web

import (
	"context"
	"crypto/subtle"
	"embed"
	"encoding/json"
	"errors"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/a3app/loft/internal/netconf"
	"github.com/a3app/loft/internal/version"
	"github.com/a3app/loft/server/panel/admproto"
	"github.com/a3app/loft/server/panel/auth"
	"github.com/a3app/loft/server/panel/store"
)

//go:embed ui
var uiFS embed.FS

// Adm is the part of admproto.Client the panel uses; tests swap it.
type Adm interface {
	Call(ctx context.Context, op string, body, out any) error
}

// Server is one panel instance.
type Server struct {
	Store    *store.Store
	Sessions *auth.Sessions
	Limit    *auth.Limiter
	// Slow replaces Limit when the client address is unknown (see knownIP).
	Slow *auth.Slow
	Adm  func(by string) Adm
	// Hosts the panel answers to besides loopback; empty answers to any.
	Hosts   []string
	hostsMu sync.RWMutex
	// Settings is the wizard's state; ACME issues certificates.
	Settings *store.SettingsFile
	// Keys is the optional panel copy of member secrets (keep_member_keys).
	Keys *store.MemberKeys
	ACME *ACME
	jobs jobs
	pres presence
	dom  domains
	mail mailCache
	bk   backups
	// ReleasesAPI and HTTP are for following GitHub releases (tests).
	ReleasesAPI string
	HTTP        *http.Client
	// Audit is the log of changes (Users → Log).
	Audit *store.Audit
	// SendMail replaces SMTP (tests).
	SendMail func(n store.Notify, subject, body string) error
	// SetupCode must be sent with the first owner while the store is empty;
	// SetupFile holds it on disk and is removed once the owner exists.
	SetupCode string
	SetupFile string
	// UIDir serves the UI from disk instead of the embedded copy (development).
	UIDir string
	Log   *log.Logger
	// sleep is the failed-login delay; tests make it instant.
	sleep func(time.Duration)
}

const cookieName = "loft_panel"

// dummyHash is verified for unknown logins so timing does not reveal them.
var dummyHash, _ = auth.Hash("not a real password, only for timing")

var errKeyNotStored = errors.New("key is not stored")

// Handler returns the full HTTP handler.
func (s *Server) Handler() (http.Handler, error) {
	if s.sleep == nil {
		s.sleep = time.Sleep
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/state", s.state)
	mux.HandleFunc("POST /api/setup", s.setup)
	mux.HandleFunc("POST /api/login", s.login)
	mux.HandleFunc("POST /api/logout", s.need(anyone, s.logout))
	mux.HandleFunc("GET /api/network", s.need(area("members,segments", store.View), s.networkGet))
	mux.HandleFunc("POST /api/network/check", s.need(area("members,segments", store.View), s.networkCheck))
	mux.HandleFunc("POST /api/network/apply", s.need(area("members,segments", store.Change), s.networkApply))
	mux.HandleFunc("GET /api/members/key", s.need(area("members", store.Change), s.memberKeyGet))
	mux.HandleFunc("POST /api/members/key", s.need(area("members", store.Change), s.memberKey))
	mux.HandleFunc("GET /api/node", s.need(anyone, s.nodeStatus))
	mux.HandleFunc("POST /api/node/restart", s.need(area("node", store.Change), s.nodeRestart))
	mux.HandleFunc("GET /api/jobs/{id}", s.need(anyone, s.jobGet))
	mux.HandleFunc("GET /api/wizard", s.need(ownerOnly, s.wizard))
	mux.HandleFunc("POST /api/domain/check", s.need(ownerOnly, s.domainCheck))
	mux.HandleFunc("POST /api/domain/now", s.need(ownerOnly, s.dnsNow))
	mux.HandleFunc("POST /api/domain", s.need(ownerOnly, s.domainSet))
	mux.HandleFunc("POST /api/cert/issue", s.need(ownerOnly, s.certIssue))
	mux.HandleFunc("POST /api/wizard/member", s.need(area("members", store.Change), s.memberCreate))
	mux.HandleFunc("POST /api/wizard/done", s.need(ownerOnly, s.wizardDone))
	mux.HandleFunc("GET /api/packages", s.need(area("packages", store.View), s.packages))
	mux.HandleFunc("GET /api/packages/job", s.need(area("packages", store.View), s.packagesJob))
	mux.HandleFunc("POST /api/packages/apply", s.need(area("packages", store.Change), s.packagesApply))
	mux.HandleFunc("POST /api/packages/unit", s.need(area("packages", store.Change), s.packageUnit))
	mux.HandleFunc("GET /api/packages/mtproto", s.need(area("packages", store.View), s.mtprotoGet))
	mux.HandleFunc("POST /api/packages/mtproto", s.need(area("packages", store.Change), s.mtprotoSet))
	mux.HandleFunc("GET /api/update", s.need(area("packages", store.View), s.updateGet))
	mux.HandleFunc("POST /api/update/settings", s.need(area("packages", store.Change), s.updateSettings))
	mux.HandleFunc("POST /api/update/check", s.need(area("packages", store.Change), s.updateCheck))
	mux.HandleFunc("POST /api/update/apply", s.need(area("packages", store.Change), s.updateApply))
	mux.HandleFunc("GET /api/net", s.need(area("network", store.View), s.netState))
	mux.HandleFunc("POST /api/net/change", s.need(area("network", store.Change), s.netChange))
	mux.HandleFunc("POST /api/net/confirm", s.need(area("network", store.Change), s.netHeld(admproto.OpNetConfirm)))
	mux.HandleFunc("POST /api/net/revert", s.need(area("network", store.Change), s.netHeld(admproto.OpNetRevert)))
	mux.HandleFunc("GET /api/channels", s.need(area("network", store.View), s.channels))
	mux.HandleFunc("POST /api/channels/save", s.need(area("network", store.Change), channelOp[admproto.Channel](s, admproto.OpChannelSave)))
	mux.HandleFunc("POST /api/channels/delete", s.need(area("network", store.Change), channelOp[admproto.ChannelName](s, admproto.OpChannelDelete)))
	mux.HandleFunc("POST /api/channels/switch", s.need(area("network", store.Change), channelOp[admproto.ChannelSwitch](s, admproto.OpChannelSwitch)))
	mux.HandleFunc("POST /api/channels/rules", s.need(area("network", store.Change), channelOp[admproto.ExitRules](s, admproto.OpChannelRules)))
	mux.HandleFunc("POST /api/channels/prio", s.need(area("network", store.Change), channelOp[admproto.Prio](s, admproto.OpChannelPrio)))
	mux.HandleFunc("POST /api/channels/prio/delete", s.need(area("network", store.Change), channelOp[admproto.ChannelName](s, admproto.OpChannelPrioDelete)))
	mux.HandleFunc("POST /api/channels/wgkey", s.need(area("network", store.Change), s.wgKey))
	mux.HandleFunc("POST /api/channels/xray", s.need(area("network", store.Change), s.xrayParse))
	mux.HandleFunc("GET /api/overview", s.need(area("main", store.View), s.overview))
	mux.HandleFunc("GET /api/me", s.need(anyone, s.meGet))
	mux.HandleFunc("POST /api/me/password", s.need(anyone, s.mePassword))
	mux.HandleFunc("POST /api/me/token", s.need(anyone, s.meToken))
	mux.HandleFunc("POST /api/me/sessions/end", s.need(anyone, s.meSessionsEnd))
	mux.HandleFunc("GET /api/users", s.need(area("users", store.View), s.usersList))
	mux.HandleFunc("POST /api/users/save", s.need(area("users", store.Change), s.usersSave))
	mux.HandleFunc("POST /api/users/delete", s.need(area("users", store.Change), s.usersDelete))
	mux.HandleFunc("POST /api/users/token", s.need(area("users", store.Change), s.usersToken))
	mux.HandleFunc("POST /api/roles/save", s.need(area("users", store.Change), s.rolesSave))
	mux.HandleFunc("POST /api/roles/delete", s.need(area("users", store.Change), s.rolesDelete))
	mux.HandleFunc("GET /api/sessions", s.need(area("users", store.View), s.sessionsList))
	mux.HandleFunc("POST /api/sessions/end", s.need(area("users", store.Change), s.sessionsEnd))
	mux.HandleFunc("GET /api/audit", s.need(area("users", store.View), s.auditList))
	mux.HandleFunc("GET /api/invite/{token}", s.inviteGet)
	mux.HandleFunc("POST /api/invite", s.inviteAccept)
	mux.HandleFunc("GET /api/nodecfg", s.need(area("node", store.View), s.nodeCfg))
	mux.HandleFunc("POST /api/nodecfg", s.need(area("node", store.Change), s.nodeCfgSave))
	mux.HandleFunc("POST /api/nodecfg/lists", s.need(area("node", store.Change), s.nodeLists))
	mux.HandleFunc("GET /api/nodecfg/log", s.need(area("node", store.View), s.nodeLog))
	mux.HandleFunc("POST /api/nodecfg/token", s.need(area("node", store.Change), s.nodeToken))
	mux.HandleFunc("POST /api/nodecfg/cdn", s.need(area("node", store.Change), s.cdnRanges))
	mux.HandleFunc("GET /api/domains", s.need(area("domains", store.View), s.domainsGet))
	mux.HandleFunc("GET /api/domains/records", s.need(area("domains", store.View), s.zoneRecords))
	mux.HandleFunc("GET /api/domains/provider-zones", s.need(area("domains", store.Change), s.providerZones))
	mux.HandleFunc("POST /api/domains/provider", s.need(area("domains", store.Change), s.providerSave))
	mux.HandleFunc("POST /api/domains/zone", s.need(area("domains", store.Change), s.zoneSave))
	mux.HandleFunc("POST /api/domains/record", s.need(area("domains", store.Change), s.recordSave))
	mux.HandleFunc("POST /api/domains/cert", s.need(area("domains", store.Change), s.certIssueAny))
	mux.HandleFunc("POST /api/domains/cert/upload", s.need(area("domains", store.Change), s.certUpload))
	mux.HandleFunc("POST /api/domains/cert/delete", s.need(area("domains", store.Change), s.certDelete))
	mux.HandleFunc("GET /api/mail", s.need(area("mail", store.View), s.mailGet))
	mux.HandleFunc("GET /api/mail/stats", s.need(area("mail", store.View), s.mailStats))
	mux.HandleFunc("GET /api/mail/dns", s.need(area("mail", store.View), s.mailDNS))
	mux.HandleFunc("POST /api/mail/dns", s.need(area("mail", store.Change), s.mailDNSCreate))
	mux.HandleFunc("POST /api/mail/domain", s.need(area("mail", store.Change), s.mailDomain))
	mux.HandleFunc("POST /api/mail/box", s.need(area("mail", store.Change), s.mailBox))
	mux.HandleFunc("POST /api/mail/alias", s.need(area("mail", store.Change), s.mailAlias))
	mux.HandleFunc("POST /api/mail/apply", s.need(area("mail", store.Change), s.mailApply))
	mux.HandleFunc("POST /api/mail/cert", s.need(area("mail", store.Change), s.mailCert))
	mux.HandleFunc("GET /api/system/users", s.need(area("system", store.View), s.sysUsers))
	mux.HandleFunc("POST /api/system/user", s.need(area("system", store.Change), s.sysUserSave))
	mux.HandleFunc("POST /api/system/user/delete", s.need(area("system", store.Change), s.sysUserDelete))
	mux.HandleFunc("GET /api/system/guard", s.need(area("system", store.View), s.guardGet))
	mux.HandleFunc("POST /api/system/guard", s.need(area("system", store.Change), s.guardSet))
	mux.HandleFunc("POST /api/system/guard/ban", s.need(area("system", store.Change), s.guardBan))
	mux.HandleFunc("POST /api/system/guard/unban", s.need(area("system", store.Change), s.guardUnban))
	mux.HandleFunc("GET /api/system/backup", s.need(area("system", store.View), s.backupGet))
	mux.HandleFunc("GET /api/system/backup/job", s.need(area("system", store.View), s.backupJob))
	mux.HandleFunc("POST /api/system/backup/settings", s.need(area("system", store.Change), s.backupSettings))
	mux.HandleFunc("POST /api/system/backup/phrase", s.need(area("system", store.Change), s.backupPhrase))
	mux.HandleFunc("POST /api/system/backup/store", s.need(area("system", store.Change), s.backupStoreSave))
	mux.HandleFunc("POST /api/system/backup/run", s.need(area("system", store.Change), s.backupRunNow))
	mux.HandleFunc("POST /api/system/backup/fetch", s.need(area("system", store.Change), s.backupFetch))
	mux.HandleFunc("POST /api/system/backup/upload", s.need(area("system", store.Change), s.backupUpload))
	mux.HandleFunc("POST /api/system/backup/restore", s.need(area("system", store.Change), s.backupRestore))
	mux.HandleFunc("POST /api/qr", s.need(area("members", store.Change), s.qrGet))
	mux.HandleFunc("GET /api/presence", s.need(area("members,segments,main", store.View), s.presenceGet))
	mux.HandleFunc("GET /api/settings", s.need(area("settings", store.View), s.settingsGet))
	mux.HandleFunc("POST /api/settings/panel", s.need(area("settings", store.Change), s.settingsPanel))
	mux.HandleFunc("GET /api/settings/timezone", s.need(area("settings", store.View), s.timezoneGet))
	mux.HandleFunc("POST /api/settings/timezone", s.need(area("settings", store.Change), s.timezoneSet))
	mux.HandleFunc("POST /api/settings/notify", s.need(area("settings", store.Change), s.settingsNotify))
	mux.HandleFunc("POST /api/settings/notify/test", s.need(area("settings", store.Change), s.settingsNotifyTest))
	if s.ACME != nil {
		mux.HandleFunc("GET /.well-known/acme-challenge/{token}", s.ACME.challenge)
	}
	noAPI := func(w http.ResponseWriter, r *http.Request) { fail(w, http.StatusNotFound, "no such API") }
	mux.HandleFunc("GET /api/", noAPI)
	mux.HandleFunc("POST /api/", noAPI)

	var ui fs.FS
	if s.UIDir != "" {
		if _, err := os.Stat(filepath.Join(s.UIDir, "index.html")); err != nil {
			return nil, errors.New("ui dir has no index.html: " + s.UIDir)
		}
		ui = os.DirFS(s.UIDir)
	} else {
		sub, err := fs.Sub(uiFS, "ui")
		if err != nil {
			return nil, err
		}
		ui = sub
	}
	files := http.FileServerFS(ui)
	mux.Handle("GET /", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-store")
		files.ServeHTTP(w, r)
	}))
	return s.guard(mux), nil
}

// guard sets security headers and refuses unknown Host names (a stranger's
// DNS name pointed at this IP must not reach the panel).
func (s *Server) guard(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hd := w.Header()
		hd.Set("X-Content-Type-Options", "nosniff")
		hd.Set("X-Frame-Options", "DENY")
		hd.Set("Referrer-Policy", "same-origin")
		hd.Set("Content-Security-Policy", "default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'")
		if !s.hostOK(r.Host) {
			http.Error(w, "unknown host", http.StatusMisdirectedRequest)
			return
		}
		h.ServeHTTP(w, r)
	})
}

// addHost lets the panel answer to name from now on (after domain.set).
func (s *Server) addHost(name string) {
	if name == "" {
		return
	}
	s.hostsMu.Lock()
	defer s.hostsMu.Unlock()
	for _, h := range s.Hosts {
		if strings.EqualFold(h, name) {
			return
		}
	}
	s.Hosts = append(s.Hosts, name)
}

func (s *Server) hostOK(host string) bool {
	s.hostsMu.RLock()
	defer s.hostsMu.RUnlock()
	if len(s.Hosts) == 0 {
		return true
	}
	h, _, err := net.SplitHostPort(host)
	if err != nil {
		h = host
	}
	h = strings.ToLower(strings.TrimSuffix(h, "."))
	if h == "localhost" || net.ParseIP(strings.Trim(h, "[]")).IsLoopback() {
		return true
	}
	for _, x := range s.Hosts {
		if strings.EqualFold(x, h) {
			return true
		}
	}
	return false
}

// clientIP is the peer, or X-Forwarded-For's last hop when the peer is the
// local reverse proxy.
func clientIP(r *http.Request) string {
	h, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		h = r.RemoteAddr
	}
	if ip := net.ParseIP(h); ip != nil && ip.IsLoopback() {
		if xff := r.Header.Get("X-Forwarded-For"); xff != "" {
			parts := strings.Split(xff, ",")
			if p := strings.TrimSpace(parts[len(parts)-1]); net.ParseIP(p) != nil {
				return p
			}
		}
	}
	return h
}

func secure(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	h, _, _ := net.SplitHostPort(r.RemoteAddr)
	ip := net.ParseIP(h)
	return ip != nil && ip.IsLoopback() && r.Header.Get("X-Forwarded-Proto") == "https"
}

// knownIP is false when the panel only sees the local proxy: loft-node hands
// non-loft TLS to nginx as plain TCP, so behind it every client is 127.0.0.1.
// Blocking that address would lock everyone out.
func knownIP(ip string) bool {
	p := net.ParseIP(ip)
	return p != nil && !p.IsLoopback()
}

func (s *Server) slow() *auth.Slow {
	if s.Slow == nil {
		s.Slow = auth.NewSlow()
	}
	return s.Slow
}

type ctxKey struct{}

// Who may call a route: a signed-in user whose role gives Level on one of
// Areas (comma-separated; empty = any signed-in user), or the owner role.
type access struct {
	areas []string
	level int
	owner bool
}

// any signed-in user
var anyone = access{}

func area(a string, level int) access { return access{areas: strings.Split(a, ","), level: level} }

var ownerOnly = access{owner: true}

// allowed reports whether role passes a.
func (s *Server) allowed(role string, a access) bool {
	if role == store.RoleOwner {
		return true
	}
	if a.owner {
		return false
	}
	if len(a.areas) == 0 {
		return true
	}
	for _, x := range a.areas {
		if s.Store.Perm(role, x) >= a.level {
			return true
		}
	}
	return false
}

// need checks the session (or an API token in Authorization: Bearer) and
// the role; state-changing methods with a session also need its CSRF token
// in X-CSRF-Token. A change that succeeded goes to the audit log.
func (s *Server) need(a access, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var u store.User
		from := clientIP(r)
		if tok, ok := strings.CutPrefix(r.Header.Get("Authorization"), "Bearer "); ok {
			var found bool
			u, found = s.Store.ByToken(strings.TrimSpace(tok))
			if !found || u.Disabled {
				fail(w, http.StatusUnauthorized, "bad API token")
				return
			}
			from = "token"
			if time.Since(u.TokenUsed) > time.Minute {
				s.Store.Update(u.Login, func(x *store.User) { x.TokenUsed = time.Now().UTC() })
			}
		} else {
			c, err := r.Cookie(cookieName)
			var ss *auth.Session
			if err == nil {
				ss = s.Sessions.Get(c.Value)
			}
			if ss == nil {
				fail(w, http.StatusUnauthorized, "sign in")
				return
			}
			u, err = s.Store.Get(ss.User)
			if err != nil || u.Disabled {
				s.Sessions.Delete(c.Value)
				fail(w, http.StatusUnauthorized, "sign in")
				return
			}
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				if t := r.Header.Get("X-CSRF-Token"); t == "" || subtle.ConstantTimeCompare([]byte(t), []byte(ss.CSRF)) != 1 {
					fail(w, http.StatusForbidden, "stale page: reload")
					return
				}
			}
		}
		if !s.allowed(u.Role, a) {
			fail(w, http.StatusForbidden, "your role cannot do this")
			return
		}
		mark := &auditMark{}
		ctx := context.WithValue(context.WithValue(r.Context(), ctxKey{}, u), markKey{}, mark)
		if r.Method == http.MethodGet || r.Method == http.MethodHead {
			h(w, r.WithContext(ctx))
			return
		}
		sw := &statusWriter{ResponseWriter: w, code: http.StatusOK}
		h(sw, r.WithContext(ctx))
		if sw.code < 400 && !mark.skip && s.Audit != nil {
			s.Audit.Add(store.Entry{User: u.Login, Op: r.Pattern, Target: mark.target, From: from})
		}
	}
}

type markKey struct{}

type auditMark struct {
	target string
	skip   bool
}

// sysEvent puts what the panel did by itself into the audit log, as "system".
func (s *Server) sysEvent(op, target string) {
	if s.Audit != nil {
		s.Audit.Add(store.Entry{User: "system", Op: "system " + op, Target: target})
	}
}

// auditTarget names what a change was about in the audit log.
func auditTarget(r *http.Request, t string) {
	if m, ok := r.Context().Value(markKey{}).(*auditMark); ok {
		m.target = t
	}
}

// auditSkip keeps a call out of the log (checks, previews).
func auditSkip(r *http.Request) {
	if m, ok := r.Context().Value(markKey{}).(*auditMark); ok {
		m.skip = true
	}
}

type statusWriter struct {
	http.ResponseWriter
	code int
}

func (w *statusWriter) WriteHeader(c int) { w.code = c; w.ResponseWriter.WriteHeader(c) }
func (w *statusWriter) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func userOf(r *http.Request) store.User {
	u, _ := r.Context().Value(ctxKey{}).(store.User)
	return u
}

type meJSON struct {
	Login    string         `json:"login"`
	Name     string         `json:"name,omitempty"`
	Role     string         `json:"role"`
	RoleName string         `json:"role_name"`
	Perms    map[string]int `json:"perms"` // area → 0 none, 1 view, 2 change
	CSRF     string         `json:"csrf"`
}

func (s *Server) me(u store.User, csrf string) *meJSON {
	r, _ := s.Store.RoleOf(u.Role)
	perms := map[string]int{}
	for _, a := range store.Areas {
		perms[a] = r.Perms[a]
	}
	return &meJSON{Login: u.Login, Name: u.Name, Role: u.Role, RoleName: r.Name, Perms: perms, CSRF: csrf}
}

func (s *Server) state(w http.ResponseWriter, r *http.Request) {
	out := struct {
		Setup          bool    `json:"setup"`
		Me             *meJSON `json:"me,omitempty"`
		Wizard         bool    `json:"wizard"` // the owner has not finished the wizard
		Lang           string  `json:"lang"`   // the panel's default language
		KeepMemberKeys bool    `json:"keep_member_keys"`
		Version        string  `json:"version,omitempty"` // signed in only: the footer shows it
	}{Setup: s.Store.Empty(), Lang: "ru"}
	if s.Settings != nil {
		if st, err := s.Settings.Get(); err == nil {
			out.Wizard = !st.WizardDone
			out.Lang = firstOf(st.Lang, "ru")
			out.KeepMemberKeys = st.KeepMemberKeys
		}
	}
	if c, err := r.Cookie(cookieName); err == nil {
		if ss := s.Sessions.Get(c.Value); ss != nil {
			if u, err := s.Store.Get(ss.User); err == nil && !u.Disabled {
				out.Me = s.me(u, ss.CSRF)
				out.Version = version.Version
			}
		}
	}
	ok(w, out)
}

type credJSON struct {
	Login    string `json:"login"`
	Password string `json:"password"`
	Code     string `json:"code,omitempty"`
	Remember bool   `json:"remember,omitempty"`
}

func readJSON(w http.ResponseWriter, r *http.Request, max int64, v any) bool {
	r.Body = http.MaxBytesReader(w, r.Body, max)
	dec := json.NewDecoder(r.Body)
	dec.DisallowUnknownFields()
	if err := dec.Decode(v); err != nil {
		fail(w, http.StatusBadRequest, "bad request")
		return false
	}
	return true
}

// setup creates the first owner. It needs SetupCode, which only someone with
// shell access to the server can read, so the first visitor cannot claim it.
func (s *Server) setup(w http.ResponseWriter, r *http.Request) {
	var in credJSON
	if !readJSON(w, r, 4<<10, &in) {
		return
	}
	ip := clientIP(r)
	known := knownIP(ip)
	if known {
		if b, _ := s.Limit.Blocked(ip); b {
			fail(w, http.StatusTooManyRequests, "too many attempts: try later")
			return
		}
	}
	if !s.Store.Empty() {
		fail(w, http.StatusConflict, "the panel already has an owner")
		return
	}
	if s.SetupCode == "" || subtle.ConstantTimeCompare([]byte(strings.TrimSpace(in.Code)), []byte(s.SetupCode)) != 1 {
		if known {
			s.Limit.Fail(ip)
			s.sleep(s.Limit.Delay)
		} else {
			s.sleep(s.slow().Fail("setup"))
		}
		fail(w, http.StatusForbidden, "wrong setup code")
		return
	}
	h, err := auth.Hash(in.Password)
	if err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	if err := s.Store.Add(store.User{Login: in.Login, Role: store.RoleOwner, Hash: h}); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	s.logf("setup: owner %q created from %s", in.Login, ip)
	if s.SetupFile != "" {
		os.Remove(s.SetupFile)
	}
	s.SetupCode = "" // one owner per setup code
	s.startSession(w, r, in.Login, false)
}

func (s *Server) login(w http.ResponseWriter, r *http.Request) {
	var in credJSON
	if !readJSON(w, r, 4<<10, &in) {
		return
	}
	ip := clientIP(r)
	known := knownIP(ip)
	if known {
		if b, _ := s.Limit.Blocked(ip); b {
			fail(w, http.StatusTooManyRequests, "too many attempts: try later")
			return
		}
	}
	u, err := s.Store.Get(in.Login)
	hash := dummyHash
	if err == nil {
		hash = u.Hash
	}
	good := auth.Verify(hash, in.Password) && err == nil && !u.Disabled
	if !good {
		if known {
			// The guard (fail2ban) reads this line: blocked here, the
			// address is also banned at the firewall, on every port.
			s.logf("login failed from %s", ip)
			if s.Limit.Fail(ip) {
				s.logf("login: %s blocked after failed attempts", ip)
				s.sysEvent("login-block", ip)
			}
			s.sleep(s.Limit.Delay)
		} else {
			s.sleep(s.slow().Fail("login:" + in.Login))
		}
		fail(w, http.StatusUnauthorized, "wrong login or password")
		return
	}
	if known {
		s.Limit.OK(ip)
	} else {
		s.slow().OK("login:" + in.Login)
	}
	s.Store.Update(u.Login, func(x *store.User) { x.Seen = time.Now().UTC() })
	s.Audit.Add(store.Entry{User: u.Login, Op: "POST /api/login", From: clientIP(r)})
	s.startSession(w, r, u.Login, in.Remember)
}

func (s *Server) startSession(w http.ResponseWriter, r *http.Request, login string, remember bool) {
	u, _ := s.Store.Get(login)
	ss := s.Sessions.Create(login, clientIP(r), r.UserAgent(), remember)
	c := &http.Cookie{Name: cookieName, Value: ss.ID, Path: "/", HttpOnly: true,
		Secure: secure(r), SameSite: http.SameSiteStrictMode}
	if remember {
		_, max := s.Sessions.Limits()
		c.MaxAge = int(max / time.Second)
	}
	http.SetCookie(w, c)
	ok(w, s.me(u, ss.CSRF))
}

func (s *Server) logout(w http.ResponseWriter, r *http.Request) {
	if c, err := r.Cookie(cookieName); err == nil {
		s.Sessions.Delete(c.Value)
	}
	http.SetCookie(w, &http.Cookie{Name: cookieName, Value: "", Path: "/", HttpOnly: true, Secure: secure(r), SameSite: http.SameSiteStrictMode, MaxAge: -1})
	ok(w, struct{}{})
}

// ── network.conf ──

func (s *Server) networkGet(w http.ResponseWriter, r *http.Request) {
	var n admproto.Network
	if err := s.Adm(userOf(r).Login).Call(r.Context(), admproto.OpNetworkGet, nil, &n); err != nil {
		admFail(w, err)
		return
	}
	ok(w, n)
}

type checkJSON struct {
	OK    bool   `json:"ok"`
	Error string `json:"error,omitempty"`
}

// networkCheck runs the node's parser here, without the daemon, so the
// editor can show errors while typing.
func (s *Server) networkCheck(w http.ResponseWriter, r *http.Request) {
	auditSkip(r)
	var in struct {
		Text string `json:"text"`
	}
	if !readJSON(w, r, 1<<20, &in) {
		return
	}
	if _, err := netconf.ParseCommands(in.Text); err != nil {
		ok(w, checkJSON{Error: err.Error()})
		return
	}
	ok(w, checkJSON{OK: true})
}

func (s *Server) networkApply(w http.ResponseWriter, r *http.Request) {
	var in admproto.NetworkApply
	if !readJSON(w, r, 1<<20, &in) {
		return
	}
	by := userOf(r).Login
	s.startJob(w, by, func(ctx context.Context) (any, error) {
		var n admproto.Network
		if err := s.Adm(by).Call(ctx, admproto.OpNetworkApply, in, &n); err != nil {
			return nil, admErr(err)
		}
		s.logf("network.conf applied by %s", by)
		return n, nil
	})
}

type memberKeyOut struct {
	admproto.IssuedKey
	Link string `json:"link,omitempty"`
	File string `json:"file,omitempty"`
	QR   string `json:"qr,omitempty"`
}

func (s *Server) keepMemberKeys() bool {
	if s.Settings == nil {
		return false
	}
	st, err := s.Settings.Get()
	return err == nil && st.KeepMemberKeys
}

func (s *Server) persistIssued(k admproto.IssuedKey) {
	if s.Keys == nil || !s.keepMemberKeys() || k.ID == "" || k.Key == "" {
		return
	}
	if err := s.Keys.Put(k.ID, k.Key, k.KeySHA256); err != nil {
		s.logf("keep member key %s: %v", k.ID, err)
	}
}

func (s *Server) wrapIssued(ctx context.Context, a Adm, k admproto.IssuedKey) memberKeyOut {
	out := memberKeyOut{IssuedKey: k}
	var info admproto.NodeInfo
	if err := a.Call(ctx, admproto.OpNodeInfo, nil, &info); err == nil && info.Host != "" {
		out.Link, out.File = s.memberLink(info, k.Key, k.ID, k.ID)
		out.QR, _ = qrSVG(out.Link)
	}
	return out
}

func (s *Server) memberKey(w http.ResponseWriter, r *http.Request) {
	var in admproto.MemberKey
	if !readJSON(w, r, 4<<10, &in) {
		return
	}
	auditTarget(r, in.ID)
	a := s.Adm(userOf(r).Login)
	var k admproto.IssuedKey
	if err := a.Call(r.Context(), admproto.OpMemberKey, in, &k); err != nil {
		admFail(w, err)
		return
	}
	s.persistIssued(k)
	ok(w, s.wrapIssued(r.Context(), a, k))
}

func (s *Server) memberKeyGet(w http.ResponseWriter, r *http.Request) {
	id := strings.TrimSpace(r.URL.Query().Get("id"))
	if id == "" {
		fail(w, http.StatusBadRequest, "member id required")
		return
	}
	auditTarget(r, id)
	if !s.keepMemberKeys() {
		fail(w, http.StatusNotFound, "member keys are not kept")
		return
	}
	k, err := s.lookupMemberKey(r.Context(), userOf(r).Login, id)
	if err != nil {
		if errors.Is(err, errKeyNotStored) {
			fail(w, http.StatusNotFound, err.Error())
			return
		}
		admFail(w, err)
		return
	}
	ok(w, s.wrapIssued(r.Context(), s.Adm(userOf(r).Login), k))
}

func (s *Server) lookupMemberKey(ctx context.Context, by, id string) (admproto.IssuedKey, error) {
	var stored store.StoredKey
	var have bool
	if s.Keys != nil {
		var err error
		stored, have, err = s.Keys.Get(id)
		if err != nil {
			return admproto.IssuedKey{}, err
		}
	}
	var peeked admproto.IssuedKey
	peekErr := s.Adm(by).Call(ctx, admproto.OpMemberGet, admproto.MemberKey{ID: id}, &peeked)
	if peekErr == nil && peeked.Key != "" {
		s.persistIssued(peeked)
		return peeked, nil
	}
	if have && stored.Key != "" {
		return admproto.IssuedKey{ID: id, Key: stored.Key, KeySHA256: stored.SHA256}, nil
	}
	if peekErr != nil && !strings.Contains(peekErr.Error(), "no stored key") {
		return admproto.IssuedKey{}, peekErr
	}
	return admproto.IssuedKey{}, errKeyNotStored
}

func (s *Server) nodeStatus(w http.ResponseWriter, r *http.Request) {
	var st admproto.NodeStatus
	if err := s.Adm(userOf(r).Login).Call(r.Context(), admproto.OpNodeStatus, nil, &st); err != nil {
		admFail(w, err)
		return
	}
	ok(w, st)
}

func (s *Server) nodeRestart(w http.ResponseWriter, r *http.Request) {
	by := userOf(r).Login
	s.startJob(w, by, func(ctx context.Context) (any, error) {
		var st admproto.NodeStatus
		if err := s.Adm(by).Call(ctx, admproto.OpNodeRestart, nil, &st); err != nil {
			return nil, admErr(err)
		}
		return st, nil
	})
}

// ── replies ──

func ok(w http.ResponseWriter, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, code int, msg string) {
	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	json.NewEncoder(w).Encode(map[string]string{"error": msg})
}

// admErr makes the daemon's errors readable for the operator.
func admErr(err error) error {
	if errors.Is(err, admproto.ErrUnavailable) {
		return errors.New("loft-panel-adm is not running")
	}
	return err
}

func admFail(w http.ResponseWriter, err error) {
	if errors.Is(err, admproto.ErrUnavailable) {
		fail(w, http.StatusServiceUnavailable, "loft-panel-adm is not running")
		return
	}
	fail(w, http.StatusUnprocessableEntity, err.Error())
}

func (s *Server) logf(f string, a ...any) {
	if s.Log != nil {
		s.Log.Printf(f, a...)
	}
}

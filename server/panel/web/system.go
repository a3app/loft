package web

// system.go is the System screen's Linux accounts and the guard; the
// screen's backups live in backup.go. Linux accounts are the server's own
// users (for ssh and the like), managed by panel-adm, the root helper. The
// guard is fail2ban: it bans addresses at the firewall after repeated failed
// logins or scanner requests. The helper writes fail2ban's files; the panel
// keeps its own copy of the settings in guard.json, applies the same numbers
// to its own login limiter, and keeps notes on manual bans.

import (
	"context"
	"encoding/json"
	"net"
	"net/http"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/a3app/loft/server/panel/admproto"
	"github.com/a3app/loft/server/panel/store"
)

/* ── Linux accounts ── */

// sysUsers lists the server's Linux accounts.
func (s *Server) sysUsers(w http.ResponseWriter, r *http.Request) {
	var v admproto.SysUsers
	if err := s.Adm(userOf(r).Login).Call(r.Context(), admproto.OpSysUsers, nil, &v); err != nil {
		admFail(w, err)
		return
	}
	ok(w, v)
}

// sysUserSave adds or changes a Linux account through the helper.
func (s *Server) sysUserSave(w http.ResponseWriter, r *http.Request) {
	var in admproto.SysUserSave
	if !readJSON(w, r, 128<<10, &in) {
		return
	}
	auditTarget(r, in.Login)
	var v admproto.SysUsers
	if err := s.Adm(userOf(r).Login).Call(r.Context(), admproto.OpSysUserSave, in, &v); err != nil {
		admFail(w, err)
		return
	}
	ok(w, v)
}

// sysUserDelete removes a Linux account through the helper.
func (s *Server) sysUserDelete(w http.ResponseWriter, r *http.Request) {
	var in admproto.SysUserDelete
	if !readJSON(w, r, 1<<10, &in) {
		return
	}
	auditTarget(r, in.Login)
	var v admproto.SysUsers
	if err := s.Adm(userOf(r).Login).Call(r.Context(), admproto.OpSysUserDelete, in, &v); err != nil {
		admFail(w, err)
		return
	}
	ok(w, v)
}

/* ── the guard ── */

// guardFile is guard.json in the panel's data directory: the guard settings
// and the notes on manual bans.
type guardFile struct {
	Conf admproto.GuardConf `json:"conf"`
	// Notes on manual bans, by address: fail2ban keeps no reason.
	Notes map[string]string `json:"notes,omitempty"`
}

// defaultGuard is used until the operator saves settings: 5 failures in 10
// minutes ban for a day; scanners and ssh/mail logins count too.
var defaultGuard = admproto.GuardConf{Fails: 5, FindMin: 10, BanSec: 86400, Scanners: true, Logins: true, Allow: []string{}}

// loadGuard reads guard.json; a missing or broken file gives the defaults.
func (s *Server) loadGuard() guardFile {
	g := guardFile{Conf: defaultGuard}
	if b, err := os.ReadFile(s.backupPath("guard.json")); err == nil {
		json.Unmarshal(b, &g)
	}
	if g.Conf.Allow == nil {
		g.Conf.Allow = []string{}
	}
	return g
}

// saveGuard writes guard.json atomically (a crash leaves the old or the new
// file, never half of one).
func (s *Server) saveGuard(g guardFile) error {
	b, _ := json.MarshalIndent(g, "", "  ")
	return store.WriteAtomic(s.backupPath("guard.json"), append(b, '\n'), 0o600)
}

// applyGuard gives the panel's own login limit the guard's numbers.
func (s *Server) applyGuard(c admproto.GuardConf) {
	if s.Limit == nil {
		return
	}
	var allow []*net.IPNet
	for _, a := range c.Allow {
		if _, n, err := net.ParseCIDR(a); err == nil {
			allow = append(allow, n)
		} else if ip := net.ParseIP(a); ip != nil {
			bits := 32
			if ip.To4() == nil {
				bits = 128
			}
			allow = append(allow, &net.IPNet{IP: ip, Mask: net.CIDRMask(bits, bits)})
		}
	}
	s.Limit.Set(c.Fails, time.Duration(c.FindMin)*time.Minute, time.Duration(c.BanSec)*time.Second, allow)
}

// LoadGuard applies the saved guard settings at start.
func (s *Server) LoadGuard() { s.applyGuard(s.loadGuard().Conf) }

// guardOut replies with the guard's state, the saved settings, the notes
// next to each ban, and the caller's own address (so the UI can warn
// before the operator bans themselves).
func (s *Server) guardOut(w http.ResponseWriter, r *http.Request, v admproto.GuardView) {
	g := s.loadGuard()
	for i, b := range v.Banned {
		v.Banned[i].Reason = g.Notes[b.IP]
	}
	ok(w, map[string]any{"guard": v, "conf": g.Conf, "you": clientIP(r)})
}

// guardGet returns the guard's state. If fail2ban is installed but still
// has only the package's own setup, it writes the panel's settings first.
func (s *Server) guardGet(w http.ResponseWriter, r *http.Request) {
	adm := s.Adm(userOf(r).Login)
	var v admproto.GuardView
	if err := adm.Call(r.Context(), admproto.OpGuard, nil, &v); err != nil {
		admFail(w, err)
		return
	}
	// Installed but still the package's own jail: write the panel's.
	if v.Installed && !v.Configured {
		ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
		defer cancel()
		if err := adm.Call(ctx, admproto.OpGuardSet, s.loadGuard().Conf, &v); err != nil {
			s.logf("guard: %v", err)
		}
	}
	auditSkip(r)
	s.guardOut(w, r, v)
}

// guardSet saves new guard settings in fail2ban (through the helper), in
// guard.json and in the panel's login limiter.
func (s *Server) guardSet(w http.ResponseWriter, r *http.Request) {
	var in admproto.GuardConf
	if !readJSON(w, r, 32<<10, &in) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 60*time.Second)
	defer cancel()
	var v admproto.GuardView
	if err := s.Adm(userOf(r).Login).Call(ctx, admproto.OpGuardSet, in, &v); err != nil {
		admFail(w, err)
		return
	}
	// The helper cleaned the list; keep what it accepted.
	g := s.loadGuard()
	g.Conf = in
	if g.Conf.Allow == nil {
		g.Conf.Allow = []string{}
	}
	for i, a := range g.Conf.Allow {
		g.Conf.Allow[i] = strings.TrimSpace(a)
	}
	g.Conf.Allow = slices.DeleteFunc(g.Conf.Allow, func(a string) bool { return a == "" })
	if err := s.saveGuard(g); err != nil {
		fail(w, http.StatusInternalServerError, err.Error())
		return
	}
	s.applyGuard(g.Conf)
	s.guardOut(w, r, v)
}

// covers says whether an address or subnet would ban ip.
func covers(target, ip string) bool {
	a := net.ParseIP(ip)
	if t := net.ParseIP(target); t != nil {
		return a != nil && t.Equal(a)
	}
	_, n, err := net.ParseCIDR(target)
	return err == nil && a != nil && n.Contains(a)
}

// guardBan bans an address or subnet by hand, with an optional note. It
// refuses to ban the caller's own address, loopback, or anything that
// overlaps the allowed list.
func (s *Server) guardBan(w http.ResponseWriter, r *http.Request) {
	var in admproto.GuardBan
	if !readJSON(w, r, 2<<10, &in) {
		return
	}
	in.IP = strings.TrimSpace(in.IP)
	you := clientIP(r)
	if covers(in.IP, you) {
		fail(w, http.StatusBadRequest, "that would ban your own address "+you)
		return
	}
	if covers(in.IP, "127.0.0.1") || covers(in.IP, "::1") {
		fail(w, http.StatusBadRequest, "the loopback cannot be banned")
		return
	}
	for _, a := range s.loadGuard().Conf.Allow {
		if a == in.IP || covers(in.IP, strings.Split(a, "/")[0]) || covers(a, strings.Split(in.IP, "/")[0]) {
			fail(w, http.StatusBadRequest, in.IP+" is on the allowed list")
			return
		}
	}
	if len(in.Reason) > 200 {
		in.Reason = in.Reason[:200]
	}
	auditTarget(r, in.IP)
	var v admproto.GuardView
	if err := s.Adm(userOf(r).Login).Call(r.Context(), admproto.OpGuardBan, admproto.GuardBan{IP: in.IP, Sec: in.Sec}, &v); err != nil {
		admFail(w, err)
		return
	}
	g := s.loadGuard()
	if g.Notes == nil {
		g.Notes = map[string]string{}
	}
	if note := strings.TrimSpace(in.Reason); note != "" {
		g.Notes[in.IP] = note
	}
	s.saveGuard(g)
	s.guardOut(w, r, v)
}

// guardUnban lifts a ban and forgets its note.
func (s *Server) guardUnban(w http.ResponseWriter, r *http.Request) {
	var in admproto.GuardBan
	if !readJSON(w, r, 1<<10, &in) {
		return
	}
	auditTarget(r, in.IP)
	var v admproto.GuardView
	if err := s.Adm(userOf(r).Login).Call(r.Context(), admproto.OpGuardUnban, admproto.GuardBan{IP: in.IP}, &v); err != nil {
		admFail(w, err)
		return
	}
	g := s.loadGuard()
	delete(g.Notes, in.IP)
	s.saveGuard(g)
	s.guardOut(w, r, v)
}

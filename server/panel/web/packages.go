package web

// packages.go is the Packages screen: optional software from the closed
// catalog (package catalog) that the panel can install on the server, such
// as the Telegram MTProto proxy. The list, installs and service switches
// are done by loft-panel-adm, the root helper. It runs installs in its own
// systemd unit, so unlike the panel's jobs (jobs.go) such a job outlives a
// restart of the panel; that is why the page polls /api/packages/job, not
// /api/jobs.

import (
	"context"
	"net/http"
	"time"

	"github.com/a3app/loft/server/panel/admproto"
	"github.com/a3app/loft/server/panel/catalog"
)

// packagesJSON is the Packages screen's data: every catalog package with
// its state, and the helper's current or last job, if any.
type packagesJSON struct {
	Packages []admproto.Package `json:"packages"`
	Job      *admproto.Job      `json:"job,omitempty"`
}

// packages returns the package list and the helper's job.
func (s *Server) packages(w http.ResponseWriter, r *http.Request) {
	adm := s.Adm(userOf(r).Login)
	var out packagesJSON
	if err := adm.Call(r.Context(), admproto.OpPackages, nil, &out.Packages); err != nil {
		admFail(w, err)
		return
	}
	var j admproto.Job
	if err := adm.Call(r.Context(), admproto.OpJobGet, nil, &j); err == nil && j.ID != "" {
		out.Job = &j
	}
	ok(w, out)
}

// packagesJob returns the helper's current or last job with its log tail,
// for the page to poll while an install runs.
func (s *Server) packagesJob(w http.ResponseWriter, r *http.Request) {
	var j admproto.Job
	if err := s.Adm(userOf(r).Login).Call(r.Context(), admproto.OpJobGet, nil, &j); err != nil {
		admFail(w, err)
		return
	}
	ok(w, j)
}

// packagesApply checks a plan (what to install, remove, upgrade or purge)
// and starts it as a helper job. The reply is the started job.
func (s *Server) packagesApply(w http.ResponseWriter, r *http.Request) {
	var p catalog.Plan
	if !readJSON(w, r, 16<<10, &p) {
		return
	}
	if err := p.Check(); err != nil {
		fail(w, http.StatusBadRequest, err.Error())
		return
	}
	var j admproto.Job
	if err := s.Adm(userOf(r).Login).Call(r.Context(), admproto.OpPackagesApply, p, &j); err != nil {
		admFail(w, err)
		return
	}
	s.logf("packages by %s: install %v remove %v upgrade %v purge %v", userOf(r).Login, p.Install, p.Remove, p.Upgrade, p.Purge)
	ok(w, j)
}

// mtprotoGet returns the MTProto proxy's settings and its share links.
func (s *Server) mtprotoGet(w http.ResponseWriter, r *http.Request) {
	var out admproto.MTProto
	if err := s.Adm(userOf(r).Login).Call(r.Context(), admproto.OpMTProtoGet, nil, &out); err != nil {
		admFail(w, err)
		return
	}
	ok(w, out)
}

// mtprotoSet changes the proxy's port or TLS domain, or makes a new secret.
// A new secret breaks the old share links.
func (s *Server) mtprotoSet(w http.ResponseWriter, r *http.Request) {
	var req admproto.MTProtoSet
	if !readJSON(w, r, 4<<10, &req) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 50*time.Second)
	defer cancel()
	var out admproto.MTProto
	if err := s.Adm(userOf(r).Login).Call(ctx, admproto.OpMTProtoSet, req, &out); err != nil {
		admFail(w, err)
		return
	}
	s.logf("mtproto settings by %s: port %d domain %s rotate %v", userOf(r).Login, req.Port, req.Domain, req.RotateSecret)
	ok(w, out)
}

// packageUnit switches a package's service on (enabled and started) or off.
func (s *Server) packageUnit(w http.ResponseWriter, r *http.Request) {
	var req admproto.PackageUnit
	if !readJSON(w, r, 4<<10, &req) {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 50*time.Second)
	defer cancel()
	var p admproto.Package
	if err := s.Adm(userOf(r).Login).Call(ctx, admproto.OpPackageUnit, req, &p); err != nil {
		admFail(w, err)
		return
	}
	ok(w, p)
}

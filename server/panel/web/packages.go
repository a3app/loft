package web

import (
	"context"
	"net/http"
	"time"

	"github.com/a3app/loft/server/panel/admproto"
	"github.com/a3app/loft/server/panel/catalog"
)

// Packages: the list and the job come from loft-panel-adm, which runs the
// job in its own unit. Unlike the panel's jobs (jobs.go) it outlives a
// restart of the panel, so the page polls /api/packages/job, not /api/jobs.

type packagesJSON struct {
	Packages []admproto.Package `json:"packages"`
	Job      *admproto.Job      `json:"job,omitempty"`
}

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

func (s *Server) packagesJob(w http.ResponseWriter, r *http.Request) {
	var j admproto.Job
	if err := s.Adm(userOf(r).Login).Call(r.Context(), admproto.OpJobGet, nil, &j); err != nil {
		admFail(w, err)
		return
	}
	ok(w, j)
}

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

func (s *Server) mtprotoGet(w http.ResponseWriter, r *http.Request) {
	var out admproto.MTProto
	if err := s.Adm(userOf(r).Login).Call(r.Context(), admproto.OpMTProtoGet, nil, &out); err != nil {
		admFail(w, err)
		return
	}
	ok(w, out)
}

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

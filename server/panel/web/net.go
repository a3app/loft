package web

// net.go is the "Network and IP" screen: the server's own IP addresses and
// routes. Reading and changing them is done by panel-adm, the root helper.
// A change is applied live but held: the helper undoes it after about two
// minutes unless the operator confirms it, so a mistake that cuts the
// panel off fixes itself.

import (
	"context"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/a3app/loft/server/panel/admproto"
)

// netState returns addresses, routes and any held change from the helper,
// and adds each address's reverse DNS name, looked up here because the
// helper has no network access. Lookups run in parallel and get two seconds
// in total.
func (s *Server) netState(w http.ResponseWriter, r *http.Request) {
	var st admproto.NetState
	if err := s.Adm(userOf(r).Login).Call(r.Context(), admproto.OpNetState, nil, &st); err != nil {
		admFail(w, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	var wg sync.WaitGroup
	for i := range st.Addrs {
		wg.Add(1)
		go func(a *admproto.NetAddr) {
			defer wg.Done()
			ip, _, _ := strings.Cut(a.Addr, "/")
			if names, err := net.DefaultResolver.LookupAddr(ctx, ip); err == nil && len(names) > 0 {
				a.PTR = strings.TrimSuffix(names[0], ".")
			}
		}(&st.Addrs[i])
	}
	wg.Wait()
	ok(w, st)
}

// netChange asks the helper to add or remove an address or route. The
// reply is the held change with its deadline; the UI must then confirm or
// revert it.
func (s *Server) netChange(w http.ResponseWriter, r *http.Request) {
	var req admproto.NetChange
	if !readJSON(w, r, 4<<10, &req) {
		return
	}
	var p admproto.NetPending
	if err := s.Adm(userOf(r).Login).Call(r.Context(), admproto.OpNetChange, req, &p); err != nil {
		admFail(w, err)
		return
	}
	s.logf("network %s %s%s by %s (held until confirmed)", req.Op, req.Addr, req.Dst, userOf(r).Login)
	ok(w, p)
}

// netHeld returns the handler that confirms or reverts a held change by its
// ID, depending on op (admproto.OpNetConfirm or OpNetRevert).
func (s *Server) netHeld(op string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		var req admproto.NetID
		if !readJSON(w, r, 1<<10, &req) {
			return
		}
		if err := s.Adm(userOf(r).Login).Call(r.Context(), op, req, nil); err != nil {
			admFail(w, err)
			return
		}
		ok(w, struct{}{})
	}
}

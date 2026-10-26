package web

import (
	"context"
	"net/http"

	"github.com/a3app/loft/server/panel/admproto"
)

// ctlRoll replaces the controller key as a job (the node restarts). The old
// key's clients and relays follow the signed hand-over for days days.
func (s *Server) ctlRoll(w http.ResponseWriter, r *http.Request) {
	var in admproto.CtlRoll
	if !readJSON(w, r, 1<<10, &in) {
		return
	}
	by := userOf(r).Login
	s.startJob(w, by, func(ctx context.Context) (any, error) {
		var v admproto.NodeConfig
		if err := s.Adm(by).Call(ctx, admproto.OpCtlRoll, in, &v); err != nil {
			return nil, admErr(err)
		}
		return v, nil
	})
}

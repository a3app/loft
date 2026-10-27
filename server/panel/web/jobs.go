package web

// This file runs the panel's own background jobs: work that outlives the
// HTTP request that started it (node restarts, ACME orders, downloading a
// backup copy). Jobs live only in memory, so a panel restart forgets them.
// These are separate from the helper's jobs (admproto.Job), which run in
// loft-panel-adm.

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

// Operations that restart loft-node run as jobs. The browser reaches the
// panel through the node, so the restart drops the very connection that
// asked for it: the request returns a job id at once, the work goes on
// without it, and the page polls /api/jobs/{id} until it is done.
type job struct {
	ID     string          `json:"id"`
	User   string          `json:"-"`
	State  string          `json:"state"` // running | done | error
	Error  string          `json:"error,omitempty"`
	Result json.RawMessage `json:"result,omitempty"`
	ended  time.Time
}

// jobs is the table of running and recently finished jobs, by id.
type jobs struct {
	mu sync.Mutex
	m  map[string]*job
}

// jobTimeout bounds one job (two node restarts, an ACME order).
const jobTimeout = 3 * time.Minute

// startJob runs fn in the background (with jobTimeout) and answers the
// request at once with 202 and {"job": id}. Whatever fn returns becomes the
// job's result or error, read later with jobGet. Finished jobs older than
// 10 minutes are cleaned up here, so the table stays small.
func (s *Server) startJob(w http.ResponseWriter, user string, fn func(ctx context.Context) (any, error)) {
	b := make([]byte, 12)
	rand.Read(b)
	j := &job{ID: hex.EncodeToString(b), User: user, State: "running"}
	s.jobs.mu.Lock()
	if s.jobs.m == nil {
		s.jobs.m = map[string]*job{}
	}
	for id, x := range s.jobs.m { // forget finished jobs after 10 minutes
		if !x.ended.IsZero() && time.Since(x.ended) > 10*time.Minute {
			delete(s.jobs.m, id)
		}
	}
	s.jobs.m[j.ID] = j
	s.jobs.mu.Unlock()
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), jobTimeout)
		defer cancel()
		v, err := fn(ctx)
		s.jobs.mu.Lock()
		defer s.jobs.mu.Unlock()
		j.ended = time.Now()
		if err != nil {
			j.State, j.Error = "error", err.Error()
			return
		}
		j.State = "done"
		if v != nil {
			j.Result, _ = json.Marshal(v)
		}
	}()
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusAccepted)
	json.NewEncoder(w).Encode(map[string]string{"job": j.ID})
}

// jobGet serves GET /api/jobs/{id}. A user only sees their own jobs; any
// other id answers 404 as if it did not exist.
func (s *Server) jobGet(w http.ResponseWriter, r *http.Request) {
	s.jobs.mu.Lock()
	j, found := s.jobs.m[r.PathValue("id")]
	var c job
	if found {
		c = *j
	}
	s.jobs.mu.Unlock()
	if !found || c.User != userOf(r).Login {
		fail(w, http.StatusNotFound, "no such job (the panel may have restarted)")
		return
	}
	ok(w, c)
}

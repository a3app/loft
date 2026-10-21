package web

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

type jobs struct {
	mu sync.Mutex
	m  map[string]*job
}

// jobTimeout bounds one job (two node restarts, an ACME order).
const jobTimeout = 3 * time.Minute

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

package metrics

import (
	"net/http"
	"time"
)

// NewHandler serves GET/HEAD /metrics. Other paths are 404.
func NewHandler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /metrics", serve)
	mux.HandleFunc("HEAD /metrics", serve)
	return mux
}

func serve(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	if r.Method == http.MethodHead {
		return
	}
	Write(w)
}

// Listen serves Prometheus text on addr until the process exits.
func Listen(addr string) error {
	s := &http.Server{
		Addr:              addr,
		Handler:           NewHandler(),
		ReadHeaderTimeout: 5 * time.Second,
	}
	return s.ListenAndServe()
}

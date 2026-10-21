package transport

import (
	"net/http"
	"testing"
)

func TestH2ServerMatch(t *testing.T) {
	s := &H2Server{Path: "/api/v1/tunnel"}
	req := func(method, path, host string) *http.Request {
		r, _ := http.NewRequest(method, "https://node"+path, nil)
		r.Host = host
		r.URL.Path = path
		return r
	}
	if !s.Match(req(http.MethodConnect, "/api/v1/tunnel", "node.example")) {
		t.Fatal("path")
	}
	if s.Match(req(http.MethodConnect, "", "node.example")) {
		t.Fatal("plain connect splice")
	}
	if s.Match(req(http.MethodGet, "/api/v1/tunnel", "node.example")) {
		t.Fatal("get")
	}
	if !s.Match(req(http.MethodConnect, "", "/api/v1/tunnel")) {
		t.Fatal("authority path")
	}
}

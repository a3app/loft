package transport

import (
	"net/http"
	"net/http/httptest"
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

func TestH2ServerServeHTTPRequiresMatch(t *testing.T) {
	s := &H2Server{Path: "/api/v1/tunnel"}
	rr := httptest.NewRecorder()
	r, _ := http.NewRequest(http.MethodConnect, "https://node/static/js", nil)
	r.URL.Path = "/static/js"
	s.ServeHTTP(rr, r)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("wrong path %d", rr.Code)
	}
}

func TestH3HandlerRejectsWrongPath(t *testing.T) {
	h := newH3Handler(NodeInfo{})
	rr := httptest.NewRecorder()
	r, _ := http.NewRequest(http.MethodConnect, "https://node/static/js", nil)
	r.URL.Path = "/static/js"
	h.ServeHTTP(rr, r)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("h3 leftover GET path %d", rr.Code)
	}
}

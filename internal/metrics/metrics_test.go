package metrics

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestWritePrometheusText(t *testing.T) {
	Reset()
	t.Cleanup(Reset)
	IncHTTP(404)
	IncHTTP(404)
	IncHTTP(413)
	IncDecoySplice()
	IncHoldPark()
	IncHoldResume()
	IncAdmit("ok")
	IncAdmit("ok_resumed")
	GETEnter()
	GETLeave(150 * time.Millisecond)
	SetLive(&Live{
		Sessions:    func() int { return 3 },
		GETSessions: func() int { return 2 },
		HoldParked:  func() int { return 1 },
	})
	var b strings.Builder
	Write(&b)
	s := b.String()
	for _, want := range []string{
		`loft_build_info{version="dev",protocol="1.0"} 1`,
		"loft_sessions 3",
		"loft_get_sessions 2",
		"loft_get_inflight 0",
		"loft_get_trip_seconds_count 1",
		`loft_http_responses_total{code="404"} 2`,
		`loft_http_responses_total{code="413"} 1`,
		"loft_decoy_splice_total 1",
		"loft_hold_park_total 1",
		"loft_hold_resume_total 1",
		"loft_hold_parked 1",
		`loft_session_admit_total{code="ok"} 1`,
		`loft_session_admit_total{code="ok_resumed"} 1`,
		"# TYPE loft_get_trip_seconds summary",
	} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in\n%s", want, s)
		}
	}
	if !strings.Contains(s, "loft_get_trip_seconds_sum 0.15") {
		t.Fatalf("trip sum:\n%s", s)
	}
}

func TestScrape(t *testing.T) {
	Reset()
	t.Cleanup(Reset)
	IncHTTP(404)
	ts := httptest.NewServer(NewHandler())
	t.Cleanup(ts.Close)
	resp, err := http.Get(ts.URL + "/metrics")
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		t.Fatalf("status %d", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.Contains(ct, "text/plain") || !strings.Contains(ct, "0.0.4") {
		t.Fatalf("content-type %q", ct)
	}
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), `loft_http_responses_total{code="404"} 1`) {
		t.Fatalf("%s", body)
	}
	miss, err := http.Get(ts.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	miss.Body.Close()
	if miss.StatusCode != 404 {
		t.Fatalf("index %d", miss.StatusCode)
	}
}

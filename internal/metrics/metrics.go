// Package metrics is a Prometheus text exposition for loft-node.
//
// The scrape listener is off until node.conf sets metrics HOST:PORT.
// It is not mounted on the public origin. No prometheus/client_golang.
package metrics

import (
	"fmt"
	"io"
	"net/http"
	"sync/atomic"
	"time"

	"github.com/a3app/loft/internal/version"
)

// DefaultAddr is the usual loopback scrape listen when metrics is enabled.
const DefaultAddr = "127.0.0.1:9101"

var (
	http404      atomic.Uint64
	http413      atomic.Uint64
	decoySplice  atomic.Uint64
	holdPark     atomic.Uint64
	holdResume   atomic.Uint64
	admitOK      atomic.Uint64
	admitResume  atomic.Uint64
	getInflight  atomic.Int64
	getTripNS    atomic.Uint64
	getTripCount atomic.Uint64
	started      = time.Now()
)

// Live is gauges read at scrape time. Nil funcs are zero.
type Live struct {
	Sessions    func() int // overlay peers
	GETSessions func() int // GET sid table
	HoldParked  func() int // mux currently parked
}

var live atomic.Pointer[Live]

// SetLive installs gauge callbacks. Pass nil to clear.
func SetLive(l *Live) { live.Store(l) }

// IncHTTP counts one origin GET answer. Only 404 and 413 are stored.
func IncHTTP(code int) {
	switch code {
	case http.StatusNotFound:
		http404.Add(1)
	case http.StatusRequestEntityTooLarge:
		http413.Add(1)
	}
}

// IncDecoySplice counts a TCP splice of a peer without a valid sid to the decoy origin.
func IncDecoySplice() { decoySplice.Add(1) }

// IncHoldPark counts a mux parked for gap_hold.
func IncHoldPark() { holdPark.Add(1) }

// IncHoldResume counts a parked mux adopted by a reconnect.
func IncHoldResume() { holdResume.Add(1) }

// IncAdmit counts Overlay Admit: "ok" or "ok_resumed".
func IncAdmit(code string) {
	switch code {
	case "ok_resumed":
		admitResume.Add(1)
	case "ok":
		admitOK.Add(1)
	}
}

// GETEnter marks one GET ServeHTTP in flight.
func GETEnter() { getInflight.Add(1) }

// GETLeave ends that GET and records service time.
func GETLeave(d time.Duration) {
	getInflight.Add(-1)
	if d < 0 {
		return
	}
	getTripNS.Add(uint64(d.Nanoseconds()))
	getTripCount.Add(1)
}

func gauge(fn func() int) int {
	if fn == nil {
		return 0
	}
	n := fn()
	if n < 0 {
		return 0
	}
	return n
}

// Write Prometheus text 0.0.4 to w.
func Write(w io.Writer) {
	l := live.Load()
	sessions, getSess, parked := 0, 0, 0
	if l != nil {
		sessions = gauge(l.Sessions)
		getSess = gauge(l.GETSessions)
		parked = gauge(l.HoldParked)
	}
	in := getInflight.Load()
	if in < 0 {
		in = 0
	}
	tripCount := getTripCount.Load()
	tripSec := float64(getTripNS.Load()) / 1e9
	fmt.Fprintf(w, "# HELP loft_build_info loft-node software and protocol.\n")
	fmt.Fprintf(w, "# TYPE loft_build_info gauge\n")
	fmt.Fprintf(w, "loft_build_info{version=%q,protocol=%q} 1\n", version.Version, version.Protocol())
	fmt.Fprintf(w, "# HELP loft_start_time_seconds Unix time when this process started.\n")
	fmt.Fprintf(w, "# TYPE loft_start_time_seconds gauge\n")
	fmt.Fprintf(w, "loft_start_time_seconds %d\n", started.Unix())
	fmt.Fprintf(w, "# HELP loft_sessions Overlay member sessions attached to this node.\n")
	fmt.Fprintf(w, "# TYPE loft_sessions gauge\n")
	fmt.Fprintf(w, "loft_sessions %d\n", sessions)
	fmt.Fprintf(w, "# HELP loft_get_sessions GET mux sessions (sid table).\n")
	fmt.Fprintf(w, "# TYPE loft_get_sessions gauge\n")
	fmt.Fprintf(w, "loft_get_sessions %d\n", getSess)
	fmt.Fprintf(w, "# HELP loft_get_inflight Outstanding GET requests being served.\n")
	fmt.Fprintf(w, "# TYPE loft_get_inflight gauge\n")
	fmt.Fprintf(w, "loft_get_inflight %d\n", in)
	fmt.Fprintf(w, "# HELP loft_get_trip_seconds GET origin service time.\n")
	fmt.Fprintf(w, "# TYPE loft_get_trip_seconds summary\n")
	fmt.Fprintf(w, "loft_get_trip_seconds_sum %.9f\n", tripSec)
	fmt.Fprintf(w, "loft_get_trip_seconds_count %d\n", tripCount)
	fmt.Fprintf(w, "# HELP loft_http_responses_total GET origin answers by code.\n")
	fmt.Fprintf(w, "# TYPE loft_http_responses_total counter\n")
	fmt.Fprintf(w, "loft_http_responses_total{code=\"404\"} %d\n", http404.Load())
	fmt.Fprintf(w, "loft_http_responses_total{code=\"413\"} %d\n", http413.Load())
	fmt.Fprintf(w, "# HELP loft_decoy_splice_total TCP splices of a peer without a valid sid to the decoy origin.\n")
	fmt.Fprintf(w, "# TYPE loft_decoy_splice_total counter\n")
	fmt.Fprintf(w, "loft_decoy_splice_total %d\n", decoySplice.Load())
	fmt.Fprintf(w, "# HELP loft_hold_park_total Mux sessions parked for gap_hold.\n")
	fmt.Fprintf(w, "# TYPE loft_hold_park_total counter\n")
	fmt.Fprintf(w, "loft_hold_park_total %d\n", holdPark.Load())
	fmt.Fprintf(w, "# HELP loft_hold_resume_total Parked mux sessions adopted by a reconnect.\n")
	fmt.Fprintf(w, "# TYPE loft_hold_resume_total counter\n")
	fmt.Fprintf(w, "loft_hold_resume_total %d\n", holdResume.Load())
	fmt.Fprintf(w, "# HELP loft_hold_parked Mux sessions currently parked.\n")
	fmt.Fprintf(w, "# TYPE loft_hold_parked gauge\n")
	fmt.Fprintf(w, "loft_hold_parked %d\n", parked)
	fmt.Fprintf(w, "# HELP loft_session_admit_total Overlay Admit results.\n")
	fmt.Fprintf(w, "# TYPE loft_session_admit_total counter\n")
	fmt.Fprintf(w, "loft_session_admit_total{code=\"ok\"} %d\n", admitOK.Load())
	fmt.Fprintf(w, "loft_session_admit_total{code=\"ok_resumed\"} %d\n", admitResume.Load())
}

// Reset zeros counters. Tests only.
func Reset() {
	http404.Store(0)
	http413.Store(0)
	decoySplice.Store(0)
	holdPark.Store(0)
	holdResume.Store(0)
	admitOK.Store(0)
	admitResume.Store(0)
	getInflight.Store(0)
	getTripNS.Store(0)
	getTripCount.Store(0)
	SetLive(nil)
}

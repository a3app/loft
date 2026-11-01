// Package cdn holds vendor edge profiles on top of protocol fronts get/ws.
// The protocol does not name Yandex or Cloudflare; those names exist only here.
//
// A CDN in front of the node only passes ordinary web traffic, and each CDN
// has its own limits (header size, request time, which methods work). A
// profile collects the numbers that keep loft under one CDN's limits, so the
// rest of the code can stay vendor-neutral. The client picks a profile with
// the cdn: field of an outbound.
package cdn

import (
	"fmt"
	"strings"
	"time"
)

// Profile is edge-specific glue on top of a protocol front (get | ws).
// The protocol does not name vendors; a profile only tweaks budgets, path
// camouflage and which front that edge can carry.
//
// HeaderBudget is the encoded Cookie size per GET and Inflight the number
// of GETs outstanding at once. Path, Ext, ContentType and FetchDest make the
// GET look like a request for a static script file, so the CDN treats it
// like any other asset. UplinkHeader is the header that carries client data.
// The Puller (see PullWait) is the one GET allowed to take frames from the
// mux at a given moment; other GETs wait their turn.
type Profile struct {
	Name         string
	Front        string // get | ws
	HeaderBudget int
	Inflight     int
	Path         string
	Ext          string
	UplinkHeader string
	ContentType  string
	FetchDest    string
	// TripTimeout is one GET after handshake. Zero is 15s. Yandex origin is 5s.
	TripTimeout time.Duration
	// PullWait is how long a non-Puller HTTP/2 GET may sit before an empty
	// 200. Zero keeps the direct default (5s). An edge that cuts origin-fetch
	// at 5s must sit less than that, or the response is a 504.
	PullWait time.Duration
}

// GenericGET is the default GET front: 16 KiB headers, inflight 8, asset-shaped path.
func GenericGET() Profile {
	return Profile{
		Name:         "generic",
		Front:        "get",
		HeaderBudget: 16 << 10,
		// Node ingestAndPull serializes mux Pull by GET seq. Inflight
		// pipelines RTT; 48 queued a 2s TTFB path until tripTimeout.
		Inflight:     8,
		Path:         "/static/v3/build",
		Ext:          ".js",
		UplinkHeader: "Cookie",
		ContentType:  "application/javascript",
		FetchDest:    "script",
	}
}

// GenericWS is a WebSocket front with no vendor header limits.
func GenericWS() Profile {
	return Profile{Name: "generic", Front: "ws"}
}

// Lookup returns a named edge profile. Empty or "generic" is GenericGET.
func Lookup(name string) (Profile, error) {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "generic":
		return GenericGET(), nil
	case "yandex":
		return Yandex(), nil
	case "cloudflare":
		return Cloudflare(), nil
	default:
		return Profile{}, fmt.Errorf("unknown cdn profile %q", name)
	}
}

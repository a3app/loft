// Package cdn holds vendor edge profiles on top of protocol fronts get/ws.
// The protocol does not name Yandex or Cloudflare; those names exist only here.
package cdn

import (
	"fmt"
	"strings"
	"time"
)

// Profile is edge-specific glue on top of a protocol front (get | ws).
// The protocol does not name vendors; a profile only tweaks budgets, path
// camouflage and which front that edge can carry.
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

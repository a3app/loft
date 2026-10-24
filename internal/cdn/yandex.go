package cdn

import "time"

// Yandex Cloud CDN edge profile: GET only, Cookie uplink.

// Yandex is the Yandex Cloud CDN edge: POST and WebSocket die (405 / ticket),
// GET bodies 413 at nginx, origin must answer within 5s.
// One-at-a-time curl accepts Cookie through 40 KiB; inflight 16 × 40 KiB
// HEADERS gets H2 GOAWAY ENHANCE_YOUR_CALM (traffic then packet loss).
// 16 KiB encoded is the concurrent budget that stays off that cliff.
// Transport remains protocol `get`; this file only pins those limits.
func Yandex() Profile {
	p := GenericGET()
	p.Name = "yandex"
	p.HeaderBudget = 16 << 10
	// Edge origin-fetch concurrency is ~10–12 HTTP/1.1 conns. Inflight 32
	// queued Cookie GETs (TTFB 900ms) and sat upload at ~3 Mbit. 16 matches
	// the pool: pad TTFB ~220ms, more payload per RTT.
	p.Inflight = 16
	// Cookie is the GET uplink (custom headers are stripped more often).
	p.UplinkHeader = "Cookie"
	// Origin fetch dies at 5s (504). Waiting 15s HOL-blocks mux on a dead GET.
	p.TripTimeout = 6 * time.Second
	// Non-Puller h2 GETs used to sit getH2PullerWait (5s). That is the
	// origin-fetch cliff: nginx 504, client give-up, new sid, streams drop.
	// 1s still lets a waiter become Puller; the slot is free before the edge cuts.
	p.PullWait = time.Second
	return p
}

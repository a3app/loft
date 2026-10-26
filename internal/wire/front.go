package wire

// Transport front names (get, ws, h2, quic, h3) and default try order.
//
// A front is one way to carry the mux over HTTPS. The inner protocol is the
// same on every front; only the packaging changes:
//   - get: all traffic rides ordinary GET requests (uplink in the Cookie
//     header, downlink in the response body), so it passes through a CDN;
//   - ws: binary WebSocket messages;
//   - h2: an HTTP/2 CONNECT stream;
//   - quic / h3: QUIC over UDP (h3 is QUIC with the HTTP/3 ALPN).

import "fmt"

// Front names on the wire and in config. get/ws/quic stay; h2 and h3 are extra.
// connect is HTTP CONNECT splice (mux-off TCP); it is not in FrontOrder.
// l3 is HTTP/2 CONNECT carrying IP datagrams; it is not in FrontOrder.
const (
	FrontGET     = "get"
	FrontWS      = "ws"
	FrontQUIC    = "quic"
	FrontH2      = "h2"
	FrontH3      = "h3"
	FrontConnect = "connect"
	FrontL3      = "l3"
)

// FrontOrder is the try-list when both sides list every transport front.
var FrontOrder = []string{FrontGET, FrontWS, FrontH2, FrontQUIC, FrontH3}

// DefaultFronts is the public path when config omits fronts: GET only.
// ws, h2, quic, h3, connect, and l3 stay off until listed.
func DefaultFronts() []string { return []string{FrontGET} }

// FrontBits is the cap mask of transport fronts (not other features).
const FrontBits = CapGET | CapWS | CapQUIC | CapH2 | CapH3 | CapConnect | CapL3

// CapForFront maps a front name to its capability bit. Unknown names are 0.
func CapForFront(f string) Cap {
	switch f {
	case FrontGET:
		return CapGET
	case FrontWS:
		return CapWS
	case FrontQUIC:
		return CapQUIC
	case FrontH2:
		return CapH2
	case FrontH3:
		return CapH3
	case FrontConnect:
		return CapConnect
	case FrontL3:
		return CapL3
	default:
		return 0
	}
}

// Fronts lists enabled transport fronts in FrontOrder, then connect and l3
// when those bits are set. connect and l3 are not in the mux try-list; they
// still have to appear here so a reconnect keeps an advertised extra front.
// A decoy node sends quic and h3 as quic_mark and h3_mark so a client that
// cannot mark its QUIC Initial does not try them. (A decoy node passes
// everything that is not loft on to a real website, so on QUIC it must be
// able to recognise a loft client from its first packet, the Initial.)
func (c Cap) Fronts() []string {
	var out []string
	for _, f := range FrontOrder {
		if c.Has(CapForFront(f)) || f == FrontQUIC && c.Has(CapQUICMark) || f == FrontH3 && c.Has(CapH3Mark) {
			out = append(out, f)
		}
	}
	for _, f := range []string{FrontConnect, FrontL3} {
		if c.Has(CapForFront(f)) {
			out = append(out, f)
		}
	}
	return out
}

// ParseFront accepts get/ws/h2/quic/h3/connect/l3. Empty is get. Vendor names are rejected.
func ParseFront(s string) (string, error) {
	switch s {
	case "", FrontGET:
		return FrontGET, nil
	case FrontWS, FrontQUIC, FrontH2, FrontH3, FrontConnect, FrontL3:
		return s, nil
	default:
		return "", fmt.Errorf("unknown front %q (want get, ws, h2, quic, h3, connect or l3)", s)
	}
}

// ParseFronts deduplicates names, preserving order. Empty list is DefaultFronts.
func ParseFronts(list []string) ([]string, error) {
	if len(list) == 0 {
		return DefaultFronts(), nil
	}
	seen := map[string]bool{}
	var out []string
	for _, s := range list {
		f, err := ParseFront(s)
		if err != nil {
			return nil, err
		}
		if seen[f] {
			continue
		}
		seen[f] = true
		out = append(out, f)
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("no fronts enabled")
	}
	return out, nil
}

// CapsWithFronts replaces FrontBits on base with the named fronts.
func CapsWithFronts(base Cap, fronts []string) Cap {
	c := base &^ FrontBits
	for _, f := range fronts {
		c |= CapForFront(f)
	}
	return c
}

// IntersectFronts keeps client order among fronts the server also lists.
func IntersectFronts(client, server []string) []string {
	ok := map[string]bool{}
	for _, f := range server {
		ok[f] = true
	}
	var out []string
	for _, f := range client {
		if ok[f] {
			out = append(out, f)
		}
	}
	return out
}

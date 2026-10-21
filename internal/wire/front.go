package wire

// Transport front names (get, ws, h2, quic, h3) and default try order.

import "fmt"

// Front names on the wire and in config. get/ws/quic stay; h2 and h3 are extra.
// connect is HTTP CONNECT splice (mux-off TCP); it is not in FrontOrder.
const (
	FrontGET     = "get"
	FrontWS      = "ws"
	FrontQUIC    = "quic"
	FrontH2      = "h2"
	FrontH3      = "h3"
	FrontConnect = "connect"
)

// FrontOrder is the default try-list when both sides enable everything.
var FrontOrder = []string{FrontGET, FrontWS, FrontH2, FrontQUIC, FrontH3}

// FrontBits is the cap mask of transport fronts (not other features).
const FrontBits = CapGET | CapWS | CapQUIC | CapH2 | CapH3 | CapConnect

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
	default:
		return 0
	}
}

// Fronts lists enabled transport fronts in FrontOrder. A decoy node sends
// quic and h3 as quic_mark and h3_mark so a client that cannot mark its
// QUIC Initial does not try them.
func (c Cap) Fronts() []string {
	var out []string
	for _, f := range FrontOrder {
		if c.Has(CapForFront(f)) || f == FrontQUIC && c.Has(CapQUICMark) || f == FrontH3 && c.Has(CapH3Mark) {
			out = append(out, f)
		}
	}
	return out
}

// ParseFront accepts get/ws/h2/quic/h3/connect. Empty is get. Vendor names are rejected.
func ParseFront(s string) (string, error) {
	switch s {
	case "", FrontGET:
		return FrontGET, nil
	case FrontWS, FrontQUIC, FrontH2, FrontH3, FrontConnect:
		return s, nil
	default:
		return "", fmt.Errorf("unknown front %q (want get, ws, h2, quic, h3 or connect)", s)
	}
}

// ParseFronts deduplicates names, preserving order. Empty list is FrontOrder.
func ParseFronts(list []string) ([]string, error) {
	if len(list) == 0 {
		return append([]string(nil), FrontOrder...), nil
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

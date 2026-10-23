package main

// GUI-identical front dial: chrome fingerprint, inflight 48, GET then WS order.

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"strings"

	"github.com/a3app/loft/internal/cdn"
	"github.com/a3app/loft/internal/config"
	"github.com/a3app/loft/internal/transport"
	"github.com/a3app/loft/internal/wire"
)

// benchDecoy and benchPBK are -decoy / -pbk. pbk makes the first hello sid v3.
var benchDecoy, benchPBK string

// outboundH2MaxAgeMS is -h2-max-age in milliseconds (0 = keep HTTP/2 until error).
var outboundH2MaxAgeMS int

// dialOrder is the single front dialFront will start. Empty follows the URL.
func dialOrder(kind, raw string) ([]string, error) {
	kind = strings.ToLower(strings.TrimSpace(kind))
	switch kind {
	case "", "auto":
		if strings.HasPrefix(raw, "ws://") || strings.HasPrefix(raw, "wss://") {
			return []string{"ws"}, nil
		}
		return []string{"get"}, nil
	default:
		f, err := wire.ParseFront(kind)
		if err != nil {
			return nil, err
		}
		return []string{f}, nil
	}
}

// dialFront dials the named front. Empty follows the URL scheme.
// h2, quic, h3 and l3 stay on that front: they are not a GET fallback.
func dialFront(ctx context.Context, token, raw, kind, path, cdnName string, muxOn bool) (transport.Dialer, string, error) {
	order, err := dialOrder(kind, raw)
	if err != nil {
		return nil, "", err
	}
	var last error
	for _, t := range order {
		o := outboundFor(raw, t, path, cdnName, muxOn)
		d, err := transport.Start(ctx, token, o)
		if err == nil {
			return d, t, nil
		}
		last = err
		fmt.Fprintf(os.Stderr, "front %s: %v\n", t, err)
	}
	if last == nil {
		last = fmt.Errorf("no front")
	}
	return nil, "", last
}

// outboundFor matches loft-front / the macOS helper.
func outboundFor(raw, kind, path, cdnName string, muxOn bool) config.Outbound {
	o := config.Outbound{
		Name:         "loft",
		Type:         kind,
		URL:          raw,
		Path:         path,
		CDN:          strings.TrimSpace(cdnName),
		Fingerprint:  "chrome",
		Insecure:     true,
		Decoy:        strings.TrimSpace(benchDecoy),
		MaskPub:      strings.TrimSpace(benchPBK),
		HeaderBudget: 16 << 10,
		Inflight:     48,
		H2MaxAgeMS:   outboundH2MaxAgeMS,
	}
	if !muxOn {
		f := false
		o.Mux = &f
	}
	if p, err := cdn.Lookup(o.CDN); err == nil && o.CDN != "" {
		o.HeaderBudget = p.HeaderBudget
		if p.Inflight > 0 {
			o.Inflight = p.Inflight
		}
		if strings.EqualFold(p.Name, "yandex") {
			o.Insecure = false
		}
	}
	switch kind {
	case "get":
		o.URL = httpOrigin(raw)
		if o.Path == "" || o.Path == "/static/js" {
			o.Path = "/static/v3/build"
		}
	case "ws":
		o.URL = withWSPath(wsURL(raw), "/api/v1/sync")
	case "h2", "h3", "l3":
		o.URL = httpOrigin(raw)
		// A leftover GET cover is not the CONNECT resource.
		if o.Path == "" || getCoverPath(o.Path) {
			o.Path = "/api/v1/tunnel"
		}
	case "quic":
		o.URL = httpOrigin(raw)
	}
	return o
}

// getCoverPath is a GET static cover. It is not an h2/h3/l3 CONNECT resource.
func getCoverPath(p string) bool {
	p = strings.TrimSpace(p)
	return strings.HasPrefix(p, "/static/") || strings.HasSuffix(p, ".js")
}

// httpOrigin maps a ws(s) URL to the matching http(s) origin.
func httpOrigin(raw string) string {
	s := strings.TrimSpace(raw)
	switch {
	case strings.HasPrefix(s, "wss://"):
		s = "https://" + strings.TrimPrefix(s, "wss://")
	case strings.HasPrefix(s, "ws://"):
		s = "http://" + strings.TrimPrefix(s, "ws://")
	}
	u, err := url.Parse(s)
	if err != nil {
		return s
	}
	host := u.Host
	if host == "" {
		host = u.Path
	}
	scheme := u.Scheme
	if scheme == "" {
		scheme = "https"
	}
	return scheme + "://" + host
}

// wsURL maps an http(s) URL to the matching ws(s) URL.
func wsURL(raw string) string {
	s := strings.TrimSpace(raw)
	if strings.HasPrefix(s, "https://") {
		return "wss://" + strings.TrimPrefix(s, "https://")
	}
	if strings.HasPrefix(s, "http://") {
		return "ws://" + strings.TrimPrefix(s, "http://")
	}
	if !strings.HasPrefix(s, "ws://") && !strings.HasPrefix(s, "wss://") {
		s = "wss://" + s
	}
	return s
}

// withWSPath puts fallback on a URL that has no path (cover is /).
func withWSPath(raw, fallback string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	if u.Path == "" || u.Path == "/" {
		u.Path = fallback
	}
	return u.String()
}

// benchConfig is the Supervise list for -switch and -type all.
func benchConfig(token, raw, path, cdnName string, muxOn bool, kinds []string) *config.ClientConfig {
	cfg := &config.ClientConfig{
		Token:  token,
		Select: "auto",
		Fronts: append([]string{}, kinds...),
	}
	for _, t := range kinds {
		cfg.Outbounds = append(cfg.Outbounds, outboundFor(raw, t, path, cdnName, muxOn))
	}
	return cfg
}

// splitTypes parses -type. all is the mux try order. Empty stays ws,get.
func splitTypes(s string) []string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "all" {
		return append([]string{}, wire.FrontOrder...)
	}
	seen := map[string]bool{}
	var out []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		f, err := wire.ParseFront(p)
		if err != nil || seen[f] {
			continue
		}
		seen[f] = true
		out = append(out, f)
	}
	if len(out) == 0 {
		return []string{"ws", "get"}
	}
	return out
}

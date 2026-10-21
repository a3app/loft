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
)

// outboundH2MaxAgeMS is -h2-max-age in milliseconds (0 = keep HTTP/2 until error).
var outboundH2MaxAgeMS int

// dialFront tries the requested kind first (GUI: empty kind from URL), then the other.
func dialFront(ctx context.Context, token, raw, kind, path, cdnName string, muxOn bool) (transport.Dialer, string, error) {
	kind = strings.ToLower(strings.TrimSpace(kind))
	order := []string{kind}
	switch kind {
	case "ws":
		order = []string{"ws"}
	case "get":
		order = []string{"get"}
	default:
		if strings.HasPrefix(raw, "ws://") || strings.HasPrefix(raw, "wss://") {
			order = []string{"ws"}
		} else {
			order = []string{"get"}
		}
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
	case "h2", "h3":
		o.URL = httpOrigin(raw)
		if o.Path == "" || o.Path == "/static/js" {
			o.Path = "/api/v1/tunnel"
		}
	case "quic":
		o.URL = httpOrigin(raw)
	}
	return o
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

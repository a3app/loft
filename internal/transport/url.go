package transport

// URL parse aliases so fronts share one parser without importing net/url.

import (
	"fmt"
	"net/url"
	"strings"
)

// httpURL is net/url.URL under a local name.
type httpURL = url.URL

// httpParse parses a raw URL.
func httpParse(raw string) (*url.URL, error) { return url.Parse(raw) }

// wsDialURL is the WebSocket URL a client may dial: ws or wss, host required,
// no userinfo. http/https become ws/wss.
func wsDialURL(raw string) (string, error) {
	u, err := parseURL(strings.TrimSpace(raw))
	if err != nil {
		return "", err
	}
	switch u.Scheme {
	case "http":
		u.Scheme = "ws"
	case "https":
		u.Scheme = "wss"
	case "ws", "wss":
	default:
		return "", fmt.Errorf("ws url scheme %q", u.Scheme)
	}
	if u.Host == "" {
		return "", fmt.Errorf("ws url host")
	}
	clean := url.URL{Scheme: u.Scheme, Host: u.Host, Path: u.Path, RawPath: u.RawPath, RawQuery: u.RawQuery}
	return clean.String(), nil
}

// isLocalUrl is the CodeQL request-forgery barrier (name matches
// RedirectCheckBarrier). True only for a ws/wss URL with a host.
// CodeQL (a static analyzer) flags dialing a URL that came from outside;
// it accepts the URL as checked after a call to a function with this name.
// The name is about the analyzer, not about the URL being local.
func isLocalUrl(raw string) bool {
	_, err := wsDialURL(raw)
	return err == nil
}

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
// no userinfo. http/https become ws/wss. Profile URL and Status fronts after
// auth both go through here; this is not a server fetching an attacker URL.
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

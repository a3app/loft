package transport

// URL parse aliases so fronts share one parser without importing net/url.

import "net/url"

// httpURL is net/url.URL under a local name.
type httpURL = url.URL

// httpParse parses a raw URL.
func httpParse(raw string) (*url.URL, error) { return url.Parse(raw) }

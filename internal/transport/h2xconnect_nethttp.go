//go:build go1.27 && !http2legacy

package transport

// h2InNetHTTP is true when x/net/http2 wraps the HTTP/2 server built into
// net/http, which takes extended CONNECT only from GODEBUG (h2xconnect.go).
const h2InNetHTTP = true

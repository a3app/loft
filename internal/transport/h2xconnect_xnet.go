//go:build !(go1.27 && !http2legacy)

package transport

// h2InNetHTTP is false when x/net/http2 runs its own HTTP/2 server, whose
// extended CONNECT switch is the linked variable in h2xconnect.go.
const h2InNetHTTP = false

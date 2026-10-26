package transport

import _ "unsafe"

// http2DisableXConnect is x/net/http2.disableExtendedConnectProtocol.
// CONNECT to /api/v1/tunnel needs SETTINGS_ENABLE_CONNECT_PROTOCOL (RFC 8441).
// x/net keeps that switch in an unexported variable, so go:linkname reaches
// it directly; there is no public option to set.
//
//go:linkname http2DisableXConnect golang.org/x/net/http2.disableExtendedConnectProtocol
var http2DisableXConnect bool

// EnableHTTP2ExtendedConnect lets the node advertise RFC 8441 CONNECT.
func EnableHTTP2ExtendedConnect() {
	http2DisableXConnect = false
}

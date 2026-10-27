package transport

// Extended CONNECT (RFC 8441) for the node's HTTP/2 server.
//
// The h2 and l3 fronts open their tunnel with an HTTP/2 CONNECT that carries
// a :protocol header. A server may accept that only after it announced
// SETTINGS_ENABLE_CONNECT_PROTOCOL, and Go's HTTP/2 server keeps that off
// by default (browsers would otherwise try WebSockets over HTTP/2, Go issue
// 71128). Without it the node answers such a CONNECT with RST_STREAM
// PROTOCOL_ERROR and both fronts fail.
//
// Where the switch lives depends on the Go version:
//
//   - Up to Go 1.26 (or any Go with the build tag http2legacy) the server is
//     the one in golang.org/x/net/http2. It keeps the switch in an unexported
//     variable, which go:linkname below reaches directly.
//   - From Go 1.27 x/net/http2 only wraps the server built into net/http
//     (x/net's server_wrap.go). That server reads the switch once, while the
//     program starts, from the environment variable GODEBUG
//     (http2xconnect=1). Nothing inside the running program can change it
//     afterwards, and a //go:debug line does not help either because the
//     value is read with os.Getenv, not through Go's godebug settings. So
//     EnsureHTTP2ExtendedConnect starts the program again with that
//     variable set.

import (
	"os"
	"strings"
	_ "unsafe"
)

// http2DisableXConnect is x/net/http2.disableExtendedConnectProtocol. It
// still exists when x/net wraps net/http, but then nothing reads it.
//
//go:linkname http2DisableXConnect golang.org/x/net/http2.disableExtendedConnectProtocol
var http2DisableXConnect bool

// EnableHTTP2ExtendedConnect turns on extended CONNECT in x/net's own HTTP/2
// server (Go up to 1.26, or the http2legacy build tag). On newer Go it has
// no effect by itself; programs call EnsureHTTP2ExtendedConnect instead.
func EnableHTTP2ExtendedConnect() {
	http2DisableXConnect = false
}

// godebugXConnect is the GODEBUG setting net/http's HTTP/2 server looks for.
const godebugXConnect = "http2xconnect=1"

// EnsureHTTP2ExtendedConnect makes sure this process serves extended
// CONNECT, whatever Go built it. A server program (loft-node) calls it as
// the first thing in main, before it starts any goroutine or opens files.
//
// With x/net's own server it just flips the switch. With the server in
// net/http (Go 1.27+) and GODEBUG not yet carrying http2xconnect, it
// replaces the process with a fresh copy of itself (same executable, same
// arguments, same process id, so systemd sees nothing) whose environment
// adds http2xconnect=1. The copy finds the setting and goes on normally, so
// this happens at most once. If the operator set http2xconnect to anything
// in GODEBUG, that choice is kept. It returns an error only when the restart
// was needed and failed; then the h2 and l3 fronts will not work.
func EnsureHTTP2ExtendedConnect() error {
	EnableHTTP2ExtendedConnect()
	if !h2InNetHTTP {
		return nil
	}
	env, changed := withXConnect(os.Environ())
	if !changed {
		return nil
	}
	return reexec(env)
}

// withXConnect returns env with http2xconnect=1 added to GODEBUG (a comma
// list of key=value), and whether it changed anything. An existing
// http2xconnect entry, whatever its value, is left as it is.
func withXConnect(env []string) ([]string, bool) {
	out := make([]string, 0, len(env)+1)
	found := false
	changed := false
	for _, kv := range env {
		v, ok := strings.CutPrefix(kv, "GODEBUG=")
		if !ok {
			out = append(out, kv)
			continue
		}
		found = true
		if strings.Contains(v, "http2xconnect=") {
			out = append(out, kv)
			continue
		}
		if strings.TrimSpace(v) == "" {
			v = godebugXConnect
		} else {
			v += "," + godebugXConnect
		}
		out = append(out, "GODEBUG="+v)
		changed = true
	}
	if !found {
		out = append(out, "GODEBUG="+godebugXConnect)
		changed = true
	}
	return out, changed
}

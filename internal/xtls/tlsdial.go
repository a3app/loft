package xtls

// Browser ClientHello for an ordinary TLS session (no REALITY).
// Xray share links set fp=firefox and alpn=h2 on XHTTP. crypto/tls would
// send a Go hello, and a server that only offers h2 would still work, but
// the fingerprint the link names would not. uTLS keeps the browser hello;
// its parrot ALPN is then replaced with the list the link (or XHTTP) asks
// for, so a h2-only server is offered h2.

import (
	"crypto/tls"
	"fmt"
	"net"
	"strings"

	utls "github.com/refraction-networking/utls"
)

// DialTLS handshakes raw. A browser fp uses that ClientHello. Empty fp
// and fp=go stay on crypto/tls. alpn, when set, must be the protocol the
// server picks (XHTTP needs h2).
func DialTLS(raw net.Conn, sni, fp string, alpn []string, insecure bool) (net.Conn, error) {
	if fp != "" && !strings.EqualFold(fp, "go") {
		return dialFingerprint(raw, sni, fp, alpn, insecure)
	}
	cfg := &tls.Config{
		ServerName:         sni,
		InsecureSkipVerify: insecure,
		MinVersion:         tls.VersionTLS12,
		NextProtos:         alpn,
	}
	tc := tls.Client(raw, cfg)
	if err := tc.Handshake(); err != nil {
		raw.Close()
		return nil, err
	}
	return tc, nil
}

func dialFingerprint(raw net.Conn, sni, fp string, alpn []string, insecure bool) (net.Conn, error) {
	cfg := &utls.Config{
		ServerName:         sni,
		InsecureSkipVerify: insecure,
		MinVersion:         utls.VersionTLS12,
		NextProtos:         alpn,
	}
	u := utls.UClient(raw, cfg, HelloID(fp))
	if len(alpn) > 0 {
		if err := applyALPN(u, alpn); err != nil {
			raw.Close()
			return nil, err
		}
	}
	if err := u.Handshake(); err != nil {
		raw.Close()
		return nil, err
	}
	if len(alpn) > 0 && !alpnHas(alpn, u.ConnectionState().NegotiatedProtocol) {
		u.Close()
		return nil, fmt.Errorf("tls: alpn %q", u.ConnectionState().NegotiatedProtocol)
	}
	return u, nil
}

// applyALPN rebuilds the uTLS ClientHello so its ALPN extension is the
// list the share link (or XHTTP) asked for. The parrot hello already has
// an ALPN extension; that slice is replaced. A fingerprint without one
// gets the extension appended.
func applyALPN(u *utls.UConn, alpn []string) error {
	if err := u.BuildHandshakeState(); err != nil {
		return err
	}
	want := append([]string(nil), alpn...)
	patched := false
	for _, ext := range u.Extensions {
		if a, ok := ext.(*utls.ALPNExtension); ok {
			a.AlpnProtocols = want
			patched = true
			break
		}
	}
	if !patched {
		u.Extensions = append(u.Extensions, &utls.ALPNExtension{AlpnProtocols: want})
	}
	return u.BuildHandshakeState()
}

func alpnHas(list []string, got string) bool {
	for _, w := range list {
		if w == got {
			return true
		}
	}
	return false
}

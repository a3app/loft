package transport

// uTLS dial, session_id inject, ClientHello split, and HTTP/2 client.

import (
	"crypto/ecdh"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"
	"time"

	utls "github.com/refraction-networking/utls"
	"golang.org/x/net/http2"

	"github.com/a3app/loft/internal/config"
	"github.com/a3app/loft/internal/protect"
)

// tlsToken is the secret for mask + session_id HMAC. A CDN hop is ordinary
// TLS to the edge: no loft ClientHello, no borrowed-cert mask.
func tlsToken(o config.Outbound, token []byte) []byte {
	if strings.TrimSpace(o.CDN) != "" {
		return nil
	}
	return token
}

// helloID maps a fingerprint name to a uTLS ClientHelloID (default Chrome).
func helloID(fp string) utls.ClientHelloID {
	switch fp {
	case "firefox":
		return utls.HelloFirefox_Auto
	case "go":
		return utls.HelloGolang
	default:
		return utls.HelloChrome_Auto
	}
}

// tlsSess is the uTLS session cache.
var tlsSess = utls.NewLRUClientSessionCache(64)

// tcpBuf is the front/origin socket buffer. 256 KiB is ~16 Mbit of BDP at 128 ms;
// 512 KiB–2 MiB queued a 0.5–2 s ping under load (speedtest loaded latency).
const tcpBuf = 256 << 10

// tuneTCP turns on TCP_NODELAY and grows the kernel buffers on the TCP conn
// under c (TLS, prefixConn, and other wrappers are unwrapped).
func tuneTCP(c net.Conn) {
	for i := 0; i < 8 && c != nil; i++ {
		if t, ok := c.(*net.TCPConn); ok {
			_ = t.SetNoDelay(true)
			_ = t.SetReadBuffer(tcpBuf)
			_ = t.SetWriteBuffer(tcpBuf)
			return
		}
		switch u := c.(type) {
		case *prefixConn:
			c = u.Conn
		case interface{ NetConn() net.Conn }:
			c = u.NetConn()
		default:
			return
		}
	}
}

// tcpTuneListener calls tuneTCP on every Accept.
type tcpTuneListener struct{ net.Listener }

// Accept returns the next conn with TCP_NODELAY and large buffers.
func (l tcpTuneListener) Accept() (net.Conn, error) {
	c, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	tuneTCP(c)
	return c, nil
}

// TuneListener wraps ln so Accept sets TCP_NODELAY and 2 MiB buffers.
func TuneListener(ln net.Listener) net.Listener {
	if ln == nil {
		return nil
	}
	return tcpTuneListener{ln}
}

// dialTLS dials TCP, injects session HMAC, and completes uTLS. Mask is tried when advertised.
func dialTLS(addr, sni, fingerprint string, insecure bool, token []byte) (net.Conn, error) {
	return dialTLSALPN(addr, sni, fingerprint, insecure, token, []string{"h2", "http/1.1"})
}

// dialTLSALPN is dialTLS with an explicit ALPN list. WebSocket must offer
// only http/1.1: the node prefers h2, and an h2 socket cannot HTTP Upgrade.
func dialTLSALPN(addr, sni, fingerprint string, insecure bool, token []byte, alpn []string) (net.Conn, error) {
	if len(alpn) == 0 {
		alpn = []string{"h2", "http/1.1"}
	}
	if len(token) > 0 && maskOnDecoySNI(addr, sni) {
		return dialMaskALPN(addr, sni, fingerprint, token, CachedMaskPub(addr), alpn)
	}
	raw, err := protect.Timeout("tcp4", addr, 10*time.Second)
	if err != nil {
		return nil, err
	}
	tuneTCP(raw)
	pol := CachedCanary(addr)
	if pol.Fat && fingerprint == "go" {
		fingerprint = "chrome"
	}
	if pol.Split {
		raw = &splitHelloConn{Conn: raw, at: 1}
	}
	cfg := &utls.Config{
		ServerName:         sni,
		InsecureSkipVerify: insecure,
		NextProtos:         append([]string(nil), alpn...),
		ClientSessionCache: tlsSess,
	}
	u := utls.UClient(raw, cfg, helloID(fingerprint))
	if err := finishClientHello(u, token, sni, alpn, CachedMaskPub(addr)); err != nil {
		raw.Close()
		return nil, err
	}
	if err := u.Handshake(); err != nil {
		raw.Close()
		if maskHandshake(err) && maskOnDecoySNI(addr, sni) {
			return dialMaskALPN(addr, sni, fingerprint, token, CachedMaskPub(addr), alpn)
		}
		return nil, err
	}
	return u, nil
}

// maskHandshake reports whether err looks like a mask CertificateVerify failure.
func maskHandshake(err error) bool {
	if err == nil {
		return false
	}
	s := err.Error()
	return strings.Contains(s, "Ed25519") ||
		strings.Contains(s, "invalid signature") ||
		strings.Contains(s, "certificate verify") ||
		strings.Contains(s, "expected an Ed25519") ||
		strings.Contains(s, "x509:") ||
		strings.Contains(s, "tls: bad certificate")
}

// finishClientHello applies ALPN and session_id (sid v3 when maskPub is
// known, else sid v2), then re-marshals the hello.
func finishClientHello(u *utls.UConn, token []byte, sni string, alpn []string, maskPub []byte) error {
	if err := u.BuildHandshakeState(); err != nil {
		return err
	}
	if len(alpn) > 0 {
		for _, ext := range u.Extensions {
			if a, ok := ext.(*utls.ALPNExtension); ok {
				a.AlpnProtocols = append([]string(nil), alpn...)
			}
		}
		u.HandshakeState.Hello.AlpnProtocols = append([]string(nil), alpn...)
	}
	if len(token) > 0 {
		rnd := u.HandshakeState.Hello.Random
		if len(rnd) == 32 {
			share := utlsShare(u)
			h := clientHello{Random: rnd, SNI: sni, KeyShare: share}
			var sid []byte
			if len(maskPub) == 32 {
				if priv := utlsSharePriv(u, share); len(priv) == 32 {
					sid = sid3FromPriv(priv, maskPub, h)
				}
			}
			if len(sid) != 32 {
				sid = sessionHMAC(token, h)
			}
			u.HandshakeState.Hello.SessionId = sid
		}
	}
	return u.MarshalClientHello()
}

// buildUTLSHello marshals a browser ClientHello (no record header) and the
// X25519 private that matches key_share. session_id is sid v3 when maskPub
// is 32 bytes, else sid v2 under token.
func buildUTLSHello(sni, fingerprint string, token, maskPub []byte, alpn []string) (msg, priv []byte, err error) {
	if len(alpn) == 0 {
		alpn = []string{"h2", "http/1.1"}
	}
	p1, p2 := net.Pipe()
	defer p1.Close()
	defer p2.Close()
	go io.Copy(io.Discard, p2)
	cfg := &utls.Config{
		ServerName:         sni,
		InsecureSkipVerify: true,
		NextProtos:         append([]string(nil), alpn...),
	}
	u := utls.UClient(p1, cfg, helloID(fingerprint))
	if err := u.BuildHandshakeState(); err != nil {
		return nil, nil, err
	}
	if len(alpn) > 0 {
		for _, ext := range u.Extensions {
			if a, ok := ext.(*utls.ALPNExtension); ok {
				a.AlpnProtocols = append([]string(nil), alpn...)
			}
		}
		u.HandshakeState.Hello.AlpnProtocols = append([]string(nil), alpn...)
	}
	rnd := u.HandshakeState.Hello.Random
	share := utlsShare(u)
	priv = utlsSharePriv(u, share)
	h := clientHello{Random: rnd, SNI: sni, KeyShare: share}
	var sid []byte
	if len(maskPub) == 32 && len(priv) == 32 {
		sid = sid3FromPriv(priv, maskPub, h)
	}
	if len(sid) != 32 && len(token) > 0 && len(rnd) == 32 {
		sid = sessionHMAC(token, h)
	}
	if len(sid) == 32 {
		u.HandshakeState.Hello.SessionId = sid
	}
	if err := u.MarshalClientHello(); err != nil {
		return nil, nil, err
	}
	raw := u.HandshakeState.Hello.Raw
	if len(raw) >= 5 && raw[0] == 0x16 {
		raw = raw[5:]
	}
	if len(raw) < 4 || raw[0] != hsClientHello {
		return nil, nil, fmt.Errorf("mask: utls hello")
	}
	if len(priv) != 32 {
		parsed, perr := parseClientHello(raw)
		if perr == nil {
			priv = utlsSharePriv(u, parsed.KeyShare)
		}
	}
	return raw, priv, nil
}

// utlsShare returns the X25519 public from uTLS key_share, or nil.
func utlsShare(u *utls.UConn) []byte {
	ks := u.HandshakeState.State13.KeyShareKeys
	if ks == nil {
		return nil
	}
	if ks.Ecdhe != nil {
		b := ks.Ecdhe.PublicKey().Bytes()
		if len(b) == 32 {
			return b
		}
	}
	if ks.MlkemEcdhe != nil {
		b := ks.MlkemEcdhe.PublicKey().Bytes()
		if len(b) == 32 {
			return b
		}
	}
	return nil
}

// utlsSharePriv returns the X25519 private whose public equals pub.
func utlsSharePriv(u *utls.UConn, pub []byte) []byte {
	ks := u.HandshakeState.State13.KeyShareKeys
	if ks == nil {
		return nil
	}
	try := func(k *ecdh.PrivateKey) []byte {
		if k == nil {
			return nil
		}
		got := k.PublicKey().Bytes()
		if len(pub) == 0 || bytesEqual(got, pub) {
			if k.Curve() == ecdh.X25519() || len(got) == 32 {
				return k.Bytes()
			}
		}
		return nil
	}
	if p := try(ks.Ecdhe); len(p) == 32 {
		return p
	}
	return try(ks.MlkemEcdhe)
}

func bytesEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// splitHelloConn writes the first TLS handshake record as two records so a
// middlebox must reassemble ClientHello. Optional; server readClientHello concatenates.
type splitHelloConn struct {
	net.Conn
	at   int
	used bool
}

// Write splits the first handshake record at s.at, then writes later records whole.
func (s *splitHelloConn) Write(p []byte) (int, error) {
	if s.used || s.at <= 0 || len(p) < 8 || p[0] != 0x16 {
		return s.Conn.Write(p)
	}
	s.used = true
	payload := p[5:]
	if s.at >= len(payload) {
		return s.Conn.Write(p)
	}
	if err := writeTLSRecord(s.Conn, 0x16, payload[:s.at]); err != nil {
		return 0, err
	}
	if err := writeTLSRecord(s.Conn, 0x16, payload[s.at:]); err != nil {
		return 0, err
	}
	return len(p), nil
}

// httpClient is the HTTP/2 client GET uses. ReadIdleTimeout is 0 so idle ping cannot kill the conn.
func httpClient(o config.Outbound, token string) (*http.Client, error) {
	u, err := parseURL(o.URL)
	if err != nil {
		return nil, err
	}
	if u.Scheme == "http" {
		return &http.Client{Timeout: 0, Transport: &http.Transport{
			ForceAttemptHTTP2:   true,
			MaxIdleConns:        64,
			MaxIdleConnsPerHost: 32,
			MaxConnsPerHost:     32,
		}}, nil
	}
	addr, sni, insecure, err := resolveTLS(o)
	if err != nil {
		return nil, err
	}
	tok := tlsToken(o, []byte(token))
	idlePing := time.Duration(0)
	if strings.EqualFold(strings.TrimSpace(o.CDN), "yandex") {
		// Direct GET: 5s ping killed a 1s-RTT h2 conn (TLS hole on retry).
		// Yandex CDN GOAWAY/RST after a quiet or long-lived h2 conn; a 20s
		// ping keeps the session off that cliff without the 5s false death.
		idlePing = 20 * time.Second
	}
	t2 := &http2.Transport{
		AllowHTTP:        false,
		ReadIdleTimeout:  idlePing,
		MaxReadFrameSize: 1 << 20,
		DialTLS: func(network, _ string, _ *tls.Config) (net.Conn, error) {
			return dialTLS(addr, sni, o.Fingerprint, insecure, tok)
		},
	}
	return &http.Client{Transport: t2}, nil
}

// parseURL parses a front URL. Empty raw is an error.
func parseURL(raw string) (*httpURL, error) {
	if raw == "" {
		return nil, fmt.Errorf("empty url")
	}
	u, err := httpParse(raw)
	if err != nil {
		return nil, err
	}
	return u, nil
}

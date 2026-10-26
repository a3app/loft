// Package xtls holds the Xray transports shared by the VLESS and Trojan
// clients: REALITY and XHTTP.
//
// REALITY is a TLS disguise. The client connects with a ClientHello copied
// from a real browser, naming some popular site as the SNI. The server
// passes connections it does not recognise to that real site, so a
// scanner sees the genuine site and its genuine certificate. Our client
// hides an authentication tag in the ClientHello's session ID, sealed with
// a key from X25519 between our key share and the server's public key (the
// pbk of the share link). The server recognises it and answers itself,
// with a throwaway certificate signed with a value derived from the same
// key, which the client checks instead of a CA chain.
//
// XHTTP (formerly SplitHTTP) carries the proxy stream in the body of an
// HTTP/2 POST, for servers that sit behind an HTTP front.
package xtls

// REALITY TLS client (Xray transport/internet/reality). Shared by VLESS and Trojan.

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ed25519"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
	"crypto/x509"
	"encoding/binary"
	"fmt"
	"net"
	"strings"
	"time"

	utls "github.com/refraction-networking/utls"
)

const (
	realityInfo = "REALITY"
	// Session-id prefix: Xray core version bytes. Server decrypts them; it
	// does not require an exact match.
	realityVerX, realityVerY, realityVerZ byte = 25, 9, 11
)

// Reality is the share-URI fields a REALITY handshake needs: the
// disguise SNI, the browser fingerprint, the server's X25519 public key and
// the short ID (a per-client value the server checks).
type Reality struct {
	SNI         string
	Fingerprint string
	PublicKey   string
	ShortID     string
}

type realityConn struct {
	*utls.UConn
	authKey  []byte
	verified bool
}

// verify accepts the server certificate only if it is an Ed25519
// certificate whose signature field is HMAC-SHA512(authKey, public key).
// Only a server that knows the private key behind pbk can make that; a
// real site's certificate fails, which means the server did not know us
// and passed the connection to the real site.
func (c *realityConn) verify(rawCerts [][]byte, _ [][]*x509.Certificate) error {
	if len(rawCerts) == 0 {
		return fmt.Errorf("reality: no certificate")
	}
	cert, err := x509.ParseCertificate(rawCerts[0])
	if err != nil {
		return err
	}
	pub, ok := cert.PublicKey.(ed25519.PublicKey)
	if ok {
		mac := hmac.New(sha512.New, c.authKey)
		mac.Write(pub)
		if hmac.Equal(mac.Sum(nil), cert.Signature) {
			c.verified = true
			return nil
		}
	}
	return fmt.Errorf("reality: dest certificate")
}

// DialReality wraps raw in a uTLS REALITY handshake.
// It builds the browser-like ClientHello, then rewrites its 32-byte
// session ID: Xray version bytes, the Unix time, the short ID, all
// encrypted with AES-GCM under the auth key (HKDF of the X25519 secret,
// salted with the first 20 bytes of the hello random; the other 12 bytes
// are the nonce, the whole hello the additional data). With insecure set a
// certificate that fails verify is still accepted.
func DialReality(raw net.Conn, r Reality, insecure bool) (net.Conn, error) {
	pbk, err := DecodePbk(r.PublicKey)
	if err != nil {
		raw.Close()
		return nil, err
	}
	sid, err := DecodeSid(r.ShortID)
	if err != nil {
		raw.Close()
		return nil, err
	}
	serverPub, err := ecdh.X25519().NewPublicKey(pbk)
	if err != nil {
		raw.Close()
		return nil, fmt.Errorf("reality: pbk")
	}
	rc := &realityConn{}
	cfg := &utls.Config{
		ServerName:             r.SNI,
		InsecureSkipVerify:     true,
		SessionTicketsDisabled: true,
		VerifyPeerCertificate:  rc.verify,
	}
	u := utls.UClient(raw, cfg, HelloID(r.Fingerprint))
	rc.UConn = u
	if err := u.BuildHandshakeState(); err != nil {
		raw.Close()
		return nil, err
	}
	hello := u.HandshakeState.Hello
	if hello == nil || len(hello.Random) < 32 || len(hello.Raw) < 71 {
		raw.Close()
		return nil, fmt.Errorf("reality: hello")
	}
	hello.SessionId = make([]byte, 32)
	copy(hello.Raw[39:], hello.SessionId)
	hello.SessionId[0] = realityVerX
	hello.SessionId[1] = realityVerY
	hello.SessionId[2] = realityVerZ
	binary.BigEndian.PutUint32(hello.SessionId[4:8], uint32(time.Now().Unix()))
	copy(hello.SessionId[8:], sid)
	ecdhe := realityECDHE(u)
	if ecdhe == nil {
		raw.Close()
		return nil, fmt.Errorf("reality: fingerprint")
	}
	shared, err := ecdhe.ECDH(serverPub)
	if err != nil {
		raw.Close()
		return nil, fmt.Errorf("reality: ecdh")
	}
	auth, err := hkdf.Key(sha256.New, shared, hello.Random[:20], realityInfo, 32)
	if err != nil {
		raw.Close()
		return nil, err
	}
	rc.authKey = auth
	block, err := aes.NewCipher(auth)
	if err != nil {
		raw.Close()
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		raw.Close()
		return nil, err
	}
	aead.Seal(hello.SessionId[:0], hello.Random[20:], hello.SessionId[:16], hello.Raw)
	copy(hello.Raw[39:], hello.SessionId)
	if err := u.Handshake(); err != nil {
		raw.Close()
		return nil, err
	}
	if !rc.verified && !insecure {
		raw.Close()
		return nil, fmt.Errorf("reality: dest certificate")
	}
	return rc, nil
}

// realityECDHE is our X25519 key from the hello's key share. With a
// post-quantum hybrid share (ML-KEM + X25519) it is the X25519 half. nil
// means the chosen fingerprint sends no X25519 share.
func realityECDHE(u *utls.UConn) *ecdh.PrivateKey {
	ks := u.HandshakeState.State13.KeyShareKeys
	if ks == nil {
		return nil
	}
	if ks.Ecdhe != nil {
		return ks.Ecdhe
	}
	return ks.MlkemEcdhe
}

// HelloID maps a share-URI fp= value to a uTLS ClientHello.
// Unknown or empty values get Chrome, the most common browser.
func HelloID(fp string) utls.ClientHelloID {
	switch strings.ToLower(strings.TrimSpace(fp)) {
	case "firefox":
		return utls.HelloFirefox_Auto
	case "safari":
		return utls.HelloSafari_Auto
	case "ios":
		return utls.HelloIOS_Auto
	case "edge":
		return utls.HelloEdge_Auto
	case "go":
		return utls.HelloGolang
	case "random", "randomized":
		return utls.HelloRandomized
	default:
		return utls.HelloChrome_Auto
	}
}

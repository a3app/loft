package transport

// Borrowed-cert mask: loft TLS presents the origin certificate; probes still splice.

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/binary"
	"errors"
	"fmt"
	"hash"
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/a3app/loft/internal/protect"

	"golang.org/x/crypto/cryptobyte"
	"golang.org/x/crypto/curve25519"
)

// TLS 1.3 record, handshake, group, and signature numbers used by mask.
const (
	tls13AESGCM         uint16 = 0x1301
	tls13ChaCha         uint16 = 0x1303
	x25519Group         uint16 = 0x001d
	sigEd25519          uint16 = 0x0807
	recChangeCipher            = 20
	recAlert                   = 21
	recHandshake               = 22
	recApp                     = 23
	hsClientHello              = 1
	hsServerHello              = 2
	hsEncryptedExt             = 8
	hsCertificate              = 11
	hsCertificateVerify        = 15
	hsFinished                 = 20
)

// lastKEX is the group of the latest client handshake ("tls" or a mask group).
var lastKEX atomic.Value

// noteKEX records the handshake group for bench and drive logs.
func noteKEX(name string) {
	lastKEX.Store(name)
	if name == "" {
		return
	}
	if d := driveLog.Load(); d != nil {
		d.Event("KEX", "group="+name)
	}
}

// LastKEX is the group recorded by the latest successful client handshake.
func LastKEX() string {
	s, _ := lastKEX.Load().(string)
	return s
}

// Mask is decoy mask: loft TLS wears a dest-shaped ECDSA leaf. Probes still splice.
type Mask struct {
	xpriv  []byte // 32-byte X25519 static private
	xpub   []byte // 32-byte X25519 static public
	origin string
	sni    string
	mu     sync.RWMutex
	certs  [][]byte
	alpn   string
	pads   []int
	ready  atomic.Bool
	stop   chan struct{}
}

// NewMask derives the node X25519 mask key from token and starts origin-cert refresh.
func NewMask(token []byte, origin string) *Mask {
	priv := maskX25519Priv(token)
	pub, err := curve25519.X25519(priv, curve25519.Basepoint)
	if err != nil {
		pub = make([]byte, 32)
	}
	host, addr := splitDecoy(origin)
	m := &Mask{
		xpriv:  priv,
		xpub:   pub,
		origin: addr,
		sni:    host,
		stop:   make(chan struct{}),
	}
	go m.loop()
	return m
}

// maskX25519Priv is HKDF-SHA256(token) → 32-byte X25519 scalar.
func maskX25519Priv(token []byte) []byte {
	s, err := hkdf.Key(sha256.New, token, nil, "loft-mask-x25519", 32)
	if err != nil {
		out := make([]byte, 32)
		copy(out, token)
		return out
	}
	return s
}

// maskSeed is the X25519 private; kept for tests that mint a Mask by hand.
func maskSeed(token []byte) []byte { return maskX25519Priv(token) }

// Public returns a copy of the 32-byte X25519 mask public key.
func (m *Mask) Public() []byte { return append([]byte(nil), m.xpub...) }

// NodeMaskPub is the X25519 mask public a client uses as sid v3 / serial bind.
func NodeMaskPub(token []byte) []byte {
	if len(token) == 0 {
		return nil
	}
	pub, err := curve25519.X25519(maskX25519Priv(token), curve25519.Basepoint)
	if err != nil {
		return nil
	}
	return pub
}

// Ready is true after the origin certificate has been fetched.
func (m *Mask) Ready() bool { return m.ready.Load() }

// Close stops origin-cert refresh.
func (m *Mask) Close() {
	select {
	case <-m.stop:
	default:
		close(m.stop)
	}
}

// loop refreshes the origin certificate on start and every 6 hours.
func (m *Mask) loop() {
	_ = m.refresh()
	t := time.NewTicker(6 * time.Hour)
	defer t.Stop()
	for {
		select {
		case <-m.stop:
			return
		case <-t.C:
			_ = m.refresh()
		}
	}
}

// refresh dials the origin, stores its certificate chain and ALPN, and
// samples application-record lengths for dest-shaped padding.
func (m *Mask) refresh() error {
	raw, err := net.DialTimeout("tcp", m.origin, 8*time.Second)
	if err != nil {
		return err
	}
	_ = raw.SetDeadline(time.Now().Add(8 * time.Second))
	spy := &recordCollector{Conn: raw}
	cfg := &tls.Config{
		ServerName: m.sni,
		// Dest-shaped TLS: trust is lt_ / pbk. codeql[go/disabled-certificate-check]
		InsecureSkipVerify: true,
		NextProtos:         []string{"h2", "http/1.1"},
		MinVersion:         tls.VersionTLS12,
	}
	c := tls.Client(spy, cfg)
	if err := c.Handshake(); err != nil {
		_ = raw.Close()
		return err
	}
	st := c.ConnectionState()
	_, _ = io.WriteString(c, "GET / HTTP/1.1\r\nHost: "+m.sni+"\r\nConnection: close\r\n\r\n")
	drainHTTP(c)
	_ = c.Close()
	var certs [][]byte
	for _, p := range st.PeerCertificates {
		certs = append(certs, append([]byte(nil), p.Raw...))
	}
	if len(certs) == 0 {
		return errors.New("mask: origin sent no certificate")
	}
	alpn := st.NegotiatedProtocol
	if alpn == "" {
		alpn = "h2"
	}
	pads := append([]int(nil), spy.appLens...)
	m.mu.Lock()
	m.certs = certs
	m.alpn = alpn
	m.pads = pads
	m.mu.Unlock()
	m.ready.Store(true)
	return nil
}

// snapshot copies the current origin certs, ALPN, and record-length pattern.
func (m *Mask) snapshot() (certs [][]byte, alpn string, pads []int) {
	m.mu.RLock()
	defer m.mu.RUnlock()
	for _, c := range m.certs {
		certs = append(certs, append([]byte(nil), c...))
	}
	pads = append([]int(nil), m.pads...)
	return certs, m.alpn, pads
}

// Handshake completes mask TLS on raw using the peeked ClientHello.
func (m *Mask) Handshake(raw net.Conn, rec []byte, hello clientHello) (net.Conn, error) {
	if !m.Ready() {
		return nil, errors.New("mask: origin cert not ready")
	}
	if len(hello.sidShare()) != 32 {
		return nil, fmt.Errorf("mask: no x25519 share (len %d sni %q)", len(hello.KeyShare), hello.SNI)
	}
	certs, originALPN, pads := m.snapshot()
	if len(certs) == 0 {
		return nil, errors.New("mask: no certs")
	}
	return maskServer(raw, rec, hello, m.xpriv, certs, pickALPN(hello.ALPNs, originALPN), pads)
}

// pickALPN is h2 when the client offered it (GET), http/1.1 when that is all
// it offered (WebSocket). Origin ALPN is the default when the client lists both.
func pickALPN(client []string, origin string) string {
	hasH2, hasH1 := false, false
	for _, a := range client {
		switch a {
		case "h2":
			hasH2 = true
		case "http/1.1":
			hasH1 = true
		}
	}
	if hasH1 && !hasH2 {
		return "http/1.1"
	}
	if hasH2 {
		if origin != "" {
			return origin
		}
		return "h2"
	}
	if hasH1 {
		return "http/1.1"
	}
	if origin != "" {
		return origin
	}
	return "h2"
}

// maskServer is the TLS 1.3 server handshake: dest-shaped ECDSA leaf, serial bind.
func maskServer(raw net.Conn, rec []byte, hello clientHello, staticPriv []byte, origin [][]byte, alpn string, pads []int) (net.Conn, error) {
	tr := sha256.New()
	chMsg := rec
	if len(rec) >= 5 && rec[0] == recHandshake {
		chMsg = rec[5:]
	}
	tr.Write(chMsg)

	shared, serverShare, group, err := maskKEX(hello)
	if err != nil {
		return nil, err
	}
	auth := authSharedFromPriv(staticPriv, hello.sidShare())
	serial := certSerial(auth, hello.Random)
	certs, ident, err := maskChain(origin, serial, hello.SNI)
	if err != nil {
		return nil, err
	}

	var srvRand [32]byte
	if _, err := rand.Read(srvRand[:]); err != nil {
		return nil, err
	}
	sid := hello.SessionID
	sh := marshalServerHello(srvRand[:], sid, group, serverShare)
	tr.Write(sh)
	if err := writeRecord(raw, recHandshake, sh); err != nil {
		return nil, err
	}
	_ = writeRecord(raw, recChangeCipher, []byte{1})

	hs := tls13Handshake(shared[:], tr.Sum(nil))
	sc, err := newHalf(hs.serverHS, true)
	if err != nil {
		return nil, err
	}

	ee := marshalEncryptedExt(alpn)
	tr.Write(ee)
	if err := sc.writeHandshake(raw, ee); err != nil {
		return nil, err
	}
	certMsg := marshalCertificate(certs)
	tr.Write(certMsg)
	if err := sc.writeHandshake(raw, certMsg); err != nil {
		return nil, err
	}
	sig, err := signMaskCV(ident, tr.Sum(nil))
	if err != nil {
		return nil, err
	}
	cv := marshalCertVerifyScheme(sigECDSA_P256_SHA256, sig)
	tr.Write(cv)
	if err := sc.writeHandshake(raw, cv); err != nil {
		return nil, err
	}
	fin := marshalFinished(tls13Finished(hs.serverHS, tr.Sum(nil)))
	tr.Write(fin)
	if err := sc.writeHandshake(raw, fin); err != nil {
		return nil, err
	}

	apHash := tr.Sum(nil)
	app := tls13App(hs.handshakeSecret, apHash)
	cc, err := newHalf(hs.clientHS, false)
	if err != nil {
		return nil, err
	}
	if err := readClientFinished(raw, cc, hs.clientHS, tr); err != nil {
		return nil, err
	}
	cApp, err := newHalf(app.clientApp, false)
	if err != nil {
		return nil, err
	}
	sApp, err := newHalf(app.serverApp, true)
	if err != nil {
		return nil, err
	}
	_ = raw.SetDeadline(time.Time{})
	return &maskConn{
		Conn: raw,
		in:   cApp,
		out:  sApp,
		pads: pads,
		state: tls.ConnectionState{
			Version:            tls.VersionTLS13,
			HandshakeComplete:  true,
			CipherSuite:        tls.TLS_AES_128_GCM_SHA256,
			NegotiatedProtocol: alpn,
			ServerName:         hello.SNI,
			PeerCertificates:   parseRawCerts(certs),
		},
	}, nil
}

// DialMask is the client mask handshake. maskPub empty skips CertificateVerify check.
func DialMask(addr, sni, fingerprint string, token, maskPub []byte) (net.Conn, error) {
	return dialMaskALPN(addr, sni, fingerprint, token, maskPub, []string{"http/1.1", "h2"})
}

// dialMaskALPN is DialMask with an explicit ALPN offer. WS passes only http/1.1.
func dialMaskALPN(addr, sni, fingerprint string, token, maskPub []byte, alpn []string) (net.Conn, error) {
	skipCV := len(maskPub) != 32
	raw, err := protect.Timeout("tcp4", addr, 10*time.Second)
	if err != nil {
		return nil, err
	}
	tuneTCP(raw)
	pol := CachedCanary(addr)
	if pol.Split {
		raw = &splitHelloConn{Conn: raw, at: 1}
	}
	kx, err := buildMaskHello(sni, fingerprint, token, maskPub, alpn)
	if err != nil {
		raw.Close()
		return nil, err
	}
	helloRaw := kx.raw
	if err := writeRecord(raw, recHandshake, helloRaw); err != nil {
		raw.Close()
		return nil, err
	}
	tr := sha256.New()
	tr.Write(helloRaw)

	shRec, err := readFullRecord(raw)
	if err != nil {
		raw.Close()
		return nil, fmt.Errorf("mask: serverhello read: %w", err)
	}
	if len(shRec) >= 5 && shRec[0] != recHandshake {
		head := shRec
		if len(head) > 24 {
			head = head[:24]
		}
		raw.Close()
		return nil, fmt.Errorf("mask: first rec type=%d len=%d head=%x", shRec[0], len(shRec), head)
	}
	shMsg, err := handshakeFromRecord(shRec)
	if err != nil {
		raw.Close()
		return nil, err
	}
	group, peerShare, err := parseServerHelloShare(shMsg)
	if err != nil {
		head := shMsg
		if len(head) > 32 {
			head = head[:32]
		}
		raw.Close()
		return nil, fmt.Errorf("%w (sh %d %x)", err, len(shMsg), head)
	}
	tr.Write(shMsg)

	shared, err := clientShared(group, peerShare, kx)
	if err != nil {
		raw.Close()
		return nil, err
	}
	hs := tls13Handshake(shared, tr.Sum(nil))
	sc, err := newHalf(hs.serverHS, false)
	if err != nil {
		raw.Close()
		return nil, err
	}
	// optional CCS
	rec, err := readFullRecord(raw)
	if err != nil {
		raw.Close()
		return nil, fmt.Errorf("mask: after-sh read: %w", err)
	}
	if rec[0] == recChangeCipher {
		rec, err = readFullRecord(raw)
		if err != nil {
			raw.Close()
			return nil, fmt.Errorf("mask: after-ccs read: %w", err)
		}
	}
	plain, err := sc.openRecord(rec)
	if err != nil {
		head := rec
		if len(head) > 16 {
			head = head[:16]
		}
		raw.Close()
		return nil, fmt.Errorf("mask: hs open typ=%d len=%d head=%x: %w", rec[0], len(rec), head, err)
	}
	msgs := splitHandshake(plain)
	// may need more records
	for !hasFinished(msgs) {
		rec, err = readFullRecord(raw)
		if err != nil {
			raw.Close()
			return nil, err
		}
		p, err := sc.openRecord(rec)
		if err != nil {
			raw.Close()
			return nil, err
		}
		msgs = append(msgs, splitHandshake(p)...)
	}
	var sawCV bool
	var leaf *x509.Certificate
	for _, msg := range msgs {
		if msg[0] == hsFinished {
			want := tls13Finished(hs.serverHS, tr.Sum(nil))
			got := msg[4:]
			if len(got) < 32 || !hmacEqual(got[:32], want) {
				raw.Close()
				return nil, errors.New("mask: server finished")
			}
			tr.Write(msg)
			continue
		}
		if msg[0] == hsCertificate {
			leaf, _ = leafFromCertMsg(msg)
		}
		if msg[0] == hsCertificateVerify {
			if skipCV {
				sawCV = true
			} else {
				if leaf == nil {
					raw.Close()
					return nil, errors.New("mask: no certificate")
				}
				if err := verifyMaskCV(msg, tr.Sum(nil), leaf); err != nil {
					raw.Close()
					return nil, err
				}
				auth := authSharedFromPub(kx.xPriv, maskPub)
				if len(kx.xPriv) != 32 {
					auth = authSharedFromPub(kx.hybridX, maskPub)
				}
				if !serialEqual(leaf, certSerial(auth, parseHelloRandom(helloRaw))) {
					raw.Close()
					return nil, errors.New("mask: serial bind")
				}
				sawCV = true
			}
		}
		tr.Write(msg)
	}
	if !sawCV {
		raw.Close()
		return nil, errors.New("mask: no certificate verify")
	}
	cc, err := newHalf(hs.clientHS, true)
	if err != nil {
		raw.Close()
		return nil, err
	}
	fin := marshalFinished(tls13Finished(hs.clientHS, tr.Sum(nil)))
	// App secrets use ClientHello…server Finished, not the client Finished.
	app := tls13App(hs.handshakeSecret, tr.Sum(nil))
	if err := cc.writeHandshake(raw, fin); err != nil {
		raw.Close()
		return nil, err
	}
	tr.Write(fin)
	cApp, err := newHalf(app.clientApp, true)
	if err != nil {
		raw.Close()
		return nil, err
	}
	sApp, err := newHalf(app.serverApp, false)
	if err != nil {
		raw.Close()
		return nil, err
	}
	_ = raw.SetDeadline(time.Time{})
	noteKEX(kexName(group))
	return &maskConn{
		Conn: raw,
		in:   sApp,
		out:  cApp,
		state: tls.ConnectionState{
			Version:            tls.VersionTLS13,
			HandshakeComplete:  true,
			CipherSuite:        tls.TLS_AES_128_GCM_SHA256,
			NegotiatedProtocol: firstALPN(alpn),
			ServerName:         sni,
		},
	}, nil
}

// parseHelloRandom is the 32-byte client_random from a ClientHello message.
func parseHelloRandom(msg []byte) []byte {
	h, err := parseClientHello(msg)
	if err != nil {
		return nil
	}
	return h.Random
}

// buildMaskHello prefers a uTLS ClientHello (browser JA3) and falls back to
// the compact builder. session_id is sid v3 when maskPub is known, else v2.
func buildMaskHello(sni, fingerprint string, token, maskPub []byte, alpn []string) (clientKX, error) {
	kx, err := buildUTLSHello(sni, fingerprint, token, maskPub, alpn)
	if err == nil && len(kx.raw) > 4 && (len(kx.xPriv) == 32 || kx.mlkem != nil) {
		return kx, nil
	}
	msg, priv, err := buildMaskClientHello(sni, token, maskPub, alpn)
	if err != nil {
		return clientKX{}, err
	}
	return clientKX{raw: msg, xPriv: priv}, nil
}

func firstALPN(alpn []string) string {
	if len(alpn) == 0 {
		return "h2"
	}
	return alpn[0]
}

// buildMaskClientHello builds a TLS 1.3 ClientHello with sid v3/v2 and X25519.
func buildMaskClientHello(sni string, token, maskPub []byte, alpn []string) (msg, priv []byte, err error) {
	if len(alpn) == 0 {
		alpn = []string{"http/1.1", "h2"}
	}
	var ephPriv, ephPub [32]byte
	if _, err = rand.Read(ephPriv[:]); err != nil {
		return nil, nil, err
	}
	curve25519.ScalarBaseMult(&ephPub, &ephPriv)
	var random [32]byte
	if _, err = rand.Read(random[:]); err != nil {
		return nil, nil, err
	}
	h := clientHello{Random: random[:], SNI: sni, KeyShare: ephPub[:]}
	var sid []byte
	if len(maskPub) == 32 {
		sid = sid3FromPriv(ephPriv[:], maskPub, h)
		if len(sid) != 32 {
			return nil, nil, errors.New("mask: sid v3")
		}
	} else {
		sid = sessionHMAC(token, h)
	}
	var b cryptobyte.Builder
	b.AddUint8(hsClientHello)
	b.AddUint24LengthPrefixed(func(b *cryptobyte.Builder) {
		b.AddUint16(0x0303)
		b.AddBytes(random[:])
		b.AddUint8LengthPrefixed(func(b *cryptobyte.Builder) { b.AddBytes(sid) })
		b.AddUint16LengthPrefixed(func(b *cryptobyte.Builder) {
			b.AddUint16(tls13AESGCM)
			b.AddUint16(tls13ChaCha)
		})
		b.AddUint8LengthPrefixed(func(b *cryptobyte.Builder) { b.AddUint8(0) })
		b.AddUint16LengthPrefixed(func(b *cryptobyte.Builder) {
			// supported_versions
			b.AddUint16(43)
			b.AddUint16LengthPrefixed(func(b *cryptobyte.Builder) {
				b.AddUint8LengthPrefixed(func(b *cryptobyte.Builder) { b.AddUint16(0x0304) })
			})
			// server_name
			if sni != "" {
				b.AddUint16(0)
				b.AddUint16LengthPrefixed(func(b *cryptobyte.Builder) {
					b.AddUint16LengthPrefixed(func(b *cryptobyte.Builder) {
						b.AddUint8(0)
						b.AddUint16LengthPrefixed(func(b *cryptobyte.Builder) { b.AddBytes([]byte(sni)) })
					})
				})
			}
			// supported_groups
			b.AddUint16(10)
			b.AddUint16LengthPrefixed(func(b *cryptobyte.Builder) {
				b.AddUint16LengthPrefixed(func(b *cryptobyte.Builder) { b.AddUint16(x25519Group) })
			})
			// key_share
			b.AddUint16(51)
			b.AddUint16LengthPrefixed(func(b *cryptobyte.Builder) {
				b.AddUint16LengthPrefixed(func(b *cryptobyte.Builder) {
					b.AddUint16(x25519Group)
					b.AddUint16LengthPrefixed(func(b *cryptobyte.Builder) { b.AddBytes(ephPub[:]) })
				})
			})
			// signature_algorithms: dest-typical ECDSA first
			b.AddUint16(13)
			b.AddUint16LengthPrefixed(func(b *cryptobyte.Builder) {
				b.AddUint16LengthPrefixed(func(b *cryptobyte.Builder) {
					b.AddUint16(sigECDSA_P256_SHA256)
					b.AddUint16(0x0804) // rsa_pss_rsae_sha256
					b.AddUint16(sigEd25519)
				})
			})
			// psk_key_exchange_modes (psk_dhe_ke) — some stacks require it for TLS 1.3
			b.AddUint16(45)
			b.AddUint16LengthPrefixed(func(b *cryptobyte.Builder) {
				b.AddUint8LengthPrefixed(func(b *cryptobyte.Builder) { b.AddUint8(1) })
			})
			b.AddUint16(16)
			b.AddUint16LengthPrefixed(func(b *cryptobyte.Builder) {
				b.AddUint16LengthPrefixed(func(b *cryptobyte.Builder) {
					for _, p := range alpn {
						b.AddUint8LengthPrefixed(func(b *cryptobyte.Builder) { b.AddBytes([]byte(p)) })
					}
				})
			})
		})
	})
	msg, err = b.Bytes()
	return msg, ephPriv[:], err
}

// hmacEqual is a constant-time compare.
func hmacEqual(a, b []byte) bool {
	if len(a) != len(b) {
		return false
	}
	var v byte
	for i := range a {
		v |= a[i] ^ b[i]
	}
	return v == 0
}

// parseRawCerts parses DER certificates, skipping failures.
func parseRawCerts(raws [][]byte) []*x509.Certificate {
	var out []*x509.Certificate
	for _, r := range raws {
		c, err := x509.ParseCertificate(r)
		if err != nil {
			continue
		}
		out = append(out, c)
	}
	return out
}

// hsSecrets are TLS 1.3 handshake traffic secrets.
type hsSecrets struct {
	handshakeSecret []byte
	clientHS        []byte
	serverHS        []byte
}

// appSecrets are TLS 1.3 application traffic secrets.
type appSecrets struct {
	clientApp []byte
	serverApp []byte
}

// tls13Handshake derives handshake traffic secrets from the X25519 shared secret.
func tls13Handshake(shared, helloHash []byte) hsSecrets {
	early := hkdfExtract(make([]byte, 32), nil)
	derived := hkdfExpandLabel(early, "derived", emptyHash(), 32)
	hs := hkdfExtract(derived, shared)
	return hsSecrets{
		handshakeSecret: hs,
		clientHS:        hkdfExpandLabel(hs, "c hs traffic", helloHash, 32),
		serverHS:        hkdfExpandLabel(hs, "s hs traffic", helloHash, 32),
	}
}

// tls13App derives application traffic secrets from the handshake secret.
func tls13App(handshakeSecret, hash []byte) appSecrets {
	derived := hkdfExpandLabel(handshakeSecret, "derived", emptyHash(), 32)
	master := hkdfExtract(derived, nil)
	return appSecrets{
		clientApp: hkdfExpandLabel(master, "c ap traffic", hash, 32),
		serverApp: hkdfExpandLabel(master, "s ap traffic", hash, 32),
	}
}

// tls13Finished is the Finished verify_data for traffic.
func tls13Finished(traffic, sum []byte) []byte {
	key := hkdfExpandLabel(traffic, "finished", nil, 32)
	return hmacSHA256(key, sum)
}

// hmacSHA256 returns HMAC-SHA256(key, data).
func hmacSHA256(key, data []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write(data)
	return m.Sum(nil)
}

// emptyHash is SHA-256 of the empty string.
func emptyHash() []byte {
	h := sha256.New()
	return h.Sum(nil)
}

// hkdfExtract is HKDF-Extract with SHA-256.
func hkdfExtract(salt, ikm []byte) []byte {
	if salt == nil {
		salt = make([]byte, 32)
	}
	if ikm == nil {
		ikm = make([]byte, 32)
	}
	out, err := hkdf.Extract(sha256.New, ikm, salt)
	if err != nil {
		panic(err)
	}
	return out
}

// hkdfExpandLabel is TLS 1.3 HKDF-Expand-Label.
func hkdfExpandLabel(secret []byte, label string, context []byte, length int) []byte {
	full := "tls13 " + label
	var b cryptobyte.Builder
	b.AddUint16(uint16(length))
	b.AddUint8LengthPrefixed(func(b *cryptobyte.Builder) { b.AddBytes([]byte(full)) })
	b.AddUint8LengthPrefixed(func(b *cryptobyte.Builder) { b.AddBytes(context) })
	info, _ := b.Bytes()
	out, err := hkdf.Expand(sha256.New, secret, string(info), length)
	if err != nil {
		panic(err)
	}
	return out
}

// certVerifySigned is the TLS 1.3 CertificateVerify input for transcript.
func certVerifySigned(transcript []byte, server bool) []byte {
	pad := make([]byte, 64)
	for i := range pad {
		pad[i] = 0x20
	}
	ctx := "TLS 1.3, client CertificateVerify"
	if server {
		ctx = "TLS 1.3, server CertificateVerify"
	}
	out := append(pad, []byte(ctx)...)
	out = append(out, 0)
	return append(out, transcript...)
}

// marshalServerHello builds a TLS 1.3 ServerHello. group is x25519 or
// X25519MLKEM768; share is the key_exchange for that group.
func marshalServerHello(random, sid []byte, group uint16, share []byte) []byte {
	var b cryptobyte.Builder
	b.AddUint8(hsServerHello)
	b.AddUint24LengthPrefixed(func(b *cryptobyte.Builder) {
		b.AddUint16(0x0303)
		b.AddBytes(random)
		b.AddUint8LengthPrefixed(func(b *cryptobyte.Builder) { b.AddBytes(sid) })
		b.AddUint16(tls13AESGCM)
		b.AddUint8(0) // compression
		b.AddUint16LengthPrefixed(func(b *cryptobyte.Builder) {
			// supported_versions
			b.AddUint16(43)
			b.AddUint16LengthPrefixed(func(b *cryptobyte.Builder) { b.AddUint16(0x0304) })
			// key_share
			b.AddUint16(51)
			b.AddUint16LengthPrefixed(func(b *cryptobyte.Builder) {
				b.AddUint16(group)
				b.AddUint16LengthPrefixed(func(b *cryptobyte.Builder) { b.AddBytes(share) })
			})
		})
	})
	msg, _ := b.Bytes()
	return msg
}

// marshalEncryptedExt builds EncryptedExtensions with ALPN.
func marshalEncryptedExt(alpn string) []byte {
	var b cryptobyte.Builder
	b.AddUint8(hsEncryptedExt)
	b.AddUint24LengthPrefixed(func(b *cryptobyte.Builder) {
		b.AddUint16LengthPrefixed(func(b *cryptobyte.Builder) {
			if alpn == "" {
				return
			}
			b.AddUint16(16)
			b.AddUint16LengthPrefixed(func(b *cryptobyte.Builder) {
				b.AddUint16LengthPrefixed(func(b *cryptobyte.Builder) {
					b.AddUint8LengthPrefixed(func(b *cryptobyte.Builder) { b.AddBytes([]byte(alpn)) })
				})
			})
		})
	})
	msg, _ := b.Bytes()
	return msg
}

// marshalCertificate builds a TLS 1.3 Certificate message from DER certs.
func marshalCertificate(certs [][]byte) []byte {
	var b cryptobyte.Builder
	b.AddUint8(hsCertificate)
	b.AddUint24LengthPrefixed(func(b *cryptobyte.Builder) {
		b.AddUint8(0) // request context
		b.AddUint24LengthPrefixed(func(b *cryptobyte.Builder) {
			for _, c := range certs {
				b.AddUint24LengthPrefixed(func(b *cryptobyte.Builder) { b.AddBytes(c) })
				b.AddUint16(0)
			}
		})
	})
	msg, _ := b.Bytes()
	return msg
}

// marshalCertVerify builds CertificateVerify with ecdsa_secp256r1_sha256.
func marshalCertVerify(sig []byte) []byte {
	return marshalCertVerifyScheme(sigECDSA_P256_SHA256, sig)
}

// marshalFinished builds a Finished message.
func marshalFinished(mac []byte) []byte {
	var b cryptobyte.Builder
	b.AddUint8(hsFinished)
	b.AddUint24LengthPrefixed(func(b *cryptobyte.Builder) { b.AddBytes(mac) })
	msg, _ := b.Bytes()
	return msg
}

// parseServerHelloShare returns the selected group and its key_exchange.
func parseServerHelloShare(msg []byte) (uint16, []byte, error) {
	if len(msg) < 40 || msg[0] != hsServerHello {
		return 0, nil, errors.New("mask: not server hello")
	}
	n := int(msg[1])<<16 | int(msg[2])<<8 | int(msg[3])
	body := msg[4 : 4+n]
	off := 2 + 32 // version + random
	if off >= len(body) {
		return 0, nil, errors.New("mask: short sh")
	}
	sidLen := int(body[off])
	off += 1 + sidLen + 2 + 1 // sid, cipher, compression
	if off+2 > len(body) {
		return 0, nil, errors.New("mask: no ext")
	}
	extLen := int(binary.BigEndian.Uint16(body[off : off+2]))
	off += 2
	end := off + extLen
	if end > len(body) {
		end = len(body)
	}
	for off+4 <= end {
		typ := binary.BigEndian.Uint16(body[off : off+2])
		ln := int(binary.BigEndian.Uint16(body[off+2 : off+4]))
		off += 4
		if off+ln > end {
			break
		}
		if typ == 51 && ln >= 4 {
			group := binary.BigEndian.Uint16(body[off : off+2])
			klen := int(binary.BigEndian.Uint16(body[off+2 : off+4]))
			if klen >= 0 && 4+klen <= ln {
				share := body[off+4 : off+4+klen]
				switch group {
				case x25519Group:
					if klen == 32 {
						return group, append([]byte(nil), share...), nil
					}
				case x25519MLKEM768:
					if klen == hybridServerShare() {
						return group, append([]byte(nil), share...), nil
					}
				}
			}
		}
		off += ln
	}
	return 0, nil, errors.New("mask: no key share in server hello")
}

// writeRecord writes one TLS record of typ.
func writeRecord(w io.Writer, typ byte, payload []byte) error {
	hdr := []byte{typ, 0x03, 0x03, 0, 0}
	binary.BigEndian.PutUint16(hdr[3:5], uint16(len(payload)))
	_, err := w.Write(append(hdr, payload...))
	return err
}

// readFullRecord reads one TLS record. Ciphertext max is 16KiB + 256.
func readFullRecord(r io.Reader) ([]byte, error) {
	hdr := make([]byte, 5)
	if _, err := io.ReadFull(r, hdr); err != nil {
		return nil, err
	}
	n := int(binary.BigEndian.Uint16(hdr[3:5]))
	// TLS 1.3 ciphertext max is 2^14 + 256 (plaintext 16KiB + AEAD expansion).
	if n < 0 || n > (16<<10)+256 {
		return nil, errors.New("mask: bad record")
	}
	body := make([]byte, n)
	if _, err := io.ReadFull(r, body); err != nil {
		return nil, err
	}
	return append(hdr, body...), nil
}

// handshakeFromRecord strips the TLS record header from a handshake record.
func handshakeFromRecord(rec []byte) ([]byte, error) {
	if len(rec) < 5 || rec[0] != recHandshake {
		return nil, errors.New("mask: not handshake record")
	}
	return rec[5:], nil
}

// splitHandshake splits concatenated handshake messages.
func splitHandshake(b []byte) [][]byte {
	var out [][]byte
	for len(b) >= 4 {
		n := int(b[1])<<16 | int(b[2])<<8 | int(b[3])
		if 4+n > len(b) {
			break
		}
		out = append(out, b[:4+n])
		b = b[4+n:]
	}
	return out
}

// hasFinished reports whether msgs includes a Finished.
func hasFinished(msgs [][]byte) bool {
	for _, m := range msgs {
		if len(m) > 0 && m[0] == hsFinished {
			return true
		}
	}
	return false
}

// readClientFinished reads and checks the client's Finished.
func readClientFinished(raw net.Conn, cc *half, traffic []byte, tr hash.Hash) error {
	rec, err := readFullRecord(raw)
	if err != nil {
		return err
	}
	if rec[0] == recChangeCipher {
		rec, err = readFullRecord(raw)
		if err != nil {
			return err
		}
	}
	plain, err := cc.openRecord(rec)
	if err != nil {
		return err
	}
	msgs := splitHandshake(plain)
	if len(msgs) == 0 || msgs[0][0] != hsFinished {
		return errors.New("mask: expected client finished")
	}
	want := tls13Finished(traffic, tr.Sum(nil))
	got := msgs[0][4:]
	if len(got) < 32 || !hmacEqual(got[:32], want) {
		return errors.New("mask: client finished")
	}
	tr.Write(msgs[0])
	return nil
}

// half is one TLS 1.3 AES-GCM traffic direction.
type half struct {
	aead cipher.AEAD
	iv   []byte
	seq  uint64
}

// newHalf derives AES-128-GCM key and IV from traffic.
func newHalf(traffic []byte, _ bool) (*half, error) {
	key := hkdfExpandLabel(traffic, "key", nil, 16)
	iv := hkdfExpandLabel(traffic, "iv", nil, 12)
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return &half{aead: aead, iv: iv}, nil
}

// nonce is the next AES-GCM nonce (IV xor seq) and increments seq.
func (h *half) nonce() []byte {
	n := make([]byte, 12)
	copy(n, h.iv)
	var seq [8]byte
	binary.BigEndian.PutUint64(seq[:], h.seq)
	for i := 0; i < 8; i++ {
		n[4+i] ^= seq[i]
	}
	h.seq++
	return n
}

// writeHandshake seals msg as a TLS 1.3 handshake inner plaintext.
func (h *half) writeHandshake(w io.Writer, msg []byte) error {
	inner := append(append([]byte{}, msg...), recHandshake)
	overhead := h.aead.Overhead()
	clen := len(inner) + overhead
	hdr := []byte{recApp, 0x03, 0x03, byte(clen >> 8), byte(clen)}
	ct := h.aead.Seal(nil, h.nonce(), inner, hdr)
	_, err := w.Write(append(hdr, ct...))
	return err
}

// openRecord decrypts rec and strips inner content type plus RFC 8446 padding.
func (h *half) openRecord(rec []byte) ([]byte, error) {
	if len(rec) < 5 {
		return nil, errors.New("mask: short record")
	}
	hdr, ct := rec[:5], rec[5:]
	plain, err := h.aead.Open(nil, h.nonce(), ct, hdr)
	if err != nil {
		return nil, err
	}
	i := len(plain) - 1
	for i >= 0 && plain[i] == 0 {
		i--
	}
	if i < 0 {
		return nil, errors.New("mask: empty inner")
	}
	return plain[:i], nil // drop type and trailing zeros
}

// sealApp seals application data as a TLS 1.3 record.
func (h *half) sealApp(p []byte) []byte {
	return h.sealAppPad(p, 0)
}

// sealAppPad seals p with RFC 8446 inner padding (zeros after the content type).
func (h *half) sealAppPad(p []byte, pad int) []byte {
	inner := append(append([]byte{}, p...), recApp)
	if pad > 0 {
		inner = append(inner, make([]byte, pad)...)
	}
	clen := len(inner) + h.aead.Overhead()
	hdr := []byte{recApp, 0x03, 0x03, byte(clen >> 8), byte(clen)}
	ct := h.aead.Seal(nil, h.nonce(), inner, hdr)
	return append(hdr, ct...)
}

// openApp decrypts an application record.
func (h *half) openApp(rec []byte) ([]byte, error) {
	return h.openRecord(rec)
}

// maskConn is net.Conn after mask TLS, with AES-GCM application records.
type maskConn struct {
	net.Conn
	in, out *half
	state   tls.ConnectionState
	rbuf    []byte
	wmu     sync.Mutex
	pads    []int
	padI    int
}

// ConnectionState returns the completed TLS 1.3 state.
func (c *maskConn) ConnectionState() tls.ConnectionState { return c.state }

// Read decrypts application records into p.
func (c *maskConn) Read(p []byte) (int, error) {
	if len(c.rbuf) > 0 {
		n := copy(p, c.rbuf)
		c.rbuf = c.rbuf[n:]
		return n, nil
	}
	rec, err := readFullRecord(c.Conn)
	if err != nil {
		return 0, err
	}
	plain, err := c.in.openApp(rec)
	if err != nil {
		return 0, err
	}
	n := copy(p, plain)
	c.rbuf = append([]byte(nil), plain[n:]...)
	return n, nil
}

// TLS 1.3 inner plaintext max 2^14; last byte is the hidden content type.
const maskAppMax = 16384 - 1

// Write seals p in records of at most maskAppMax bytes, padded to dest sizes.
func (c *maskConn) Write(p []byte) (int, error) {
	c.wmu.Lock()
	defer c.wmu.Unlock()
	n := 0
	oh := 16
	if c.out != nil && c.out.aead != nil {
		oh = c.out.aead.Overhead()
	}
	for len(p) > 0 {
		target := destRec(c.pads, c.padI)
		c.padI++
		nchunk := chunkFor(target, len(p), oh)
		chunk := p[:nchunk]
		pad := padFor(target, len(chunk), oh)
		if _, err := c.Conn.Write(c.out.sealAppPad(chunk, pad)); err != nil {
			return n, err
		}
		n += len(chunk)
		p = p[len(chunk):]
	}
	return n, nil
}

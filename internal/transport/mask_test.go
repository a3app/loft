package transport

// Mask record-layer, handshake loopback, and front-filter tests.

import (
	"bytes"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/mlkem"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/binary"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net"
	"sync"
	"testing"
	"time"

	"golang.org/x/crypto/curve25519"

	"github.com/a3app/loft/internal/config"
	"github.com/a3app/loft/internal/testcfg"
)

// TestMaskRecordRoundtrip opens a sealed handshake record.
func TestMaskRecordRoundtrip(t *testing.T) {
	secret := bytes.Repeat([]byte{7}, 32)
	w, err := newHalf(secret, true)
	if err != nil {
		t.Fatal(err)
	}
	r, err := newHalf(secret, false)
	if err != nil {
		t.Fatal(err)
	}
	var buf []byte
	if err := w.writeHandshake(writerFunc(func(p []byte) (int, error) {
		buf = append(buf, p...)
		return len(p), nil
	}), []byte{hsFinished, 0, 0, 4, 1, 2, 3, 4}); err != nil {
		t.Fatal(err)
	}
	plain, err := r.openRecord(buf)
	if err != nil {
		t.Fatal(err)
	}
	if len(plain) < 4 || plain[0] != hsFinished {
		t.Fatalf("%x", plain)
	}
}

// TestMaskConnLargeWrite writes more than one TLS 1.3 application record.
func TestMaskConnLargeWrite(t *testing.T) {
	secret := bytes.Repeat([]byte{9}, 32)
	out, err := newHalf(secret, true)
	if err != nil {
		t.Fatal(err)
	}
	in, err := newHalf(secret, false)
	if err != nil {
		t.Fatal(err)
	}
	c1, c2 := net.Pipe()
	defer c1.Close()
	defer c2.Close()
	w := &maskConn{Conn: c1, out: out}
	r := &maskConn{Conn: c2, in: in}
	payload := make([]byte, 40000)
	for i := range payload {
		payload[i] = byte(i)
	}
	errc := make(chan error, 1)
	go func() {
		_, err := w.Write(payload)
		errc <- err
		c1.Close()
	}()
	got, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("got %d want %d", len(got), len(payload))
	}
}

// writerFunc is an io.Writer over a function.
type writerFunc func([]byte) (int, error)

// Write calls the function.
func (w writerFunc) Write(p []byte) (int, error) { return w(p) }

// TestMaskHandshakeLoopback completes dest-shaped ECDSA mask TLS over loopback.
func TestMaskHandshakeLoopback(t *testing.T) {
	token := []byte("mask-test-token-32bytes-minimum!")
	xpriv := maskX25519Priv(token)
	xpub, err := curve25519.X25519(xpriv, curve25519.Basepoint)
	if err != nil {
		t.Fatal(err)
	}
	cert := selfSigned(t, testcfg.Decoy())
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	m := &Mask{xpriv: xpriv, xpub: xpub}
	m.certs = [][]byte{cert}
	m.alpn = "h2"
	m.ready.Store(true)

	var wg sync.WaitGroup
	wg.Add(1)
	errc := make(chan error, 1)
	go func() {
		defer wg.Done()
		c, err := ln.Accept()
		if err != nil {
			errc <- err
			return
		}
		raw, hello, err := readClientHello(c)
		if err != nil {
			c.Close()
			errc <- err
			return
		}
		if !verifyAny(token, hello) {
			c.Close()
			errc <- io.ErrUnexpectedEOF
			return
		}
		if hello.sidShare() == nil {
			errc <- fmt.Errorf("hello sni=%q sid=%d raw=%d exts=%v", hello.SNI, len(hello.SessionID), len(raw), extTypes(raw))
			c.Close()
			return
		}
		if len(hello.hybridEK) != mlkem.EncapsulationKeySize768 || len(hello.hybridX) != 32 {
			errc <- fmt.Errorf("chrome hello: hybrid ek %d x %d", len(hello.hybridEK), len(hello.hybridX))
			c.Close()
			return
		}
		mc, err := m.Handshake(c, raw, hello)
		if err != nil {
			c.Close()
			errc <- err
			return
		}
		buf := make([]byte, 4)
		if _, err := io.ReadFull(mc, buf); err != nil {
			errc <- err
			return
		}
		_, err = mc.Write(buf)
		errc <- err
	}()

	addr := ln.Addr().String()
	RememberMaskPub(addr, m.Public())
	t.Cleanup(func() { views.Delete(addr) })
	client, err := DialMask(addr, testcfg.Decoy(), "chrome", token, m.Public())
	if err != nil {
		wg.Wait()
		t.Fatalf("dial %v server %v", err, <-errc)
	}
	defer client.Close()
	if _, err := client.Write([]byte("ping")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 4)
	if _, err := io.ReadFull(client, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != "ping" {
		t.Fatalf("got %q", buf)
	}
	wg.Wait()
	if err := <-errc; err != nil {
		t.Fatal(err)
	}
}

// TestFilterOutboundsRespectsFronts keeps only client-enabled fronts.
func TestFilterOutboundsRespectsFronts(t *testing.T) {
	clearDecoyCache()
	views = sync.Map{}
	cfg := &config.ClientConfig{
		Fronts: []string{"ws"},
		Outbounds: []config.Outbound{
			{Name: "a", Type: "get", URL: "https://n.example"},
			{Name: "b", Type: "ws", URL: "wss://n.example/api/v1/sync"},
		},
	}
	list := filterOutbounds(cfg, cfg.Outbounds)
	if len(list) != 1 || list[0].Type != "ws" {
		t.Fatalf("%+v", list)
	}
}

// TestMintDestLeafCopiesOrigin mints an ECDSA P-256 leaf with dest names and serial bind.
func TestMintDestLeafCopiesOrigin(t *testing.T) {
	origin := selfSigned(t, testcfg.Decoy())
	orig, err := x509.ParseCertificate(origin)
	if err != nil {
		t.Fatal(err)
	}
	shared := bytes.Repeat([]byte{11}, 32)
	rnd := bytes.Repeat([]byte{4}, 32)
	serial := certSerial(shared, rnd)
	der, key, err := mintDestLeaf(origin, serial, testcfg.Decoy())
	if err != nil {
		t.Fatal(err)
	}
	if key == nil {
		t.Fatal("key")
	}
	cert, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	if cert.PublicKeyAlgorithm != x509.ECDSA {
		t.Fatalf("alg %v", cert.PublicKeyAlgorithm)
	}
	if _, ok := cert.PublicKey.(*ecdsa.PublicKey); !ok {
		t.Fatal("pub")
	}
	if len(cert.DNSNames) == 0 || cert.DNSNames[0] != testcfg.Decoy() {
		t.Fatalf("dns %v", cert.DNSNames)
	}
	if orig.Subject.CommonName != "" && cert.Subject.CommonName != orig.Subject.CommonName {
		t.Fatalf("cn %q", cert.Subject.CommonName)
	}
	if !serialEqual(cert, serial) {
		t.Fatal("serial bind")
	}
}

// TestSealAppPadMatchesPattern pads ciphertext to a sampled dest record length.
func TestSealAppPadMatchesPattern(t *testing.T) {
	secret := bytes.Repeat([]byte{5}, 32)
	w, err := newHalf(secret, true)
	if err != nil {
		t.Fatal(err)
	}
	r, err := newHalf(secret, false)
	if err != nil {
		t.Fatal(err)
	}
	payload := []byte("hello")
	target := 5 + len(payload) + 1 + 10 + 16 // header + content + type + pad + gcm
	pad := padFor(target, len(payload), 16)
	if pad != 10 {
		t.Fatalf("pad %d", pad)
	}
	rec := w.sealAppPad(payload, pad)
	if len(rec) != target {
		t.Fatalf("len %d want %d", len(rec), target)
	}
	got, err := r.openRecord(rec)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, payload) {
		t.Fatalf("got %q", got)
	}
}

// TestParseMaskPubHexAndBase64url round-trips 32-byte keys.
func TestParseMaskPubHexAndBase64url(t *testing.T) {
	raw := bytes.Repeat([]byte{0xab}, 32)
	hex := fmt.Sprintf("%x", raw)
	if got := ParseMaskPub(hex); !bytes.Equal(got, raw) {
		t.Fatalf("hex %x", got)
	}
	b64 := base64.RawURLEncoding.EncodeToString(raw)
	if got := ParseMaskPub(b64); !bytes.Equal(got, raw) {
		t.Fatalf("b64 %x", got)
	}
}

// selfSigned returns a DER self-signed certificate for dns.
func selfSigned(t *testing.T, dns string) []byte {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: dns},
		DNSNames:     []string{dns},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	_ = pem.EncodeToMemory
	return der
}

// extTypes lists TLS extension types from a ClientHello record.
func extTypes(rec []byte) []uint16 {
	body := rec
	if len(rec) >= 5 && rec[0] == 0x16 {
		body = rec[5:]
	}
	if len(body) < 40 {
		return nil
	}
	off := 4 + 2 + 32
	if off >= len(body) {
		return nil
	}
	off += 1 + int(body[off])
	if off+2 > len(body) {
		return nil
	}
	cs := int(binary.BigEndian.Uint16(body[off : off+2]))
	off += 2 + cs
	if off >= len(body) {
		return nil
	}
	off += 1 + int(body[off])
	if off+2 > len(body) {
		return nil
	}
	el := int(binary.BigEndian.Uint16(body[off : off+2]))
	off += 2
	end := off + el
	if end > len(body) {
		end = len(body)
	}
	var out []uint16
	for off+4 <= end {
		typ := binary.BigEndian.Uint16(body[off : off+2])
		ln := int(binary.BigEndian.Uint16(body[off+2 : off+4]))
		out = append(out, typ)
		off += 4 + ln
	}
	return out
}

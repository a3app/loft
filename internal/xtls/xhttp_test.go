package xtls

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"io"
	"math/big"
	"net"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"golang.org/x/net/http2"
)

func TestXHTTPStreamOneEcho(t *testing.T) {
	ln := serveH2(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/one" {
			http.Error(w, "shape", 400)
			return
		}
		w.WriteHeader(200)
		_, _ = io.Copy(flushBody{w}, r.Body)
	}))
	c := dialH2(t, ln)
	xc, err := DialXHTTP(c, XHTTP{Host: "panel.example", Path: "/one"})
	if err != nil {
		t.Fatal(err)
	}
	defer xc.Close()
	_ = xc.SetDeadline(time.Now().Add(5 * time.Second))
	if _, err := xc.Write([]byte("one")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 3)
	if _, err := io.ReadFull(xc, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != "one" {
		t.Fatalf("echo %q", buf)
	}
}

func TestXHTTPStreamUpEcho(t *testing.T) {
	var mu sync.Mutex
	sess := map[string]*io.PipeWriter{}
	ln := serveH2(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ru, err := url.Parse(r.Header.Get("Referer"))
		if err != nil || ru.Path != "/abc/" {
			http.Error(w, "referer", 400)
			return
		}
		pad := ru.Query().Get("x_padding")
		if len(pad) < 100 || len(pad) > 1000 || strings.Trim(pad, "X") != "" {
			http.Error(w, "pad", 400)
			return
		}
		const base = "/abc/"
		if !strings.HasPrefix(r.URL.Path, base) {
			http.Error(w, "path", 400)
			return
		}
		id := strings.TrimPrefix(r.URL.Path, base)
		if id == "" || strings.Contains(id, "/") {
			http.Error(w, "sid", 400)
			return
		}
		switch r.Method {
		case http.MethodGet:
			pr, pw := io.Pipe()
			mu.Lock()
			sess[id] = pw
			mu.Unlock()
			w.WriteHeader(200)
			if f, ok := w.(http.Flusher); ok {
				f.Flush()
			}
			_, _ = io.Copy(flushBody{w}, pr)
		case http.MethodPost:
			if r.Header.Get("Content-Type") != "application/grpc" {
				http.Error(w, "ct", 400)
				return
			}
			var pw *io.PipeWriter
			deadline := time.Now().Add(3 * time.Second)
			for pw == nil && time.Now().Before(deadline) {
				mu.Lock()
				pw = sess[id]
				mu.Unlock()
				if pw == nil {
					time.Sleep(5 * time.Millisecond)
				}
			}
			if pw == nil {
				http.Error(w, "nosess", 400)
				return
			}
			defer pw.Close()
			_, _ = io.Copy(pw, r.Body)
			w.WriteHeader(200)
		default:
			http.Error(w, "method", 400)
		}
	}))
	c := dialH2(t, ln)
	xc, err := DialXHTTP(c, XHTTP{Host: "panel.example", Path: "/abc", Mode: "stream-up", PadFrom: 100, PadTo: 1000})
	if err != nil {
		t.Fatal(err)
	}
	defer xc.Close()
	_ = xc.SetDeadline(time.Now().Add(5 * time.Second))
	payload := []byte("stream-up")
	if _, err := xc.Write(payload); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, len(payload))
	if _, err := io.ReadFull(xc, buf); err != nil {
		t.Fatal(err)
	}
	if string(buf) != string(payload) {
		t.Fatalf("echo %q", buf)
	}
}

func TestXHTTPReadDeadlineBeforeHeaders(t *testing.T) {
	ln := serveH2(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		<-r.Context().Done()
	}))
	c := dialH2(t, ln)
	xc, err := DialXHTTP(c, XHTTP{Host: "panel.example", Path: "/hang"})
	if err != nil {
		t.Fatal(err)
	}
	defer xc.Close()
	if err := xc.SetReadDeadline(time.Now().Add(200 * time.Millisecond)); err != nil {
		t.Fatal(err)
	}
	start := time.Now()
	_, err = xc.Read(make([]byte, 1))
	if time.Since(start) > 2*time.Second {
		t.Fatal("read waited past the deadline")
	}
	ne, ok := err.(net.Error)
	if !ok || !ne.Timeout() || !strings.Contains(err.Error(), "i/o timeout") {
		t.Fatalf("want timeout, got %v", err)
	}
}

func TestXHTTPPacketUpRejected(t *testing.T) {
	a, b := net.Pipe()
	defer b.Close()
	_, err := DialXHTTP(a, XHTTP{Mode: "packet-up"})
	if err == nil || !strings.Contains(err.Error(), "packet-up") {
		t.Fatal(err)
	}
}

func TestDialFingerprintFirefoxH2(t *testing.T) {
	ln := serveH2(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(200)
	}))
	raw, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	c, err := DialTLS(raw, "panel.example", "firefox", []string{"h2"}, true)
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
}

// flushBody pushes each Write out. The handler must not wait for the
// request body to end before the client can read the response.
type flushBody struct{ http.ResponseWriter }

func (f flushBody) Write(p []byte) (int, error) {
	n, err := f.ResponseWriter.Write(p)
	if fl, ok := f.ResponseWriter.(http.Flusher); ok {
		fl.Flush()
	}
	return n, err
}

func serveH2(t *testing.T, h http.Handler) net.Listener {
	t.Helper()
	cert := testCert(t)
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	srv := &http.Server{Handler: h, TLSConfig: &tls.Config{
		Certificates: []tls.Certificate{cert},
		NextProtos:   []string{"h2"},
	}}
	if err := http2.ConfigureServer(srv, &http2.Server{}); err != nil {
		t.Fatal(err)
	}
	go func() { _ = srv.Serve(tls.NewListener(ln, srv.TLSConfig)) }()
	t.Cleanup(func() { _ = srv.Close() })
	return ln
}

func dialH2(t *testing.T, ln net.Listener) net.Conn {
	t.Helper()
	raw, err := net.Dial("tcp", ln.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	tc := tls.Client(raw, &tls.Config{
		InsecureSkipVerify: true,
		ServerName:         "panel.example",
		NextProtos:         []string{"h2"},
	})
	if err := tc.Handshake(); err != nil {
		t.Fatal(err)
	}
	if tc.ConnectionState().NegotiatedProtocol != "h2" {
		t.Fatalf("alpn %s", tc.ConnectionState().NegotiatedProtocol)
	}
	return tc
}

func testCert(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	tmpl := &x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "panel.example"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		DNSNames:     []string{"panel.example"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
	}
	der, err := x509.CreateCertificate(rand.Reader, tmpl, tmpl, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

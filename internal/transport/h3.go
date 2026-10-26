package transport

// HTTP/3 CONNECT front: ALPN h3, CONNECT to the same path shape as h2.
//
// This is the h2 front (h2.go) carried over QUIC instead of TCP. The node
// side also lives here: one UDP socket serves both the plain quic front and
// h3, and since both use ALPN h3 the node tells them apart by the first byte
// of the first stream (serveH3OrQUIC).

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"time"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"

	"github.com/a3app/loft/internal/config"
	ecrypto "github.com/a3app/loft/internal/crypto"
	"github.com/a3app/loft/internal/mux"
	"github.com/a3app/loft/internal/wire"
)

// h3ALPN is the HTTP/3 TLS ALPN.
const h3ALPN = "h3"

// H3 is HTTP/3 CONNECT (ALPN h3). Extra front; raw quic mux uses the same ALPN when this front is off.
type H3 struct {
	cfg   config.Outbound
	token []byte
	offer wire.Cap
	inner *rawSess
	tr    *http3.Transport
}

// NewH3 builds an H3 client that has not yet CONNECT'd.
func NewH3(token string, o config.Outbound, offer wire.Cap) *H3 {
	return &H3{cfg: o, token: []byte(token), offer: offer}
}

// Session returns the inner mux session.
func (h *H3) Session() *mux.Session {
	if h.inner == nil {
		return nil
	}
	return h.inner.Session()
}

// Done is closed when the CONNECT stream or mux dies.
func (h *H3) Done() <-chan struct{} {
	if h.inner == nil {
		ch := make(chan struct{})
		close(ch)
		return ch
	}
	return h.inner.Done()
}

// Open starts the front if needed and opens a mux stream to target.
func (h *H3) Open(target string) (net.Conn, error) {
	if h.inner == nil {
		if err := h.Start(context.Background()); err != nil {
			return nil, err
		}
	}
	return h.inner.Open(target)
}

// Park drops CONNECT without Leave or mux.Close.
func (h *H3) Park() error {
	var err error
	if h.inner != nil {
		err = h.inner.Park()
	}
	if h.tr != nil {
		_ = h.tr.Close()
	}
	return err
}

// Close sends CmdLeave and closes the CONNECT stream.
func (h *H3) Close() error {
	var err error
	if h.inner != nil {
		err = h.inner.Close()
	}
	if h.tr != nil {
		_ = h.tr.Close()
	}
	return err
}

// Start dials QUIC and issues HTTP/3 CONNECT.
// The QUIC Initial carries the loft mark (dialQUIC) so a decoy node routes
// it to loft. Once the node answers 200, the request and response bodies
// carry the raw mux.
func (h *H3) Start(ctx context.Context) error {
	addr, sni, insecure, err := resolveTLS(h.cfg)
	if err != nil {
		return err
	}
	path := h.cfg.Path
	if path == "" {
		path = "/api/v1/tunnel"
	}
	if sni == "" {
		sni = addr
	}
	tlsCfg := &tls.Config{
		ServerName:         sni,
		InsecureSkipVerify: insecure,
		NextProtos:         []string{h3ALPN},
		MinVersion:         tls.VersionTLS13,
	}
	dialCtx := ctx
	tr := &http3.Transport{
		TLSClientConfig: tlsCfg,
		QUICConfig:      quicCfg(),
		// URL host is SNI (often without :444). Dial the outbound addr.
		Dial: func(_ context.Context, _ string, tc *tls.Config, qc *quic.Config) (*quic.Conn, error) {
			if tc == nil {
				tc = tlsCfg
			}
			c, err := dialQUIC(dialCtx, addr, h.token, tc, qc)
			if err != nil {
				return nil, err
			}
			noteQUICKEX(c)
			return c, nil
		},
	}
	pr, pw := io.Pipe()
	// Handshake ctx must not own the CONNECT stream: loft-probe cancels it
	// after Start, which would RST a live mux.
	req, err := http.NewRequestWithContext(context.Background(), http.MethodConnect, "https://"+sni+path, pr)
	if err != nil {
		_ = tr.Close()
		return err
	}
	req.Host = sni
	req.Proto = "loft"
	type rt struct {
		resp *http.Response
		err  error
	}
	ch := make(chan rt, 1)
	go func() {
		resp, err := tr.RoundTrip(req)
		ch <- rt{resp, err}
	}()
	var resp *http.Response
	select {
	case <-ctx.Done():
		_ = pw.Close()
		_ = tr.Close()
		return ctx.Err()
	case r := <-ch:
		resp, err = r.resp, r.err
	}
	if err != nil {
		_ = pw.Close()
		_ = tr.Close()
		return fmt.Errorf("h3 connect: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		_ = pw.Close()
		_ = resp.Body.Close()
		_ = tr.Close()
		return fmt.Errorf("h3 connect: status %d", resp.StatusCode)
	}
	conn := &pipeConn{Reader: resp.Body, Writer: pw, closer: func() error {
		_ = pw.Close()
		return resp.Body.Close()
	}}
	inner, err := startRaw(ctx, conn, h.token, h.offer, addr, sni, h.cfg, wire.FrontH3)
	if err != nil {
		_ = tr.Close()
		return err
	}
	h.tr = tr
	h.inner = inner
	return nil
}

// mustAddr is host:port of o.URL, or empty on parse error.
func mustAddr(o config.Outbound) string {
	a, err := outboundAddr(o)
	if err != nil {
		return ""
	}
	return a
}

// ListenUDP serves quic mux and/or h3 CONNECT on one UDP socket. ALPN h3 wins CONNECT.
func ListenUDP(addr, certFile, keyFile string, info NodeInfo, fronts []string) error {
	pc, err := listenUDPSticky(addr)
	if err != nil {
		return err
	}
	tr := &quic.Transport{Conn: pc}
	return serveUDP(certFile, keyFile, info, fronts, func(tc *tls.Config) (*quic.Listener, error) {
		return tr.Listen(tc, quicCfg())
	})
}

// udpFronts reports whether the quic and h3 fronts are on.
func udpFronts(fronts []string) (wantQ, wantH3 bool) {
	for _, f := range fronts {
		if f == wire.FrontQUIC {
			wantQ = true
		}
		if f == wire.FrontH3 {
			wantH3 = true
		}
	}
	return wantQ, wantH3
}

// serveUDP accepts QUIC from listen and serves quic mux or h3 CONNECT.
// listen builds the QUIC listener: on a plain socket (ListenUDP) or on the
// loft side of the decoy demux (ListenUDPDecoy).
func serveUDP(certFile, keyFile string, info NodeInfo, fronts []string, listen func(*tls.Config) (*quic.Listener, error)) error {
	wantQ, wantH3 := udpFronts(fronts)
	if !wantQ && !wantH3 {
		return nil
	}
	cert, err := tls.LoadX509KeyPair(certFile, keyFile)
	if err != nil {
		return err
	}
	rawALPN := info.QuicALPN
	if rawALPN == "" {
		rawALPN = quicALPN
	}
	var protos []string
	if wantH3 {
		protos = append(protos, h3ALPN)
	}
	if wantQ && (!wantH3 || rawALPN != h3ALPN) {
		protos = append(protos, rawALPN)
	}
	tlsCfg := &tls.Config{Certificates: []tls.Certificate{cert}, NextProtos: protos, MinVersion: tls.VersionTLS13}
	ln, err := listen(tlsCfg)
	if err != nil {
		return err
	}
	h3s := &http3.Server{Handler: newH3Handler(info), TLSConfig: tlsCfg}
	seen := ecrypto.NewNonces()
	for {
		c, err := ln.Accept(context.Background())
		if err != nil {
			return err
		}
		alpn := c.ConnectionState().TLS.NegotiatedProtocol
		switch {
		case wantH3 && wantQ && alpn == h3ALPN:
			go serveH3OrQUIC(c, h3s, seen, info)
		case wantH3 && alpn == h3ALPN:
			go func(c *quic.Conn) { _ = h3s.ServeQUICConn(c) }(c)
		default:
			go serveQUIC(c, seen, info)
		}
	}
}

// serveH3OrQUIC tells the two fronts apart when both use ALPN h3: the
// quic front's first stream starts with a 4-byte length (first byte 0, a
// message is at most 1 MiB), an HTTP/3 request with a HEADERS frame (1).
// SETTINGS go out first: an HTTP/3 client waits for them before CONNECT.
// The quic front never accepts that stream.
func serveH3OrQUIC(c *quic.Conn, h3s *http3.Server, seen *ecrypto.Nonces, info NodeInfo) {
	hc, err := h3s.NewRawServerConn(c)
	if err != nil {
		c.CloseWithError(0, "")
		return
	}
	st, err := c.AcceptStream(c.Context())
	if err != nil {
		return
	}
	_ = st.SetReadDeadline(time.Now().Add(10 * time.Second))
	first := make([]byte, 1)
	if _, err := st.Peek(first); err != nil {
		c.CloseWithError(0, "")
		return
	}
	_ = st.SetReadDeadline(time.Time{})
	if first[0] == 0 {
		serveQUICStream(c, st, seen, info)
		return
	}
	go func() {
		for {
			us, err := c.AcceptUniStream(context.Background())
			if err != nil {
				return
			}
			go hc.HandleUnidirectionalStream(us)
		}
	}()
	for {
		go hc.HandleRequestStream(st)
		if st, err = c.AcceptStream(context.Background()); err != nil {
			return
		}
	}
}

// newH3Handler answers HTTP/3 CONNECT with the same H2Server path and raw mux.
func newH3Handler(info NodeInfo) http.Handler {
	hs := &H2Server{Info: info, Path: "/api/v1/tunnel", Front: wire.FrontH3}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if hs.Match(r) {
			hs.ServeHTTP(w, r)
			return
		}
		http.NotFound(w, r)
	})
}

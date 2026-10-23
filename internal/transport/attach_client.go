package transport

// Client attach: after GET Status.ticket, open h2/ws/quic/h3 with 0x04.

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"strings"

	"github.com/coder/websocket"
	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"

	"github.com/a3app/loft/internal/config"
	ecrypto "github.com/a3app/loft/internal/crypto"
	"github.com/a3app/loft/internal/wire"
)

type pairCloser struct {
	io.ReadWriteCloser
	extra io.Closer
}

func (p pairCloser) Close() error {
	err := p.ReadWriteCloser.Close()
	if p.extra != nil {
		_ = p.extra.Close()
	}
	return err
}

func (g *GET) closeAttach() {
	g.attachOff.Store(true)
	g.dropToGET.Store(true)
	g.attachMu.Lock()
	c := g.attach
	g.attach = nil
	g.attachMu.Unlock()
	if c != nil {
		_ = c.Close()
	}
	g.attachWG.Wait()
	g.attachLive.Store(false)
}

func (g *GET) ticketSID() string {
	if len(g.ticket) > 0 {
		return string(g.ticket)
	}
	return g.sid
}

func (g *GET) attachStopped() bool {
	if g == nil || g.sess == nil || g.sess.Closed() {
		return true
	}
	select {
	case <-g.done:
		return true
	default:
		return false
	}
}

func (g *GET) unbindAttach(c io.Closer) {
	g.attachMu.Lock()
	mine := g.attach == c
	if mine {
		g.attach = nil
	}
	if mine || g.attach == nil {
		g.attachLive.Store(false)
	}
	g.attachMu.Unlock()
	if !mine || g.attachOff.Load() || g.attachStopped() {
		return
	}
	go g.attachExtras(context.Background())
}

// attachExtras tries one extra front on the live GET session. Failures stay on GET.
func (g *GET) attachExtras(ctx context.Context) {
	if g == nil || !g.caps.Has(wire.CapTicket) || len(g.ticket) == 0 || len(g.key) == 0 {
		return
	}
	if g.attachStopped() || g.attachLive.Load() {
		return
	}
	if !g.attaching.CompareAndSwap(false, true) {
		return
	}
	defer g.attaching.Store(false)
	if g.attachStopped() || g.attachLive.Load() {
		return
	}
	for _, f := range []string{wire.FrontH2, wire.FrontWS, wire.FrontQUIC, wire.FrontH3} {
		if !g.caps.Has(wire.CapForFront(f)) || !g.offer.Has(wire.CapForFront(f)) {
			continue
		}
		if err := g.attachFront(ctx, f); err == nil {
			return
		}
		if g.attachStopped() {
			return
		}
	}
}

func (g *GET) attachFront(ctx context.Context, front string) error {
	o := attachOutbound(g.cfg, front)
	switch front {
	case wire.FrontH2:
		parent, stream, _, _, err := dialH2CONNECT(ctx, o, g.token)
		if err != nil {
			return err
		}
		return g.runAttach(ctx, pairCloser{ReadWriteCloser: stream, extra: parent}, front)
	case wire.FrontWS:
		return g.attachWS(ctx, o)
	case wire.FrontQUIC:
		return g.attachQUIC(ctx, o)
	case wire.FrontH3:
		return g.attachH3(ctx, o)
	default:
		return fmt.Errorf("attach %s: not a mux front", front)
	}
}

func attachOutbound(get config.Outbound, front string) config.Outbound {
	o := get
	o.Type = front
	u := get.URL
	u = strings.TrimSuffix(u, "/")
	switch front {
	case wire.FrontH2, wire.FrontH3:
		o.Path = "/api/v1/tunnel"
		o.URL = u
	case wire.FrontWS:
		o.Path = "/api/v1/sync"
		host := strings.TrimPrefix(strings.TrimPrefix(u, "https://"), "http://")
		host = strings.TrimPrefix(strings.TrimPrefix(host, "wss://"), "ws://")
		if strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "wss://") {
			o.URL = "wss://" + host + "/api/v1/sync"
		} else {
			o.URL = "ws://" + host + "/api/v1/sync"
		}
	case wire.FrontQUIC:
		o.Path = ""
		o.URL = u
	}
	return o
}

func (g *GET) runAttach(ctx context.Context, conn io.ReadWriteCloser, front string) error {
	if g.attachStopped() {
		conn.Close()
		return fmt.Errorf("attach: closed")
	}
	blob, err := ecrypto.MakeAttach(g.key, g.ticketSID(), front)
	if err != nil {
		conn.Close()
		return err
	}
	if err := writeMsg(conn, blob); err != nil {
		conn.Close()
		return err
	}
	raw, err := readMsg(conn)
	if err != nil {
		conn.Close()
		return err
	}
	plain, err := ecrypto.Open(g.key, raw, nil)
	if err != nil {
		conn.Close()
		return err
	}
	frames, err := wire.DecodeBatch(plain)
	if err != nil {
		conn.Close()
		return err
	}
	rest, got := wire.SplitFront(frames)
	if got != front {
		conn.Close()
		return fmt.Errorf("attach ack %q", got)
	}
	if g.attachStopped() {
		conn.Close()
		return fmt.Errorf("attach: closed")
	}
	g.attachMu.Lock()
	old := g.attach
	g.attach = conn
	g.attachMu.Unlock()
	if old != nil {
		_ = old.Close()
		g.attachWG.Wait()
	}
	g.attachOff.Store(false)
	g.attachLive.Store(true)
	g.dropToGET.Store(false)
	if len(rest) > 0 {
		g.sess.Push(rest)
	}
	done := make(chan struct{})
	g.attachWG.Add(1)
	go func() {
		defer close(done)
		g.attachRead(conn, readMsg)
		_ = conn.Close()
	}()
	go func() {
		defer g.attachWG.Done()
		pumpAttach(g.sess, func(b []byte) error { return writeMsg(conn, b) }, g.key, done)
		g.unbindAttach(conn)
	}()
	return nil
}

func (g *GET) attachRead(conn io.Reader, read func(io.Reader) ([]byte, error)) {
	for {
		raw, err := read(conn)
		if err != nil {
			return
		}
		plain, err := ecrypto.Open(g.key, raw, nil)
		if err != nil {
			continue
		}
		frames, err := wire.DecodeBatch(plain)
		if err != nil {
			continue
		}
		frames, bulk := wire.SplitFront(frames)
		if bulk == wire.FrontGET {
			return
		}
		if len(frames) > 0 {
			g.sess.Push(frames)
		}
		if g.sess.Closed() {
			return
		}
	}
}

func (g *GET) attachWS(ctx context.Context, o config.Outbound) error {
	u, err := parseURL(o.URL)
	if err != nil {
		return err
	}
	addr, sni, insecure, err := resolveTLS(o)
	if err != nil {
		return err
	}
	opts := &websocket.DialOptions{
		HTTPHeader: http.Header{
			"User-Agent": []string{chromeUA},
			"Origin":     []string{"https://" + sni},
		},
		CompressionMode: websocket.CompressionDisabled,
	}
	if u.Scheme == "wss" || u.Scheme == "https" {
		opts.HTTPClient = &http.Client{
			Transport: &http.Transport{
				ForceAttemptHTTP2: false,
				TLSClientConfig: &tls.Config{
					ServerName:         sni,
					InsecureSkipVerify: insecure,
					NextProtos:         []string{"http/1.1"},
				},
				DialTLSContext: func(context.Context, string, string) (net.Conn, error) {
					return dialTLSALPN(addr, sni, o.Fingerprint, insecure, g.token, []string{"http/1.1"})
				},
			},
		}
	}
	c, _, err := websocket.Dial(ctx, o.URL, opts)
	if err != nil {
		return err
	}
	c.SetReadLimit(2 << 20)
	blob, err := ecrypto.MakeAttach(g.key, g.ticketSID(), wire.FrontWS)
	if err != nil {
		c.Close(websocket.StatusInternalError, "auth")
		return err
	}
	if err := c.Write(ctx, websocket.MessageBinary, blob); err != nil {
		c.Close(websocket.StatusInternalError, "write")
		return err
	}
	_, raw, err := c.Read(ctx)
	if err != nil {
		c.Close(websocket.StatusInternalError, "read")
		return err
	}
	plain, err := ecrypto.Open(g.key, raw, nil)
	if err != nil {
		c.Close(websocket.StatusPolicyViolation, "no")
		return err
	}
	frames, err := wire.DecodeBatch(plain)
	if err != nil {
		c.Close(websocket.StatusPolicyViolation, "no")
		return err
	}
	rest, got := wire.SplitFront(frames)
	if got != wire.FrontWS {
		c.Close(websocket.StatusPolicyViolation, "no")
		return fmt.Errorf("attach ack %q", got)
	}
	live := context.Background()
	wrap := &wsAttach{
		write: func(p []byte) error { return c.Write(live, websocket.MessageBinary, p) },
		read: func() ([]byte, error) {
			_, b, err := c.Read(live)
			return b, err
		},
		close: func() error { return c.Close(websocket.StatusNormalClosure, "") },
	}
	if g.attachStopped() {
		_ = wrap.Close()
		return fmt.Errorf("attach: closed")
	}
	g.attachMu.Lock()
	old := g.attach
	g.attach = wrap
	g.attachMu.Unlock()
	if old != nil {
		_ = old.Close()
		g.attachWG.Wait()
	}
	g.attachOff.Store(false)
	g.attachLive.Store(true)
	g.dropToGET.Store(false)
	if len(rest) > 0 {
		g.sess.Push(rest)
	}
	done := make(chan struct{})
	g.attachWG.Add(1)
	go func() {
		defer close(done)
		g.attachRead(&wsReader{read: wrap.ReadBinary}, func(r io.Reader) ([]byte, error) {
			return r.(*wsReader).read()
		})
		_ = wrap.Close()
	}()
	go func() {
		defer g.attachWG.Done()
		pumpAttach(g.sess, wrap.WriteBinary, g.key, done)
		g.unbindAttach(wrap)
	}()
	return nil
}

type wsReader struct {
	read func() ([]byte, error)
}

func (w *wsReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

func (g *GET) attachQUIC(ctx context.Context, o config.Outbound) error {
	addr, sni, insecure, err := resolveTLS(o)
	if err != nil {
		return err
	}
	tc := &tls.Config{
		ServerName:         sni,
		InsecureSkipVerify: insecure,
		NextProtos:         []string{quicALPN},
		MinVersion:         tls.VersionTLS13,
		ClientSessionCache: quicSess,
	}
	conn, err := dialQUIC(ctx, addr, g.token, tc, quicCfg())
	if err != nil {
		return fmt.Errorf("attach quic: %w", err)
	}
	st, err := conn.OpenStreamSync(ctx)
	if err != nil {
		_ = conn.CloseWithError(0, "stream")
		return err
	}
	return g.runAttach(ctx, pairCloser{ReadWriteCloser: st, extra: quicCloser{conn}}, wire.FrontQUIC)
}

type quicCloser struct{ c *quic.Conn }

func (q quicCloser) Close() error { return q.c.CloseWithError(0, "") }

func (g *GET) attachH3(ctx context.Context, o config.Outbound) error {
	addr, sni, insecure, err := resolveTLS(o)
	if err != nil {
		return err
	}
	path := o.Path
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
		Dial: func(_ context.Context, _ string, tc *tls.Config, qc *quic.Config) (*quic.Conn, error) {
			if tc == nil {
				tc = tlsCfg
			}
			return dialQUIC(dialCtx, addr, g.token, tc, qc)
		},
	}
	pr, pw := io.Pipe()
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
		return fmt.Errorf("attach h3: %w", err)
	}
	if resp.StatusCode != http.StatusOK {
		_ = pw.Close()
		_ = resp.Body.Close()
		_ = tr.Close()
		return fmt.Errorf("attach h3: status %d", resp.StatusCode)
	}
	stream := &pipeConn{Reader: resp.Body, Writer: pw, closer: func() error {
		_ = pw.Close()
		return resp.Body.Close()
	}}
	return g.runAttach(ctx, pairCloser{ReadWriteCloser: stream, extra: tr}, wire.FrontH3)
}

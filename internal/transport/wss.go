package transport

// WebSocket front: TLS, HTTP upgrade, then auth / AEAD(Status) / AEAD(mux).

import (
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/coder/websocket"

	"github.com/a3app/loft/internal/config"
	ecrypto "github.com/a3app/loft/internal/crypto"
	"github.com/a3app/loft/internal/mux"
	"github.com/a3app/loft/internal/netconf"
	"github.com/a3app/loft/internal/wire"
)

// WSS is the generic CDN profile (Cloudflare and anything that passes WebSocket).
type WSS struct {
	cfg     config.Outbound
	token   []byte
	sess    *mux.Session
	conn    *websocket.Conn
	key     []byte
	holdGen uint64
	done    chan struct{}
	dead    sync.Once
	mu      sync.Mutex
	closed  bool
	offer   wire.Cap
}

// NewWSS builds a WebSocket client that has not yet dialed.
func NewWSS(token string, o config.Outbound) *WSS {
	sess, gen := takeClientHold()
	return &WSS{cfg: o, token: []byte(token), sess: sess, holdGen: gen, done: make(chan struct{})}
}

// Start dials WebSocket, writes auth, reads AEAD(Status), and starts mux loops.
func (w *WSS) Start(ctx context.Context) error {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.conn != nil {
		return nil
	}
	u, err := parseURL(w.cfg.URL)
	if err != nil {
		return err
	}
	addr, sni, insecure, err := resolveTLS(w.cfg)
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
				DialTLSContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
					return dialTLSALPN(addr, sni, w.cfg.Fingerprint, insecure, w.token, []string{"http/1.1"})
				},
			},
		}
	}
	wsURL := w.cfg.URL
	if strings.HasPrefix(wsURL, "https://") {
		wsURL = "wss://" + strings.TrimPrefix(wsURL, "https://")
	}
	if strings.HasPrefix(wsURL, "http://") {
		wsURL = "ws://" + strings.TrimPrefix(wsURL, "http://")
	}
	c, _, err := websocket.Dial(ctx, wsURL, opts)
	if err != nil {
		return fmt.Errorf("wss dial: %w", err)
	}
	c.SetReadLimit(2 << 20)
	hello := newClientHello(w.offer)
	proof, nonce, sk, err := ecrypto.MakeAuth(w.token)
	if err != nil {
		c.Close(websocket.StatusInternalError, "auth")
		return err
	}
	auth := append(proof, hello.Encode()...)
	if err := c.Write(ctx, websocket.MessageBinary, auth); err != nil {
		c.Close(websocket.StatusInternalError, "write")
		return err
	}
	_, stRaw, err := c.Read(ctx)
	if err != nil {
		c.Close(websocket.StatusInternalError, "status")
		return err
	}
	key, plain, err := ecrypto.OpenHello(sk, nonce, stRaw, nil)
	if err != nil {
		c.Close(websocket.StatusInternalError, "status")
		return err
	}
	st, err := wire.DecodeStatus(plain)
	if err != nil {
		return err
	}
	if !st.Code.OK() {
		return wire.Fail(st.Code, st.Program)
	}
	RememberVis(st)
	w.sess.SetHold(st.Caps.Has(wire.CapHold))
	if err := adoptDecoy(addr, sni, st, strings.TrimSpace(w.cfg.CDN) == ""); err != nil {
		c.Close(websocket.StatusNormalClosure, "decoy")
		return err
	}
	w.conn = c
	w.key = key
	go w.readLoop()
	go w.writeLoop()
	return nil
}

// Session returns the live mux session.
func (w *WSS) Session() *mux.Session { return w.sess }

// Done is closed when the socket or mux dies.
func (w *WSS) Done() <-chan struct{} { return w.done }

// die closes Done once.
func (w *WSS) die() { w.dead.Do(func() { close(w.done) }) }

// Open starts the front if needed and opens a mux stream to target.
func (w *WSS) Open(target string) (net.Conn, error) {
	w.mu.Lock()
	need := w.conn == nil && !w.closed
	w.mu.Unlock()
	if need {
		if err := w.Start(context.Background()); err != nil {
			return nil, err
		}
	}
	return w.sess.Open(target)
}

// Park drops the WebSocket without Leave or mux.Close. Streams stay for gap_hold.
func (w *WSS) Park() error {
	w.mu.Lock()
	w.closed = true
	conn := w.conn
	w.conn = nil
	w.mu.Unlock()
	parkClient(w.sess, "", nil, 0)
	w.die()
	if conn != nil {
		return conn.Close(websocket.StatusNormalClosure, "")
	}
	return nil
}

// Close sends CmdLeave and closes the WebSocket.
func (w *WSS) Close() error {
	w.mu.Lock()
	w.closed = true
	conn := w.conn
	key := w.key
	w.mu.Unlock()
	if conn != nil && len(key) > 0 {
		if sealed, err := sealLeave(key, nil); err == nil {
			ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
			_ = conn.Write(ctx, websocket.MessageBinary, sealed)
			cancel()
		}
	}
	if releaseClientHold(w.holdGen) {
		w.sess.Close()
	}
	w.die()
	if conn != nil {
		return conn.Close(websocket.StatusNormalClosure, "")
	}
	return nil
}

// readLoop decrypts binary messages and pushes them into mux.
func (w *WSS) readLoop() {
	ctx := context.Background()
	for {
		_, data, err := w.conn.Read(ctx)
		if err != nil {
			if w.sess.Holding() {
				w.die()
				return
			}
			w.sess.Close()
			w.die()
			return
		}
		plain, err := ecrypto.Open(w.key, data, nil)
		if err != nil {
			continue
		}
		frames, err := wire.DecodeBatch(plain)
		if err != nil {
			continue
		}
		w.sess.Push(frames)
	}
}

// wsPullBytes is one WebSocket binary message. 512 KiB HOL'd inner TCP ACKs
// and live download fell to ~7 Mbit while upload (no inner ACK wait) hit ~27.
const wsPullBytes = 48 << 10

// wsPriBytes is a WS message that is only control/small Data so a loaded
// ping is not stuck behind 96 KiB of already-sealed bulk.
const wsPriBytes = 8 << 10

// wsPrefetch is sealed messages prepared while a Write is in flight.
// 6×96 KiB was ~2 s of loaded download latency at 8 Mbit (speedtest ↓2057 ms).
const wsPrefetch = 2

// writeLoop Pulls mux, seals, and writes binary messages. Empty batches are not sent.
func (w *WSS) writeLoop() {
	ctx := context.Background()
	pumpWS(w.sess, time.Second, w.done, w.closedConn, func(frames []wire.Frame) ([]byte, error) {
		raw, err := wire.EncodeBatch(frames)
		if err != nil {
			return nil, err
		}
		return ecrypto.Seal(w.key, raw, nil)
	}, func(sealed []byte) error {
		return w.conn.Write(ctx, websocket.MessageBinary, sealed)
	})
	w.die()
}

// pumpWS prefetches a few sealed WS messages. Control/small Data is pulled
// into its own short message so a loaded ping is not stuck behind bulk.
func pumpWS(sess *mux.Session, pullWait time.Duration, done <-chan struct{}, dead func() bool, seal func([]wire.Frame) ([]byte, error), write func([]byte) error) {
	out := make(chan []byte, wsPrefetch)
	go func() {
		defer close(out)
		for {
			if dead() {
				return
			}
			max, wait := wsPullBytes, pullWait
			if sess.PriQueued() > 0 {
				max = wsPriBytes
				wait = 0
			}
			var frames []wire.Frame
			if DraftPaused() {
				frames = sess.PullPri(max, wait)
			} else {
				frames = sess.Pull(max, wait)
			}
			if dead() {
				return
			}
			if len(frames) == 0 {
				continue
			}
			sealed, err := seal(frames)
			if err != nil {
				continue
			}
			select {
			case out <- sealed:
			case <-done:
				return
			}
		}
	}()
	for sealed := range out {
		if err := write(sealed); err != nil {
			return
		}
	}
}

// closedConn reports whether Close has been called.
func (w *WSS) closedConn() bool {
	w.mu.Lock()
	defer w.mu.Unlock()
	return w.closed
}

// WSSServer is the origin for the WebSocket front.
type WSSServer struct {
	Token []byte
	Decoy []string
	Info  NodeInfo
	Seen  *ecrypto.Nonces
}

// ServeHTTP upgrades, authenticates message 1, writes AEAD(Status), and relays mux.
func (s *WSSServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	c, err := websocket.Accept(w, r, &websocket.AcceptOptions{
		InsecureSkipVerify: true,
		OriginPatterns:     []string{"*"},
		CompressionMode:    websocket.CompressionDisabled,
	})
	if err != nil {
		return
	}
	defer c.Close(websocket.StatusNormalClosure, "")
	c.SetReadLimit(2 << 20)
	ctx := r.Context()
	_, auth, err := c.Read(ctx)
	if err != nil {
		return
	}
	if ecrypto.LooksLikeAttach(auth) {
		live := context.Background()
		wrap := wsAttach{
			write: func(p []byte) error { return c.Write(live, websocket.MessageBinary, p) },
			read: func() ([]byte, error) {
				_, b, err := c.Read(live)
				return b, err
			},
			close: func() error { return c.Close(websocket.StatusNormalClosure, "") },
		}
		serveAttach(wrap, auth, wire.FrontWS)
		return
	}
	if s.Seen == nil {
		s.Seen = ecrypto.NewNonces()
	}
	a, err := frontAuth(s.Token, auth, s.Seen)
	if err != nil || a.Replay {
		if err != nil {
			if hello, e2 := sealAuthReject(auth, nil); e2 == nil {
				_ = c.Write(ctx, websocket.MessageBinary, hello)
			}
		}
		c.Close(websocket.StatusPolicyViolation, "no")
		return
	}
	ms, reused := adoptServerMux(a.Rest, a.Token)
	st := s.Info.Status("ws")
	if len(s.Info.Token) == 0 {
		st = withDecoy(netconf.DefaultSession().Status(wire.OK, "", "", "", 1280, 0), s.Decoy)
	}
	finishStatus(&st, a.Token, "ws", a.Rest, ms, remoteAddr(r.RemoteAddr))
	sealed, err := ecrypto.Seal(a.Key, st.Encode(), nil)
	if err != nil {
		return
	}
	hello := ecrypto.WrapHello(a.EphN, sealed)
	if err := c.Write(ctx, websocket.MessageBinary, hello); err != nil {
		return
	}
	key := a.Key
	if !reused {
		go serveMux(ms)
		go serveDgrams(ms)
	}
	go pumpWS(ms, 4*time.Millisecond, ctx.Done(), func() bool { return ctx.Err() != nil }, func(frames []wire.Frame) ([]byte, error) {
		raw, err := wire.EncodeBatch(frames)
		if err != nil {
			return nil, err
		}
		return ecrypto.Seal(key, raw, nil)
	}, func(sealed []byte) error {
		return c.Write(ctx, websocket.MessageBinary, sealed)
	})
	for {
		_, data, err := c.Read(ctx)
		if err != nil {
			dropServerMux(ms)
			return
		}
		plain, err := ecrypto.Open(key, data, nil)
		if err != nil {
			continue
		}
		frames, err := wire.DecodeBatch(plain)
		if err != nil {
			continue
		}
		ms.Push(frames)
	}
}

// serveMux Accepts mux streams and proxies each one until the session ends.
func serveMux(ms *mux.Session) {
	ov := NetSwitch()
	defer func() {
		if ov != nil {
			ov.Detach(ms)
		}
	}()
	for {
		st, err := ms.Accept()
		if err != nil {
			return
		}
		go proxyStream(st)
	}
}

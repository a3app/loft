package transport

// WebSocket front: TLS, HTTP upgrade, then auth / AEAD(Status) / AEAD(mux).
//
// The client opens an ordinary WebSocket (HTTP/1.1 upgrade over TLS) and
// sends the 0x02 handshake plus Hello as the first binary message. The node
// answers with the Status sealed (AEAD-encrypted) under the session key;
// after that every binary message is one sealed batch of mux frames. Many
// CDNs pass WebSocket through, so this front also works behind a CDN.
//
// This file also holds the write pump shared by the stream fronts (ws,
// quic, h2, h3) and pullGate, which lets attached bulk lanes take over Pull.

import (
	"context"
	"crypto/tls"
	"fmt"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
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
	lanes   *GET
	gate    pullGate
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
					return dialTLSALPNContext(ctx, addr, sni, w.cfg.Fingerprint, insecure, w.token, []string{"http/1.1"})
				},
			},
		}
	}
	wsURL, err := wsDialURL(w.cfg.URL)
	if err != nil {
		return err
	}
	if !isLocalUrl(wsURL) {
		return fmt.Errorf("ws url")
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
		return afterProof(err)
	}
	key, plain, err := ecrypto.OpenHello(sk, nonce, stRaw, nil)
	if err != nil {
		c.Close(websocket.StatusInternalError, "status")
		return err
	}
	st, err := wire.DecodeStatus(plain)
	if err != nil {
		c.Close(websocket.StatusInternalError, "status")
		return err
	}
	if !st.Code.OK() {
		// Same as QUIC: the node is already in its read loop. Close so the
		// next front can admit instead of sitting behind this socket.
		c.Close(websocket.StatusInternalError, "status")
		return wire.Fail(st.Code, st.Program)
	}
	RememberVis(st)
	w.sess.SetHold(st.Caps.Has(wire.CapHold))
	takeInner(wire.FrontWS, key, st)
	takeCC(st, nil)
	if err := adoptDecoy(addr, sni, st, strings.TrimSpace(w.cfg.CDN) == ""); err != nil {
		c.Close(websocket.StatusNormalClosure, "decoy")
		return err
	}
	w.conn = c
	w.key = key
	w.lanes = startBulkLanes(ctx, wire.FrontWS, w.cfg, w.token, key, st, w.offer, w.sess, w.done, func(b *GET) {
		w.gate.handover()
		b.adoptLane(noopCloser{}, func(p []byte) error {
			return c.Write(context.Background(), websocket.MessageBinary, p)
		}, wire.FrontWS)
	}, func() { w.gate.resume() })
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
// gap_hold (Status gap_hold_ms) is how long the node keeps a parked mux.
func (w *WSS) Park() error {
	if w.lanes != nil {
		w.lanes.closeAttach()
	}
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
	if w.lanes != nil {
		w.lanes.closeAttach()
	}
	w.mu.Lock()
	w.closed = true
	conn := w.conn
	key := w.key
	w.mu.Unlock()
	if holdOwner(w.holdGen) && conn != nil && len(key) > 0 {
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
		ingestPlain(w.sess, plain)
	}
}

// wsPullBytes is one WebSocket binary message. 512 KiB HOL'd inner TCP ACKs
// and live download fell to ~7 Mbit while upload (no inner ACK wait) hit ~27.
// (HOL is head-of-line blocking: small inner TCP ACKs waited behind a big
// message, so the sender inside the tunnel slowed down.)
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
	}, &w.gate)
	w.die()
}

// pullGate hands Pull from the owner write loop to the lane dispatcher.
// handover blocks until the owner is outside Pull and write.
// The owner is the front that did the 0x02 handshake. When attached bulk
// lanes come up, they take over sending; the mux must have exactly one
// Puller or frames would leave out of order. resume gives Pull back.
type pullGate struct {
	mu    sync.Mutex
	pause atomic.Bool
}

func (g *pullGate) handover() {
	if g == nil {
		return
	}
	g.mu.Lock()
	g.pause.Store(true)
	g.mu.Unlock()
}

func (g *pullGate) resume() {
	if g == nil {
		return
	}
	g.mu.Lock()
	g.pause.Store(false)
	g.mu.Unlock()
}

// pumpWS prefetches a few sealed WS messages. Control/small Data is pulled
// into its own short message so a loaded ping is not stuck behind bulk.
// A non-nil gate runs pull and write on one goroutine under the gate lock
// so 0x04 can take the Puller without a second writer.
// 0x04 is attach: another front joining this session by its ticket. Despite
// the name, quic, h2 and h3 use this pump too.
func pumpWS(sess *mux.Session, pullWait time.Duration, done <-chan struct{}, dead func() bool, seal func([]wire.Frame) ([]byte, error), write func([]byte) error, gate *pullGate) {
	if gate != nil {
		pumpGated(sess, pullWait, done, dead, seal, write, gate)
		return
	}
	out := make(chan []byte, wsPrefetch)
	go func() {
		defer close(out)
		for {
			if dead() {
				return
			}
			max, wait := standPull(), pullWait
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

// pumpGated is pumpWS without the prefetch goroutine. The gate lock covers
// Pull and write, so handover waits out an in-flight batch and then owns Pull.
func pumpGated(sess *mux.Session, pullWait time.Duration, done <-chan struct{}, dead func() bool, seal func([]wire.Frame) ([]byte, error), write func([]byte) error, gate *pullGate) {
	var last time.Time
	var writes, nbytes int
	var maxGap time.Duration
	defer func() {
		if standTrace() && writes > 0 {
			log.Printf("stand pump writes=%d bytes=%d maxgap=%s", writes, nbytes, maxGap.Round(time.Millisecond))
		}
	}()
	for {
		if dead() {
			return
		}
		select {
		case <-done:
			return
		default:
		}
		gate.mu.Lock()
		if gate.pause.Load() {
			gate.mu.Unlock()
			select {
			case <-done:
				return
			case <-time.After(5 * time.Millisecond):
			}
			continue
		}
		max, wait := standPull(), pullWait
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
		if len(frames) > 0 {
			if sealed, err := seal(frames); err == nil {
				standWrote(len(sealed), &last, &writes, &nbytes, &maxGap)
				_ = write(sealed)
			}
		}
		gate.mu.Unlock()
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
// Message 1 may instead be 0x04, which attaches this socket to a live
// session as an extra lane. A held mux of the same client is adopted, so
// streams survive a reconnect.
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
		serveAttach(wrap, auth, wire.FrontWS, nil)
		return
	}
	if s.Seen == nil {
		s.Seen = ecrypto.NewNonces()
	}
	a, err := frontAuth(s.Token, auth, s.Seen)
	if err != nil || a.Replay {
		// No sealed reject: it would confirm loft to a prober. The protocol
		// requires a wrong key to look like an ordinary site's refusal.
		c.Close(websocket.StatusPolicyViolation, "no")
		return
	}
	ms, reused := adoptServerMux(a.Rest, a.Token)
	st := s.Info.Status("ws")
	if len(s.Info.Token) == 0 {
		st = withDecoy(netconf.DefaultSession().Status(wire.OK, "", "", "", 1280, 0), s.Decoy)
	}
	finishStatus(&st, a.Token, "ws", a.Rest, ms, remoteAddr(r.RemoteAddr))
	key := a.Key
	gate := &pullGate{}
	ss := bindTicket(ms, key, wire.FrontWS, func(b []byte) error {
		return c.Write(context.Background(), websocket.MessageBinary, b)
	}, gate)
	if ss != nil {
		st.Ticket = []byte(ss.id)
		defer ss.releaseTicket()
	}
	applyInner(&st, wire.FrontWS, a.Rest)
	sealed, err := ecrypto.Seal(key, st.Encode(), nil)
	if err != nil {
		return
	}
	commitInner(key, st)
	hello := ecrypto.WrapHello(a.EphN, sealed)
	if err := c.Write(ctx, websocket.MessageBinary, hello); err != nil {
		return
	}
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
	}, gate)
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
		ingestPlain(ms, plain)
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

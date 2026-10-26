package transport

// Client attach: after Status.ticket, open the other mux fronts with 0x04.
// The front that finished 0x02 owns the mux. Extra fronts are lanes. The
// owner socket joins the fan once a lane stays up, except a real GET.
//
// Why: GET is the front most likely to get through (it passes a CDN) but
// slow for uplink, since every uplink byte rides a Cookie header. So the client
// logs in over GET and then tries to add faster direct fronts (ws, h2, quic,
// h3) to the same session. If any come up, bulk traffic moves to them; if
// they all drop, GET carries everything again. The lane state lives on the
// GET value; for a session whose owner is not GET, a "shadow" GET value
// holds the lanes and never speaks HTTP.

import (
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/coder/websocket"
	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/http3"

	"github.com/a3app/loft/internal/config"
	ecrypto "github.com/a3app/loft/internal/crypto"
	"github.com/a3app/loft/internal/mux"
	"github.com/a3app/loft/internal/wire"
)

// pairCloser closes a stream and then the connection under it (the HTTP/2
// or QUIC connection that carries a single CONNECT stream).
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

// closeAttach drops every lane and waits for their goroutines. The next
// uplink tells the node to return to GET (dropToGET), and a non-GET owner
// resumes its own write loop (ownerDown).
func (g *GET) closeAttach() {
	g.attachOff.Store(true)
	g.holdDispatch.Store(false)
	g.dropToGET.Store(true)
	g.ownerOnce.Store(false)
	g.attachMu.Lock()
	lanes := g.lanes
	g.lanes = nil
	g.attach = nil
	g.attachMu.Unlock()
	for _, ln := range lanes {
		if ln == nil {
			continue
		}
		ln.dead.Store(true)
		if ln.conn != nil {
			_ = ln.conn.Close()
		}
	}
	g.attachWG.Wait()
	g.attachLive.Store(false)
	noteLiveBulk(g, "")
	if g.ownerDown != nil {
		g.ownerDown()
	}
}

// ticketSID is the session id a 0x04 names: Status.ticket, or the GET sid
// when the node sent no ticket.
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

// unbindAttach marks the lane on socket c dead. When it was the last one
// and nothing else is in progress, bulk returns to the owner and a new
// round of attachExtras starts in the background.
func (g *GET) unbindAttach(c io.Closer) {
	g.attachMu.Lock()
	for _, ln := range g.lanes {
		if ln != nil && ln.conn == c {
			ln.dead.Store(true)
		}
	}
	left := g.liveCountLocked()
	g.attachMu.Unlock()
	if left > 0 || g.holdDispatch.Load() || g.attachOff.Load() || g.attachStopped() {
		if left > 0 {
			noteLiveBulk(g, strings.Join(g.liveFronts(), "+"))
		}
		return
	}
	g.attachLive.Store(false)
	g.dropToGET.Store(true)
	g.ownerOnce.Store(false)
	noteLiveBulk(g, "")
	if g.ownerDown != nil {
		g.ownerDown()
	}
	go g.attachExtras(context.Background())
}

// startBulkLanes opens 0x04 lanes after a non-GET 0x02. The shadow GET is
// only the lane host: it does not speak HTTP. Owner socket joins the fan
// once an extra front stays up, and the owner write loop pauses.
func startBulkLanes(ctx context.Context, front string, cfg config.Outbound, token, key []byte, st wire.Status, offer wire.Cap, sess *mux.Session, done chan struct{}, arm func(*GET), down func()) *GET {
	if standFan() == "off" {
		return nil
	}
	if sess == nil || done == nil || !st.Caps.Has(wire.CapTicket) || !validGETSID(string(st.Ticket)) || len(key) == 0 {
		return nil
	}
	g := &GET{
		cfg:        cfg,
		token:      append([]byte(nil), token...),
		sess:       sess,
		key:        append([]byte(nil), key...),
		ticket:     append([]byte(nil), st.Ticket...),
		caps:       st.Caps,
		offer:      offer,
		fronts:     append([]wire.FrontEP(nil), st.Fronts...),
		done:       done,
		ownerFront: front,
		ownerArm:   arm,
		ownerDown:  down,
	}
	// The dial budget is cancelled when Start returns. Lanes must outlive it
	// and stop only with the session.
	lctx := laneCtx(ctx, done)
	go g.attachExtras(lctx)
	return g
}

// laneCtx is ctx without the dial deadline. It ends when the session does.
func laneCtx(ctx context.Context, done <-chan struct{}) context.Context {
	base := context.Background()
	if ctx != nil {
		base = context.WithoutCancel(ctx)
	}
	out, cancel := context.WithCancel(base)
	if done == nil {
		return out
	}
	go func() {
		select {
		case <-done:
			cancel()
		case <-out.Done():
		}
	}()
	return out
}

// noteLiveBulk publishes the fan state for LiveBulk / LiveLaneCount. An
// empty s clears it, but only if g is the session that set it.
func noteLiveBulk(g *GET, s string) {
	if s == "" {
		if liveOwner.Load() == g {
			liveOwner.Store(nil)
			liveBulk.Store("")
		}
		return
	}
	liveOwner.Store(g)
	liveBulk.Store(s)
}

// LiveBulk is the fan riding the mux ("ws+h2"), or empty when one front Pulls.
func LiveBulk() string {
	s, _ := liveBulk.Load().(string)
	return s
}

// LiveLaneCount is how many sockets are in the fan. Zero before the fan is up.
func LiveLaneCount() int {
	g := liveOwner.Load()
	if g == nil {
		return 0
	}
	return g.liveCount()
}

var (
	liveBulk  atomic.Value
	liveOwner atomic.Pointer[GET]
)

// attachExtras opens every mux front the node listed, on the node URL.
// GET stays up. Bulk moves onto the fronts that stayed up. One stream
// stays on one front. A CDN profile used to skip this because g.cfg.URL
// is the CDN host, which does not speak ws/h2/quic/h3.
func (g *GET) attachExtras(ctx context.Context) {
	if g == nil {
		return
	}
	// dropLostSid clears key under g.mu while this goroutine is in startFront.
	g.mu.Lock()
	caps, ticket, key := g.caps, append([]byte(nil), g.ticket...), append([]byte(nil), g.key...)
	g.mu.Unlock()
	if !caps.Has(wire.CapTicket) || len(ticket) == 0 || len(key) == 0 {
		return
	}
	if g.attachStopped() {
		return
	}
	if !g.attaching.CompareAndSwap(false, true) {
		return
	}
	defer g.attaching.Store(false)
	if g.attachStopped() || g.attachLive.Load() {
		return
	}
	g.holdDispatch.Store(true)
	defer func() {
		g.holdDispatch.Store(false)
		g.finishAttach()
	}()
	copies := standCopies()
	for _, f := range g.attachNames() {
		// Extra copies are all the owner's front. A second front kind in the
		// round-robin makes reorder wait on the slow socket.
		if copies > 1 && f != g.ownerFront {
			continue
		}
		if !caps.Has(wire.CapForFront(f)) || !g.offer.Has(wire.CapForFront(f)) {
			continue
		}
		o := attachOutbound(g.cfg, f, g.fronts)
		if !markReady(o) {
			continue
		}
		actx, cancel := context.WithTimeout(ctx, 4*time.Second)
		err := g.attachFront(actx, f)
		cancel()
		if err != nil && g.attachStopped() {
			return
		}
	}
	g.dialCopies(ctx, copies)
	// A node that still keeps one 0x04 closes the previous link inside
	// setAttach, after the new ack. Readers need a moment to see EOF
	// before the dispatcher Pulls onto a conn the node already dropped.
	if g.liveCount() > 0 {
		time.Sleep(100 * time.Millisecond)
		g.armOwnerLane()
	}
}

// dialCopies opens extra sockets of the owner front so one mux can fill a
// path that gives each TCP only a slice of the rate. The owner socket counts
// as the first copy. Dials run together; sequential handshakes on a 190 ms
// path would still be in progress when the bench starts.
func (g *GET) dialCopies(ctx context.Context, copies int) {
	if g == nil || copies <= 1 || g.ownerFront == "" {
		return
	}
	n := copies - 1
	if g.ownerFront != wire.FrontWS && g.ownerFront != wire.FrontH2 {
		return
	}
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		if g.attachStopped() {
			break
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			o := attachOutbound(g.cfg, g.ownerFront, g.fronts)
			if !markReady(o) {
				return
			}
			actx, cancel := context.WithTimeout(ctx, 8*time.Second)
			defer cancel()
			var err error
			switch g.ownerFront {
			case wire.FrontWS:
				err = g.attachWS(actx, o)
			default:
				err = g.attachFront(actx, g.ownerFront)
			}
			if err != nil {
				log.Printf("attach copy %s: %v", g.ownerFront, err)
			}
		}()
	}
	wg.Wait()
}

// armOwnerLane pauses the owner write loop and puts that socket in the fan.
// A real GET leaves ownerArm nil so the CDN socket stays off the fan.
func (g *GET) armOwnerLane() {
	if g == nil || g.ownerArm == nil || g.liveCount() == 0 {
		return
	}
	if !g.ownerOnce.CompareAndSwap(false, true) {
		return
	}
	g.ownerArm(g)
}

// finishAttach ends one attachExtras round: if any lane is up, the
// dispatcher starts and bulk moves onto the lanes.
func (g *GET) finishAttach() {
	if g.attachStopped() || g.attachOff.Load() || g.liveCount() == 0 {
		return
	}
	g.attachLive.Store(true)
	g.dropToGET.Store(false)
	g.kickDispatch()
	if names := g.liveFronts(); len(names) > 0 {
		noteLiveBulk(g, strings.Join(names, "+"))
		log.Printf("attach %s", strings.Join(names, ","))
	}
}

// attachNames lists the fronts to try as lanes: those the node listed in
// Status, minus get, connect, l3 and the owner's own front. With no list,
// try h2, ws, quic, h3 in that order.
func (g *GET) attachNames() []string {
	order := []string{wire.FrontH2, wire.FrontWS, wire.FrontQUIC, wire.FrontH3}
	if len(g.fronts) == 0 {
		return order
	}
	var out []string
	seen := map[string]bool{}
	for _, e := range g.fronts {
		n := e.Name
		if n == wire.FrontGET || n == g.ownerFront || n == wire.FrontConnect || n == wire.FrontL3 || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	return out
}

// attachFront dials one lane of the given front and runs the 0x04 exchange.
// A clear-cipher session is never attached over GET: the CDN would see the
// frames.
func (g *GET) attachFront(ctx context.Context, front string) error {
	if front == wire.FrontGET && ecrypto.InnerOf(g.key) == wire.InnerClear {
		return fmt.Errorf("get: inner clear")
	}
	o := attachOutbound(g.cfg, front, g.fronts)
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

// directNode is the profile URL (-url). Edge GET's cfg.URL is the CDN host.
// Lanes must dial the node itself, since the CDN only forwards GET.
var directNode atomic.Value // string

// SetDirectNode is the node origin extra fronts dial. Empty keeps cfg.URL.
func SetDirectNode(raw string) {
	directNode.Store(strings.TrimSpace(raw))
}

func directNodeURL() string {
	s, _ := directNode.Load().(string)
	return s
}

// attachOutbound builds the outbound for one lane: the GET outbound's
// settings, the direct node URL if set, and the path from Status (only if
// it is a safe path), else the default path for that front.
func attachOutbound(get config.Outbound, front string, learned []wire.FrontEP) config.Outbound {
	o := get
	o.Type = front
	if base := directNodeURL(); base != "" {
		o.URL = base
	}
	path, _ := frontEP(learned, front)
	if !okFrontPath(path) {
		path = ""
	}
	if path == "" {
		switch front {
		case wire.FrontH2, wire.FrontH3:
			path = config.DefaultH2Path
		case wire.FrontWS:
			path = config.DefaultWSPath
		}
	}
	u := strings.TrimSuffix(o.URL, "/")
	switch front {
	case wire.FrontH2, wire.FrontH3:
		o.Path = path
		o.URL = u
	case wire.FrontWS:
		o.Path = ""
		host := strings.TrimPrefix(strings.TrimPrefix(u, "https://"), "http://")
		host = strings.TrimPrefix(strings.TrimPrefix(host, "wss://"), "ws://")
		if i := strings.IndexByte(host, '/'); i >= 0 {
			host = host[:i]
		}
		if strings.HasPrefix(u, "https://") || strings.HasPrefix(u, "wss://") {
			o.URL = "wss://" + host + path
		} else {
			o.URL = "ws://" + host + path
		}
	case wire.FrontQUIC:
		o.Path = ""
		o.URL = u
	}
	return o
}

// frontEP returns the path and URL Status gave for front name.
func frontEP(learned []wire.FrontEP, name string) (path, rawURL string) {
	for _, e := range learned {
		if e.Name == name {
			return e.Path, e.URL
		}
	}
	return "", ""
}

// okFrontPath is a Status path that cannot move the host (no @, no scheme).
// Status comes from the node, but a path is glued onto our URL, so it must
// not be able to point the dial somewhere else.
func okFrontPath(p string) bool {
	p = strings.TrimSpace(p)
	return p != "" && p[0] == '/' && !strings.Contains(p, "@") && !strings.Contains(p, "://")
}

// runAttach does the 0x04 exchange on a length-prefixed stream: send the
// attach blob, read the sealed ack, and check that it names this front.
// Frames that came with the ack go into the mux. Then the socket becomes a
// lane.
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
	if len(rest) > 0 {
		g.sess.Push(rest)
	}
	g.arm(conn, func(b []byte) error { return writeMsg(conn, b) }, func() {
		g.attachRead(conn, readMsg)
	}, front)
	return nil
}

// attachLane is one client lane: an attached socket and how to write a
// sealed batch on it.
type attachLane struct {
	id    int
	front string
	conn  io.Closer
	write func([]byte) error
	dead  atomic.Bool
}

// arm starts the lane's read loop (which unbinds the lane when it ends) and
// adds the lane.
func (g *GET) arm(conn io.Closer, write func([]byte) error, read func(), front string) {
	g.attachWG.Add(1)
	go func() {
		defer g.attachWG.Done()
		read()
		_ = conn.Close()
		g.unbindAttach(conn)
	}()
	g.adoptLane(conn, write, front)
}

// adoptLane adds a lane. While attachExtras is still dialing (holdDispatch)
// it only records it; otherwise bulk moves onto lanes at once.
func (g *GET) adoptLane(conn io.Closer, write func([]byte) error, front string) {
	ln := &attachLane{
		id:    int(g.nextLane.Add(1)),
		front: front,
		conn:  conn,
		write: write,
	}
	g.attachMu.Lock()
	g.lanes = append(g.lanes, ln)
	g.attach = conn
	g.attachMu.Unlock()
	if g.holdDispatch.Load() {
		return
	}
	g.attachOff.Store(false)
	g.attachLive.Store(true)
	g.dropToGET.Store(false)
	g.kickDispatch()
}

func (g *GET) liveCount() int {
	g.attachMu.Lock()
	defer g.attachMu.Unlock()
	return g.liveCountLocked()
}

func (g *GET) liveCountLocked() int {
	n := 0
	for _, ln := range g.lanes {
		if ln != nil && !ln.dead.Load() {
			n++
		}
	}
	return n
}

func (g *GET) liveFronts() []string {
	g.attachMu.Lock()
	defer g.attachMu.Unlock()
	var out []string
	seen := map[string]bool{}
	for _, ln := range g.lanes {
		if ln == nil || ln.dead.Load() || ln.front == "" || seen[ln.front] {
			continue
		}
		seen[ln.front] = true
		out = append(out, ln.front)
	}
	return out
}

func (g *GET) fanLanes() []fanLane {
	g.attachMu.Lock()
	defer g.attachMu.Unlock()
	var out []fanLane
	for _, ln := range g.lanes {
		if ln == nil || ln.write == nil || ln.dead.Load() {
			continue
		}
		out = append(out, fanLane{id: ln.id, dead: &ln.dead, write: ln.write})
	}
	return out
}

// kickDispatch starts dispatchLoop unless it runs already, and restarts it
// if it stops while lanes are still live.
func (g *GET) kickDispatch() {
	if g.attachOff.Load() || g.attachStopped() || g.liveCount() == 0 {
		return
	}
	if !g.dispatchOn.CompareAndSwap(false, true) {
		return
	}
	g.attachWG.Add(1)
	go func() {
		defer g.attachWG.Done()
		g.dispatchLoop()
		g.dispatchOn.Store(false)
		if !g.attachOff.Load() && !g.attachStopped() && g.liveCount() > 0 {
			g.kickDispatch()
		}
	}()
}

// dispatchLoop is the client Puller while lanes are up (GET's pump stops
// pulling then). When control frames wait in the priority queue it pulls a
// small batch at once; during a draft pause it pulls only those.
func (g *GET) dispatchLoop() {
	fan := &attachFan{key: g.key, bind: map[uint32]int{}}
	for {
		if g.attachStopped() || g.attachOff.Load() {
			return
		}
		lanes := g.fanLanes()
		if len(lanes) == 0 {
			if g.holdDispatch.Load() {
				time.Sleep(5 * time.Millisecond)
				continue
			}
			return
		}
		max, wait := standPull(), 4*time.Millisecond
		if g.sess.PriQueued() > 0 {
			max = wsPriBytes
			wait = 0
		}
		var frames []wire.Frame
		if DraftPaused() {
			frames = g.sess.PullPri(max, wait)
		} else {
			frames = g.sess.Pull(max, wait)
		}
		if len(frames) == 0 {
			continue
		}
		fan.send(lanes, frames)
	}
}

// attachRead reads sealed batches from one lane into the mux until the
// socket ends or the node sends a Front frame naming "get" (back to GET).
// A batch that fails to open is skipped.
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
		if isStripe(plain) {
			stripeIngest(g.sess, plain[1:])
			if g.sess.Closed() {
				return
			}
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

// attachWS opens a WebSocket lane. It forces HTTP/1.1 (an h2 socket cannot
// upgrade to WebSocket) and uses the same browser TLS as other fronts. Each
// sealed batch is one binary message.
func (g *GET) attachWS(ctx context.Context, o config.Outbound) error {
	wsURL, err := wsDialURL(o.URL)
	if err != nil {
		return err
	}
	if !isLocalUrl(wsURL) {
		return fmt.Errorf("ws url")
	}
	u, err := parseURL(wsURL)
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
				DialTLSContext: func(ctx context.Context, network, _ string) (net.Conn, error) {
					return dialTLSALPNContext(ctx, addr, sni, o.Fingerprint, insecure, g.token, []string{"http/1.1"})
				},
			},
		}
	}
	c, _, err := websocket.Dial(ctx, wsURL, opts)
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
	if len(rest) > 0 {
		g.sess.Push(rest)
	}
	g.arm(wrap, wrap.WriteBinary, func() {
		g.attachRead(&wsReader{read: wrap.ReadBinary}, func(r io.Reader) ([]byte, error) {
			return r.(*wsReader).read()
		})
	}, wire.FrontWS)
	return nil
}

// wsReader lets attachRead take a whole-message WebSocket reader.
type wsReader struct {
	read func() ([]byte, error)
}

func (w *wsReader) Read([]byte) (int, error) { return 0, io.ErrUnexpectedEOF }

// attachQUIC opens a QUIC lane: one stream on a new QUIC connection.
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

// attachH3 opens an h3 lane: an HTTP/3 CONNECT request whose request and
// response bodies form the stream.
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

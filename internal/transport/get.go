package transport

// GET + header-uplink front. Inflight, exclusive Puller, and pad hold are here.

import (
	"bytes"
	"context"
	"crypto/rand"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptrace"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/a3app/loft/internal/cdn"
	"github.com/a3app/loft/internal/config"
	ecrypto "github.com/a3app/loft/internal/crypto"
	"github.com/a3app/loft/internal/mux"
	"github.com/a3app/loft/internal/netconf"
	"github.com/a3app/loft/internal/wire"
)

// GET is the GET + header-uplink transport. Any CDN/proxy that only
// forwards GET can sit in front; which vendor is node config, not protocol.
type GET struct {
	prof         cdn.Profile
	cfg          config.Outbound
	token        []byte
	client       *http.Client
	clients      []*http.Client
	born         []time.Time
	drain        []getH2Drain
	h2MaxAge     time.Duration // 0 = keep HTTP/2 until error
	poolMu       sync.Mutex
	rr           atomic.Uint32
	base         string
	path         string
	host         string
	sess         *mux.Session
	key          []byte
	sid          string
	seq          atomic.Uint64
	started      atomic.Bool
	stop         context.CancelFunc
	done         chan struct{}
	dead         sync.Once
	mu           sync.Mutex
	pushMu       sync.Mutex
	offer        wire.Cap
	nextSeq      uint64
	held         map[uint64][]wire.Frame
	keepAlive    time.Duration
	busy         atomic.Bool
	upBusy       atomic.Bool
	catch        atomic.Int32 // extra pad GETs after useful up/down
	lastLive     atomic.Int64 // unix nano of last useful up/down
	caps         wire.Cap
	connUp       atomic.Uint64
	connDown     atomic.Uint64
	trips        getTrips
	lastDrop     atomic.Int64
	budget       atomic.Int32  // live header budget; 413 halves it, floor 1024
	miss         atomic.Int32  // consecutive same-seq retry 404s; 3 → die (sid lost)
	calmUntil    atomic.Int64  // unix nano; H2 ENHANCE_YOUR_CALM shared backoff
	liveInflight atomic.Int32  // node Tune / Status; pump caps outstanding
	note         atomic.Uint32 // packed CmdTune to send once on next GET
	liveH2       atomic.Int32  // HTTP/2 connections; node Status/Tune
	uplinkEnc    atomic.Int32  // wire.UplinkBase64 or UplinkCookie85
	applied      atomic.Uint64 // CmdReport applied-through GET seq
}

// getTrips is per-GET HTTP timing: pad vs bulk body vs fat Cookie uplink.
// Used to split CDN delay (edge TTFB) from origin body and header budget.
type getTrips struct {
	errN      atomic.Int64
	pad       tripBucket
	bulk      tripBucket
	up        tripBucket
	maxTTFBNs atomic.Int64
}

type tripBucket struct {
	n, ttfbNs, totalNs, bytes atomic.Int64
}

func (b *tripBucket) add(ttfb, total time.Duration, n int) {
	b.n.Add(1)
	b.ttfbNs.Add(ttfb.Nanoseconds())
	b.totalNs.Add(total.Nanoseconds())
	b.bytes.Add(int64(n))
}

// TripSummary is pad/bulk/up HTTP timing for CDN vs direct GET debug.
func (g *GET) TripSummary() string {
	if g == nil {
		return ""
	}
	n := g.trips.pad.n.Load() + g.trips.bulk.n.Load() + g.trips.up.n.Load()
	if n == 0 && g.trips.errN.Load() == 0 {
		return "get trips=0"
	}
	avg := func(b *tripBucket) (ttfb, total, kb float64) {
		c := b.n.Load()
		if c == 0 {
			return 0, 0, 0
		}
		ttfb = float64(b.ttfbNs.Load()) / float64(c) / 1e6
		total = float64(b.totalNs.Load()) / float64(c) / 1e6
		kb = float64(b.bytes.Load()) / float64(c) / 1024
		return
	}
	pT, pTot, _ := avg(&g.trips.pad)
	bT, bTot, bKB := avg(&g.trips.bulk)
	uT, uTot, uKB := avg(&g.trips.up)
	return fmt.Sprintf(
		"get trips=%d err=%d max_ttfb=%.0fms | pad n=%d ttfb=%.0fms total=%.0fms | bulk n=%d ttfb=%.0fms total=%.0fms body=%.1fKiB | up n=%d ttfb=%.0fms total=%.0fms hdr=%.1fKiB",
		n, g.trips.errN.Load(), float64(g.trips.maxTTFBNs.Load())/1e6,
		g.trips.pad.n.Load(), pT, pTot,
		g.trips.bulk.n.Load(), bT, bTot, bKB,
		g.trips.up.n.Load(), uT, uTot, uKB,
	)
}

func (g *GET) noteTrip(ttfb, total time.Duration, up, down int, err error) {
	if err != nil {
		g.trips.errN.Add(1)
		return
	}
	for {
		cur := g.trips.maxTTFBNs.Load()
		if ttfb.Nanoseconds() <= cur || g.trips.maxTTFBNs.CompareAndSwap(cur, ttfb.Nanoseconds()) {
			break
		}
	}
	switch {
	case up >= 2048:
		g.trips.up.add(ttfb, total, up)
	case down >= 8192:
		g.trips.bulk.add(ttfb, total, down)
	default:
		g.trips.pad.add(ttfb, total, down)
	}
}

// NewGET builds a GET client from o. Handshake runs in Start.
func NewGET(token string, o config.Outbound) (*GET, error) {
	prof, err := cdn.Lookup(o.CDN)
	if err != nil {
		return nil, err
	}
	if prof.Front == "ws" {
		return nil, fmt.Errorf("cdn profile %q is ws, use type ws", prof.Name)
	}
	if o.Path == "" {
		o.Path = prof.Path
	}
	if o.HeaderBudget <= 0 {
		o.HeaderBudget = prof.HeaderBudget
	}
	if o.Inflight <= 0 {
		o.Inflight = prof.Inflight
	}
	cl, err := httpClient(o, token)
	if err != nil {
		return nil, err
	}
	u, err := parseURL(o.URL)
	if err != nil {
		return nil, err
	}
	base := strings.TrimRight(o.URL, "/")
	g := &GET{
		prof:     prof,
		cfg:      o,
		token:    []byte(token),
		client:   cl,
		clients:  []*http.Client{cl},
		born:     []time.Time{time.Now()},
		h2MaxAge: parseH2MaxAge(o.H2MaxAgeMS),
		base:     base,
		path:     strings.TrimRight(o.Path, "/"),
		host:     u.Host,
		sess:     clientHoldMux(),
		done:     make(chan struct{}),
		held:     map[uint64][]wire.Frame{},
	}
	if sid, key, seq := clientHoldGET(); sid != "" {
		g.sid = sid
		g.key = key
		g.seq.Store(seq)
	}
	if o.HeaderBudget > 0 {
		g.budget.Store(int32(o.HeaderBudget))
	}
	if o.Inflight > 0 {
		g.liveInflight.Store(int32(o.Inflight))
	}
	g.liveH2.Store(1)
	return g, nil
}

// Start sends the first GET (0x02 Cookie, client-chosen sid) and starts the inflight pump.
func (g *GET) Start(ctx context.Context) error {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.started.Load() {
		return nil
	}
	if g.sid != "" && g.sid != "new" && len(g.key) > 0 {
		if g.keepAlive <= 0 {
			g.keepAlive = 15 * time.Second
		}
		loopCtx, stop := context.WithCancel(context.Background())
		g.stop = stop
		g.nextSeq = g.seq.Load() + 1
		g.started.Store(true)
		n := g.cfg.Inflight
		if n < 1 {
			n = 1
		}
		if v := int(g.liveInflight.Load()); v > n {
			n = v
		}
		if g.liveInflight.Load() <= 0 {
			g.liveInflight.Store(int32(n))
		}
		go g.pump(loopCtx, n)
		return nil
	}
	hello := newClientHello(g.offer)
	proof, nonce, sk, err := ecrypto.MakeAuth(g.token)
	if err != nil {
		return err
	}
	auth := append(proof, hello.Encode()...)
	g.sid = ecrypto.RandomID()
	body, _, err := g.roundTrip(ctx, auth, nil)
	if err != nil {
		return fmt.Errorf("get handshake: %w", err)
	}
	if g.sid == "" || g.sid == "new" {
		return fmt.Errorf("get handshake: empty session")
	}
	key, plain, err := ecrypto.OpenHello(sk, nonce, body, []byte(g.sid))
	if err != nil {
		return fmt.Errorf("get handshake: status: %w", err)
	}
	g.key = key
	st, err := wire.DecodeStatus(plain)
	if err != nil {
		return fmt.Errorf("get handshake: status: %w", err)
	}
	if !st.Code.OK() {
		return wire.Fail(st.Code, st.Program)
	}
	RememberVis(st)
	g.caps = st.Caps
	g.sess.SetHold(st.Caps.Has(wire.CapHold))
	if !g.cfg.UseMux() && !st.Caps.Has(wire.CapConnect) {
		return fmt.Errorf("node requires mux")
	}
	ka := time.Duration(st.KeepaliveMS) * time.Millisecond
	if ka <= 0 {
		ka = 15 * time.Second
	}
	g.keepAlive = ka
	// Handshake Status is a ceiling. Live CmdTune later is authoritative.
	if st.HeaderBudget > 0 {
		cur := g.budget.Load()
		if cur <= 0 || int32(st.HeaderBudget) < cur {
			g.budget.Store(int32(st.HeaderBudget))
		}
	}
	if st.Inflight > 0 {
		cur := g.liveInflight.Load()
		if cur <= 0 || int32(st.Inflight) < cur {
			g.liveInflight.Store(int32(st.Inflight))
		}
	}
	if st.UplinkEnc == wire.UplinkCookie85 {
		g.uplinkEnc.Store(int32(wire.UplinkCookie85))
	}
	if st.H2Conns > 1 {
		n := int(st.H2Conns)
		if n > 4 {
			n = 4
		}
		g.liveH2.Store(int32(n))
	}
	if addr, sni, _, err := resolveTLS(g.cfg); err == nil {
		if err := adoptDecoy(addr, sni, st, strings.TrimSpace(g.cfg.CDN) == ""); err != nil {
			return err
		}
	}
	loopCtx, stop := context.WithCancel(context.Background())
	g.stop = stop
	g.nextSeq = g.seq.Load() + 1
	g.started.Store(true)
	n := g.cfg.Inflight
	if n < 1 {
		n = 1
	}
	if v := int(g.liveInflight.Load()); v > n {
		n = v
	}
	if g.liveInflight.Load() <= 0 {
		g.liveInflight.Store(int32(n))
	}
	go g.pump(loopCtx, n)
	return nil
}

// Session returns the live mux session.
func (g *GET) Session() *mux.Session { return g.sess }

// ConnBytes returns spliced CONNECT bytes when mux is off.
func (g *GET) ConnBytes() (uint64, uint64) {
	return g.connUp.Load(), g.connDown.Load()
}

// Done is closed when the pump dies or held seqs overflow.
func (g *GET) Done() <-chan struct{} { return g.done }

// Open starts the front if needed. Mux off uses HTTP CONNECT splice.
func (g *GET) Open(target string) (net.Conn, error) {
	if !g.started.Load() {
		if err := g.Start(context.Background()); err != nil {
			return nil, err
		}
	}
	if !g.cfg.UseMux() {
		return g.openConnect(target)
	}
	return g.sess.Open(target)
}

// die closes Done once.
func (g *GET) die() {
	g.dead.Do(func() { close(g.done) })
}

// Rebind replaces GET HTTP/2 sockets after an egress change.
// Mux, sid, key, seq, and the pump stay. Inflight GETs finish on
// drained clients or retry the same seq (radioGap). CONNECT splice
// on the old pool dies; mux Open does not. The node keys the session
// on sid, not the client address.
func (g *GET) Rebind() error {
	if !g.started.Load() || len(g.key) == 0 || g.sid == "" || g.sid == "new" {
		return fmt.Errorf("get rebind: not live")
	}
	select {
	case <-g.done:
		return fmt.Errorf("get rebind: not live")
	default:
	}
	n := int(g.liveH2.Load())
	if n < 1 {
		n = 1
	}
	if n > 4 {
		n = 4
	}
	next := make([]*http.Client, 0, n)
	for i := 0; i < n; i++ {
		cl, err := httpClient(g.cfg, string(g.token))
		if err != nil {
			for _, c := range next {
				if c != nil {
					c.CloseIdleConnections()
				}
			}
			return err
		}
		next = append(next, cl)
	}
	now := time.Now()
	g.poolMu.Lock()
	seen := make(map[*http.Client]struct{}, len(g.clients)+1)
	for _, c := range g.clients {
		g.parkDrainLocked(c, now)
		seen[c] = struct{}{}
	}
	if g.client != nil {
		if _, ok := seen[g.client]; !ok {
			g.parkDrainLocked(g.client, now)
		}
	}
	g.clients = next
	g.born = make([]time.Time, len(next))
	for i := range g.born {
		g.born[i] = now
	}
	g.client = next[0]
	g.sweepDrainLocked(now)
	g.poolMu.Unlock()
	return nil
}

// Park stops the pump without Leave or mux.Close. Same GET sid resumes.
func (g *GET) Park() error {
	if g.stop != nil {
		g.stop()
	}
	g.dropIdleFront()
	parkClient(g.sess, g.sid, g.key, g.seq.Load())
	g.die()
	return nil
}

// Close stops the pump, sends CmdLeave, and closes mux.
func (g *GET) Close() error {
	if g.stop != nil {
		g.stop()
	}
	g.sendLeave()
	clearClientHold()
	g.sess.Close()
	g.die()
	return nil
}

// sendLeave is the last GET: CmdLeave, then the node Detach+Release.
// Independent of the loop ctx so SIGTERM can still deliver it (GUI SIGKILL is 1.5s).
func (g *GET) sendLeave() {
	if !g.started.Load() || g.key == nil || g.sid == "" || g.sid == "new" {
		return
	}
	raw, err := encodeLeave()
	if err != nil {
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), 800*time.Millisecond)
	defer cancel()
	_, _, _ = g.roundTrip(ctx, nil, raw)
}

// getJob is one uplink batch with its GET seq.
type getJob struct {
	seq     uint64
	payload []byte
	resin   []wire.Frame // held for same-seq retry; draft is dropped
}

// pump is the only mux Puller. Seq is assigned in Pull order, then HTTP
// workers run in parallel. Extra loops used to Pull concurrently and
// g.seq.Add in a different order than mux.out — origin TLS saw reordered
// records (bad record MAC) and Yandex upload sat at ~100 kbit/s.
func (g *GET) pump(ctx context.Context, inflight int) {
	jobs := make(chan getJob, inflight)
	var wg sync.WaitGroup
	for i := 0; i < inflight; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			g.worker(ctx, jobs)
		}()
	}
	defer func() {
		close(jobs)
		wg.Wait()
	}()
	for {
		if ctx.Err() != nil {
			return
		}
		wait := g.pace()
		pullWait := wait
		queued := g.sess.OutQueued() > 0
		if queued || wait == 0 {
			pullWait = 0
		} else if g.outstanding() > 0 {
			// Warm/idle: wait for in-flight before another pad so we
			// don't stampede, and so a Data body can set lastLive.
			if !g.sleepCtx(ctx, 5*time.Millisecond) {
				return
			}
			continue
		}
		// Pads must not occupy every worker: Open/Data uplink then
		// waits a full RTT (TCP 1-byte went to ~8 RTT / 640ms).
		slots := g.slots()
		padMax := slots / 4
		if padMax < 4 {
			padMax = 4
		}
		if padMax > slots {
			padMax = slots
		}
		if pullWait == 0 && !queued && g.outstanding() >= padMax {
			if !g.sleepCtx(ctx, 5*time.Millisecond) {
				return
			}
			continue
		}
		var up []wire.Frame
		if DraftPaused() {
			up = g.sess.PullPri(g.uplinkBudget(), pullWait)
		} else {
			up = g.sess.Pull(g.uplinkBudget(), pullWait)
		}
		note := g.takeNoteFrame()
		if len(note) > 0 {
			up = append(note, up...)
		}
		parts := wire.SplitUplink(up)
		if len(parts) == 0 {
			parts = [][]wire.Frame{nil}
		}
		for _, part := range parts {
			if muxBusy(part) {
				g.upBusy.Store(true)
				g.markLive()
			} else if len(note) == 0 {
				g.upBusy.Store(false)
				if g.outstanding() >= padMax {
					if !g.sleepCtx(ctx, 5*time.Millisecond) {
						return
					}
					g.restoreNote(note)
					break
				}
				if wait > 0 {
					// paced pad (idle keepalive)
				} else if g.busy.Load() || g.catchingDown() {
					g.noteCatch()
				} else {
					if !g.sleepCtx(ctx, 20*time.Millisecond) {
						return
					}
					g.restoreNote(note)
					break
				}
			}
			raw, err := wire.EncodeBatch(part)
			if err != nil {
				g.restoreNote(note)
				break
			}
			seq := g.seq.Add(1)
			select {
			case jobs <- getJob{seq: seq, payload: raw, resin: wire.ResinOnly(part)}:
			case <-ctx.Done():
				return
			}
		}
	}
}

// worker runs one inflight HTTP GET per job and retries on front reset.
func (g *GET) worker(ctx context.Context, jobs <-chan getJob) {
	for {
		select {
		case <-ctx.Done():
			return
		case j, ok := <-jobs:
			if !ok {
				return
			}
			t0 := time.Now()
			tries := 0
			payload := j.payload
			for {
				if g.applied.Load() >= j.seq && j.seq > 0 {
					break
				}
				if !g.waitCalm(ctx, j.seq) {
					return
				}
				_, _, _, err := g.roundTripAt(ctx, nil, payload, j.seq)
				if err == nil {
					g.miss.Store(0)
					break
				}
				// Pump Close cancels ctx; do not die() on that or Supervise
				// stacks a resume on top of an in-flight teardown.
				if ctx.Err() != nil {
					return
				}
				if headerTooLarge(err) {
					n := g.halveBudget()
					fmt.Fprintf(os.Stderr, "get front 413 budget=%d seq=%d\n", n, j.seq)
					g.queueNote(wire.Tune413)
				}
				if status404(err) {
					// First-attempt 404s race across inflight workers; that
					// is edge noise. Count only same-seq retries. Three
					// retry 404s is origin without sid.
					if tries == 0 {
						if g.miss.Load() == 0 {
							g.queueNote(wire.Tune404)
						}
					} else {
						n404 := g.miss.Add(1)
						if n404 >= 3 {
							fmt.Fprintf(os.Stderr, "get front %v\n", err)
							g.die()
							return
						}
					}
					fmt.Fprintf(os.Stderr, "get front 404 seq=%d retry\n", j.seq)
				} else if status429(err) {
					g.queueNote(wire.Tune429)
				} else if !retryableGET(err) {
					fmt.Fprintf(os.Stderr, "get front %v\n", err)
					g.die()
					return
				}
				// Fat Cookie × inflight 16: Yandex GOAWAY ENHANCE_YOUR_CALM.
				// die()+resume slams 16 new GETs and the edge stays calm;
				// mux looks like “traffic then packet loss”. Back off on
				// the same sid instead.
				tries++
				if getGiveUp(tries, t0) {
					fmt.Fprintf(os.Stderr, "get front give up seq=%d %v\n", j.seq, err)
					g.die()
					return
				}
				if g.applied.Load() >= j.seq && j.seq > 0 {
					break
				}
				if len(j.resin) > 0 {
					if raw, e := wire.EncodeBatch(j.resin); e == nil {
						payload = raw
					}
				} else {
					payload = nil
				}
				if enhanceCalm(err) {
					g.noteCalm()
					g.busy.Store(false)
					if !g.waitCalm(ctx, j.seq) {
						return
					}
					continue
				}
				if frontReset(err) || radioGap(err) {
					fmt.Fprintf(os.Stderr, "get front reset seq=%d %v\n", j.seq, err)
					g.busy.Store(false)
					g.dropIdleFront()
					if strings.Contains(strings.ToLower(err.Error()), "goaway") {
						g.queueNote(wire.TuneGoaway)
					} else {
						g.queueNote(wire.TuneReset)
					}
				}
				wait := 50 * time.Millisecond
				if radioGap(err) {
					wait = time.Duration(tries) * 200 * time.Millisecond
					if wait > 2*time.Second {
						wait = 2 * time.Second
					}
				} else if !frontReset(err) {
					wait = time.Duration(tries) * 150 * time.Millisecond
				}
				if cap := gapHoldDuration(); wait > cap {
					wait = cap
				}
				select {
				case <-ctx.Done():
					return
				case <-time.After(wait):
				}
			}
		}
	}
}

const (
	getRetryMax    = 8
	getRetryWindow = 12 * time.Second
	getCalmBackoff = 2 * time.Second
)

// getGiveUp is the worker retry cap: radioGap used to continue past this
// and occupy inflight forever (SOCKS HOL, LIVE n=1, no die/resume).
func getGiveUp(tries int, t0 time.Time) bool {
	return tries > getRetryMax || time.Since(t0) > getRetryWindow
}

func enhanceCalm(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "enhance_your_calm") || strings.Contains(s, "enhance your calm")
}

// noteCalm is H2 ENHANCE_YOUR_CALM: one shared 2s window, one idle drop.
func (g *GET) noteCalm() {
	until := time.Now().Add(getCalmBackoff).UnixNano()
	for {
		cur := g.calmUntil.Load()
		if cur >= until {
			return
		}
		if g.calmUntil.CompareAndSwap(cur, until) {
			fmt.Fprintf(os.Stderr, "get front enhance_your_calm backoff=%s\n", getCalmBackoff)
			g.dropIdleFront()
			g.queueNote(wire.TuneCalm)
			return
		}
	}
}

// waitCalm holds until the CALM window ends, staggered by seq so 16
// workers do not redial in one tick.
func (g *GET) waitCalm(ctx context.Context, seq uint64) bool {
	ns := g.calmUntil.Load()
	if ns == 0 {
		return true
	}
	d := time.Until(time.Unix(0, ns))
	if d <= 0 {
		return true
	}
	d += time.Duration(seq%8) * 150 * time.Millisecond
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

// dropIdleFront drops a dead HTTP/2 conn so the next GET dials a new one.
func (g *GET) dropIdleFront() {
	now := time.Now().UnixNano()
	prev := g.lastDrop.Load()
	if prev != 0 && now-prev < int64(200*time.Millisecond) {
		return
	}
	if !g.lastDrop.CompareAndSwap(prev, now) {
		return
	}
	g.poolMu.Lock()
	cs := append([]*http.Client(nil), g.clients...)
	ds := make([]*http.Client, len(g.drain))
	for i, d := range g.drain {
		ds[i] = d.c
	}
	g.poolMu.Unlock()
	if len(cs) == 0 && g.client != nil {
		g.client.CloseIdleConnections()
	}
	for _, c := range cs {
		if c != nil {
			c.CloseIdleConnections()
		}
	}
	for _, c := range ds {
		if c != nil {
			c.CloseIdleConnections()
		}
	}
}

// pickClient round-robins live HTTP/2 connections. Node Status h2_conns
// grows the pool; inflight is the total across them. poolMu is not GET.mu:
// Start holds GET.mu across the handshake GET. h2_max_age_ms (0 = off)
// replaces one aged slot per pick; mux, GET sid, and CONNECT splice stay.
func (g *GET) pickClient() *http.Client {
	n := int(g.liveH2.Load())
	if n < 1 {
		n = 1
	}
	g.poolMu.Lock()
	if n > len(g.clients) {
		g.growClientsLocked(n)
	}
	g.rotateLocked()
	list := g.clients
	g.poolMu.Unlock()
	if len(list) == 0 {
		return g.client
	}
	if n > len(list) {
		n = len(list)
	}
	i := int(g.rr.Add(1) % uint32(n))
	return list[i]
}

func (g *GET) growClientsLocked(n int) {
	if n > 4 {
		n = 4
	}
	now := time.Now()
	for len(g.clients) < n {
		cl, err := httpClient(g.cfg, string(g.token))
		if err != nil {
			return
		}
		g.clients = append(g.clients, cl)
		g.born = append(g.born, now)
	}
}

type getH2Drain struct {
	c *http.Client
	t time.Time
}

const (
	h2MaxAgeMin = 15 * time.Second
	h2MaxAgeMax = time.Hour
	h2DrainTTL  = 30 * time.Second
	h2DrainCap  = 8
)

func parseH2MaxAge(ms int) time.Duration {
	if ms <= 0 {
		return 0
	}
	d := time.Duration(ms) * time.Millisecond
	if d < h2MaxAgeMin {
		return h2MaxAgeMin
	}
	if d > h2MaxAgeMax {
		return h2MaxAgeMax
	}
	return d
}

// rotateLocked replaces at most one HTTP/2 client older than h2MaxAge.
// The old Transport stays in drain so in-flight GETs finish; CloseIdle
// later. 0 max-age is a no-op.
func (g *GET) rotateLocked() {
	g.sweepDrainLocked(time.Now())
	if g.h2MaxAge <= 0 {
		return
	}
	now := time.Now()
	for i := range g.clients {
		if i >= len(g.born) {
			g.born = append(g.born, now)
			continue
		}
		if now.Sub(g.born[i]) < g.h2MaxAge {
			continue
		}
		cl, err := httpClient(g.cfg, string(g.token))
		if err != nil {
			return
		}
		g.parkDrainLocked(g.clients[i], now)
		g.clients[i] = cl
		g.born[i] = now
		return
	}
}

func (g *GET) parkDrainLocked(c *http.Client, now time.Time) {
	if c == nil {
		return
	}
	g.drain = append(g.drain, getH2Drain{c: c, t: now})
	for len(g.drain) > h2DrainCap {
		old := g.drain[0]
		g.drain = g.drain[1:]
		if old.c != nil {
			old.c.CloseIdleConnections()
		}
	}
}

func (g *GET) sweepDrainLocked(now time.Time) {
	if len(g.drain) == 0 {
		return
	}
	keep := g.drain[:0]
	for _, d := range g.drain {
		if now.Sub(d.t) < h2DrainTTL {
			keep = append(keep, d)
			continue
		}
		if d.c != nil {
			d.c.CloseIdleConnections()
		}
	}
	g.drain = keep
}

// markLive records useful mux so pace stays warm for a short window.
func (g *GET) markLive() {
	g.catch.Store(8)
	g.lastLive.Store(time.Now().UnixNano())
}

// liveWindow is true for 800ms after the last useful up or down.
func (g *GET) liveWindow() bool {
	t := g.lastLive.Load()
	if t == 0 {
		return false
	}
	return time.Since(time.Unix(0, t)) < 800*time.Millisecond
}

// outstanding is seqs assigned but not yet applied by pushOrdered.
func (g *GET) outstanding() int {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.nextSeq == 0 {
		return int(g.seq.Load())
	}
	n := int64(g.seq.Load()) - int64(g.nextSeq) + 1
	if n < 0 {
		return 0
	}
	return int(n)
}

// catchingDown is true while extra pad GETs after useful mux remain.
func (g *GET) catchingDown() bool {
	return g.catch.Load() > 0
}

// wantFull keeps inflight packed after useful mux. A count of empty
// pads used to clear busy (inflight=8 blows past 4 in one RTT) and
// the pump slept 1s — live download fell ~10× vs an 80ms pipeline.
func (g *GET) wantFull() bool {
	return g.busy.Load() || g.catchingDown() || g.sess.OutQueued() > 0 || g.liveWindow()
}

// noteCatch consumes one catch pad after a full inflight GET.
func (g *GET) noteCatch() {
	for {
		v := g.catch.Load()
		if v <= 0 {
			return
		}
		if g.catch.CompareAndSwap(v, v-1) {
			return
		}
	}
}

// pace: idle GETs around keepalive/2 with jitter so the public path is not a metronome.
// busy / catch / uplink → fill inflight (download). lastLive or live TCP
// warms at 20ms so a gapped download does not sleep 1s (~10×). pace=0
// on lastLive packed 8 pads and TCP 1-byte waited ~8 RTT.
func (g *GET) pace() time.Duration {
	if g.busy.Load() || g.catchingDown() || g.sess.OutQueued() > 0 {
		return 0
	}
	if g.liveWindow() || g.sess.StreamCount() > 0 {
		return 20 * time.Millisecond
	}
	d := g.keepAlive / 2
	if d < time.Second {
		d = time.Second
	}
	return jitterDur(d)
}

// uplinkBudget is decoded mux plaintext that still fits HeaderBudget after AEAD and base64url.
func (g *GET) uplinkBudget() int {
	h := int(g.budget.Load())
	if h <= 0 {
		h = g.cfg.HeaderBudget
	}
	if h <= 0 {
		h = 16 << 10
	}
	// Cookie is base64.RawURLEncoding(nonce||ct) or Z85. Keep the
	// encoded value inside HeaderBudget so the GET is not 413 / truncated
	// (truncated uplink used to consume seq and drop POST Data).
	n := h*3/4 - 12 - 16 - 96
	if n < 1024 {
		n = 1024
	}
	// Batch plaintext can be several CmdData (each ≤ MaxPayload). Capping
	// at MaxPayload packed one frame per GET and sat Yandex upload.
	if byte(g.uplinkEnc.Load()) == wire.UplinkCookie85 {
		// '~' + Z85(u16be len || seal). 5 encoded chars per 4 raw bytes.
		n = (h-1)*4/5 - 2 - 12 - 16 - 96
		if n < 1024 {
			n = 1024
		}
	}
	return n
}

// sleepCtx waits d or returns false if ctx is done.
func (g *GET) sleepCtx(ctx context.Context, d time.Duration) bool {
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

// pipelineSlot is true when fewer than Inflight seqs are still waiting
// on pushOrdered. Pump does not call it: jobs+workers over-subscribe so
// a pad is waiting to be exclusive Puller. Capping at Inflight starved a
// 4 MB GET (2.1/4 MB, 2.6s gap). Extra loops can stampede HTTP/1.1.
func (g *GET) pipelineSlot() bool {
	n := g.cfg.Inflight
	if n < 1 {
		n = 1
	}
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.nextSeq == 0 {
		return true
	}
	ahead := int64(g.seq.Load()) - int64(g.nextSeq) + 1
	return ahead < int64(n)
}

// muxBusy is true when frames carry Open, Data, Dgram, Leave, or Close (not Pad/Ping/Pong).
func muxBusy(frames []wire.Frame) bool {
	for _, f := range frames {
		switch f.Cmd {
		case wire.CmdPad, wire.CmdPing, wire.CmdPong, wire.CmdTune, wire.CmdReport:
			continue
		default:
			return true
		}
	}
	return false
}

func (g *GET) slots() int {
	n := int(g.liveInflight.Load())
	if n < 1 {
		return 1
	}
	return n
}

func packNote(ev, inflight byte, budget uint16) uint32 {
	return uint32(ev)<<24 | uint32(inflight)<<16 | uint32(budget)
}

func (g *GET) queueNote(ev byte) {
	if ev == 0 {
		return
	}
	inf := byte(g.liveInflight.Load())
	if inf == 0 {
		n := g.cfg.Inflight
		if n < 1 {
			n = 1
		}
		if n > 255 {
			n = 255
		}
		inf = byte(n)
	}
	bud := uint16(g.budget.Load())
	g.note.CompareAndSwap(0, packNote(ev, inf, bud))
}

func (g *GET) takeNoteFrame() []wire.Frame {
	v := g.note.Swap(0)
	if v == 0 {
		return nil
	}
	return []wire.Frame{wire.TuneFrame(byte(v>>24), byte(v>>16), uint16(v))}
}

func (g *GET) restoreNote(note []wire.Frame) {
	if len(note) == 0 || note[0].Cmd != wire.CmdTune {
		return
	}
	t, ok := wire.ParseTune(note[0].Payload)
	if !ok || t.Event == 0 {
		return
	}
	g.note.CompareAndSwap(0, packNote(t.Event, t.Inflight, t.Budget))
}

func (g *GET) applyTune(t wire.Tune) {
	if t.Event != wire.TuneSet {
		return
	}
	if t.Budget > 0 {
		g.budget.Store(int32(t.Budget))
		fmt.Fprintf(os.Stderr, "get front tune budget=%d\n", t.Budget)
	}
	if t.Inflight > 0 {
		g.liveInflight.Store(int32(t.Inflight))
		fmt.Fprintf(os.Stderr, "get front tune inflight=%d\n", t.Inflight)
	}
	if t.H2Conns > 1 {
		n := int(t.H2Conns)
		if n > 4 {
			n = 4
		}
		g.liveH2.Store(int32(n))
		fmt.Fprintf(os.Stderr, "get front tune h2=%d\n", n)
	}
}

// noteApplied records CmdReport applied-through. Workers drop retries at or below it.
func (g *GET) noteApplied(n uint64) {
	for {
		cur := g.applied.Load()
		if n <= cur {
			return
		}
		if g.applied.CompareAndSwap(cur, n) {
			return
		}
	}
}

// frontReset reports a dropped HTTP connection that the worker should retry.
func radioGap(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "no route") ||
		strings.Contains(s, "network is unreachable") ||
		strings.Contains(s, "network unreachable") ||
		strings.Contains(s, "network down") ||
		strings.Contains(s, "host is down") ||
		strings.Contains(s, "i/o timeout") ||
		strings.Contains(s, "deadline exceeded") ||
		strings.Contains(s, "connection timed out") ||
		strings.Contains(s, "connection reset") ||
		strings.Contains(s, "broken pipe") ||
		strings.Contains(s, "unexpected eof") ||
		strings.HasSuffix(s, "eof") ||
		strings.Contains(s, ": eof")
}

func frontReset(err error) bool {
	if err == nil {
		return false
	}
	if radioGap(err) {
		return true
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "connection refused") ||
		strings.Contains(s, "goaway") ||
		strings.Contains(s, "stream error") ||
		strings.Contains(s, "http2: client conn")
}

// retryableGET is a transient front/CDN failure. 405 must not HOL-block
// (wrong method). 413 retries after halveBudget. 404 retries; worker
// die() only after three consecutive misses (origin lost the sid).
func retryableGET(err error) bool {
	if err == nil {
		return false
	}
	if frontReset(err) || radioGap(err) {
		return true
	}
	s := strings.ToLower(err.Error())
	if strings.Contains(s, "method not allowed") {
		return false
	}
	if headerTooLarge(err) || status404(err) {
		return true
	}
	if strings.Contains(s, "status 4") {
		return strings.Contains(s, "status 408") || strings.Contains(s, "status 429")
	}
	return strings.Contains(s, "timeout") ||
		strings.Contains(s, "deadline") ||
		strings.Contains(s, "canceled") ||
		strings.Contains(s, "cancelled") ||
		strings.Contains(s, "status 502") ||
		strings.Contains(s, "status 503") ||
		strings.Contains(s, "status 504") ||
		strings.Contains(s, "unavailable") ||
		strings.Contains(s, "eof") ||
		strings.Contains(s, "http2:")
}

func headerTooLarge(err error) bool {
	if err == nil {
		return false
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "header too large") || strings.Contains(s, "status 413")
}

func status404(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "status 404")
}

func status429(err error) bool {
	if err == nil {
		return false
	}
	return strings.Contains(strings.ToLower(err.Error()), "status 429")
}

// halveBudget is HTTP 413: spec says halve the header budget, floor 1024, and split.
func (g *GET) halveBudget() int {
	for {
		cur := int(g.budget.Load())
		h := cur
		if h <= 0 {
			h = g.cfg.HeaderBudget
			if h <= 0 {
				h = 16 << 10
			}
		}
		n := h / 2
		if n < 1024 {
			n = 1024
		}
		if cur == n {
			return n
		}
		if g.budget.CompareAndSwap(int32(cur), int32(n)) {
			return n
		}
	}
}

// roundTrip is roundTripAt with seq assigned inside.
func (g *GET) roundTrip(ctx context.Context, auth, uplink []byte) ([]byte, http.Header, error) {
	b, h, _, err := g.roundTripAt(ctx, auth, uplink, 0)
	return b, h, err
}

// roundTripAt issues one GET. Auth and later uplink both use Cookie session=.
func (g *GET) roundTripAt(ctx context.Context, auth, uplink []byte, seq uint64) ([]byte, http.Header, uint64, error) {
	var blob []byte
	var err error
	if len(auth) > 0 {
		blob = auth
	} else {
		payload := uplink
		if len(payload) == 0 {
			pad := make([]byte, 24+randN(80))
			_, _ = rand.Read(pad)
			payload, err = wire.EncodeBatch([]wire.Frame{{Cmd: wire.CmdPad, Payload: pad}})
			if err != nil {
				return nil, nil, seq, err
			}
		}
		blob, err = ecrypto.Seal(g.key, payload, []byte(g.sid))
		if err != nil {
			return nil, nil, seq, err
		}
	}
	if seq == 0 {
		seq = g.seq.Add(1)
	}
	ext := g.prof.Ext
	if ext == "" {
		ext = ".js"
	}
	url := fmt.Sprintf("%s%s/%s/%d%s", g.base, g.path, g.sid, seq, ext)
	// 2s was shorter than Google/Chrome TLS+HTTP2. Timed-out GETs used to
	// finishMux(empty) and skip that seq — the next DATA was a hole in the
	// TLS record stream (ERR_SSL_PROTOCOL_ERROR / LibreSSL bad decrypt).
	timeout := g.tripTimeout(len(auth) > 0)
	rctx, cancel := context.WithCancel(ctx)
	idle := time.AfterFunc(timeout, cancel)
	defer func() {
		idle.Stop()
		cancel()
	}()
	req, err := http.NewRequestWithContext(rctx, http.MethodGet, url, nil)
	if err != nil {
		return nil, nil, seq, err
	}
	dest := g.prof.FetchDest
	if dest == "" {
		dest = "script"
	}
	req.Header.Set("Accept", "*/*")
	req.Header.Set("Accept-Language", "ru-RU,ru;q=0.9,en-US;q=0.8,en;q=0.7")
	req.Header.Set("User-Agent", chromeUA)
	req.Header.Set("Sec-Fetch-Dest", dest)
	req.Header.Set("Sec-Fetch-Mode", "cors")
	req.Header.Set("Sec-Fetch-Site", "same-origin")
	req.Header.Set("Origin", g.base)
	req.Header.Set("Referer", g.base+"/")
	enc := byte(0)
	if len(auth) == 0 {
		enc = byte(g.uplinkEnc.Load())
	}
	hdr := wire.EncodeUplink(blob, enc)
	req.Header.Set("Cookie", "session="+hdr)
	req = req.WithContext(httptrace.WithClientTrace(req.Context(), &httptrace.ClientTrace{
		GotFirstResponseByte: func() { idle.Reset(timeout) },
	}))

	t0 := time.Now()
	cli := g.pickClient()
	if cli == nil {
		cli = g.client
	}
	resp, err := cli.Do(req)
	ttfb := time.Since(t0)
	if err != nil {
		g.noteTrip(ttfb, ttfb, len(blob), 0, err)
		// Do not finishMux(empty): that skips this seq and the next GET's
		// DATA lands in the middle of a TLS record (Chrome ERR_SSL_PROTOCOL_ERROR).
		return nil, nil, seq, err
	}
	defer resp.Body.Close()
	resp.Body = idleReader{ReadCloser: resp.Body, idle: idle, d: timeout}
	if resp.StatusCode == http.StatusMethodNotAllowed {
		g.noteTrip(ttfb, time.Since(t0), len(blob), 0, fmt.Errorf("status"))
		return nil, nil, seq, fmt.Errorf("GET front: method not allowed (use type get)")
	}
	if resp.StatusCode == http.StatusRequestEntityTooLarge || resp.StatusCode == 413 {
		g.noteTrip(ttfb, time.Since(t0), len(blob), 0, fmt.Errorf("status"))
		return nil, nil, seq, fmt.Errorf("GET front: header too large (413): lower header_budget")
	}
	if resp.StatusCode != http.StatusOK {
		slurp, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		g.noteTrip(ttfb, time.Since(t0), len(blob), 0, fmt.Errorf("status"))
		return nil, resp.Header, seq, fmt.Errorf("GET front: status %d: %s", resp.StatusCode, slurp)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	total := time.Since(t0)
	if err != nil {
		g.noteTrip(ttfb, total, len(blob), len(body), err)
		return nil, resp.Header, seq, err
	}
	g.noteTrip(ttfb, total, len(blob), len(body), nil)
	if sid := resp.Header.Get("X-Session"); sid != "" {
		g.sid = sid
	}
	if len(auth) > 0 {
		return body, resp.Header, seq, nil
	}
	var frames []wire.Frame
	if len(body) > 0 && g.key != nil && g.sid != "new" {
		plain, err := ecrypto.Open(g.key, body, []byte(g.sid))
		if err != nil {
			// Do not finishMux(empty): that punches a hole in the TLS stream.
			return nil, resp.Header, seq, err
		}
		frames, err = wire.DecodeBatch(plain)
		if err != nil {
			return nil, resp.Header, seq, err
		}
	}
	g.finishMux(seq, auth, frames)
	return body, resp.Header, seq, nil
}

// idleReader resets the GET idle timer on each body byte so a streaming
// bulk response is not killed by the TTFB budget.
type idleReader struct {
	io.ReadCloser
	idle *time.Timer
	d    time.Duration
}

func (r idleReader) Read(p []byte) (int, error) {
	n, err := r.ReadCloser.Read(p)
	if n > 0 && r.idle != nil {
		r.idle.Reset(r.d)
	}
	return n, err
}

func (g *GET) tripTimeout(auth bool) time.Duration {
	if auth {
		return 20 * time.Second
	}
	if d := g.prof.TripTimeout; d > 0 {
		return d
	}
	return 15 * time.Second
}

// finishMux applies downlink in GET seq order.
func (g *GET) finishMux(seq uint64, auth []byte, frames []wire.Frame) {
	if len(auth) > 0 {
		return
	}
	g.pushOrdered(seq, frames)
}

// pushOrdered applies downlink in GET seq order. Inflight>1 otherwise
// reorders CmdData and Chrome shows ERR_CONNECTION_CLOSED / TLS bad decrypt.
func (g *GET) pushOrdered(seq uint64, frames []wire.Frame) {
	g.mu.Lock()
	if !g.started.Load() || g.nextSeq == 0 {
		g.mu.Unlock()
		frames, applied, has := wire.SplitReport(frames)
		if has {
			g.noteApplied(applied)
		}
		g.sess.Push(frames)
		return
	}
	if seq < g.nextSeq {
		g.mu.Unlock()
		return
	}
	if seq > g.nextSeq {
		if len(g.held) >= 256 {
			g.mu.Unlock()
			g.die()
			return
		}
		g.held[seq] = frames
		g.mu.Unlock()
		return
	}
	var batch [][]wire.Frame
	batch = append(batch, frames)
	g.nextSeq++
	for {
		extra, ok := g.held[g.nextSeq]
		if !ok {
			break
		}
		delete(g.held, g.nextSeq)
		batch = append(batch, extra)
		g.nextSeq++
	}
	g.mu.Unlock()
	useful := false
	g.pushMu.Lock()
	for _, fs := range batch {
		fs, applied, has := wire.SplitReport(fs)
		if has {
			g.noteApplied(applied)
		}
		var keep []wire.Frame
		for _, f := range fs {
			if f.Cmd == wire.CmdTune {
				if t, ok := wire.ParseTune(f.Payload); ok {
					g.applyTune(t)
				}
				continue
			}
			keep = append(keep, f)
		}
		if muxBusy(keep) {
			useful = true
		}
		if len(keep) > 0 {
			g.sess.Push(keep)
		}
	}
	g.pushMu.Unlock()
	if useful {
		g.busy.Store(true)
		g.markLive()
	} else if !g.liveWindow() {
		g.busy.Store(false)
	}
}

// chromeUA is the browser User-Agent GET and WS send on the public path.
const chromeUA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7) AppleWebKit/537.36 (KHTML, like Gecko) Chrome/131.0.0.0 Safari/537.36"

// randN returns a random integer in [0, n).
func randN(n int) int {
	if n <= 0 {
		return 0
	}
	var b [2]byte
	_, _ = rand.Read(b[:])
	return int(b[0]) % n
}

// jitterDur returns d ± 1/3, not below d/2. Idle GET/Ping only — not the live 20ms path.
func jitterDur(d time.Duration) time.Duration {
	if d <= 0 {
		return d
	}
	span := d / 3
	if span <= 0 {
		return d
	}
	var b [4]byte
	_, _ = rand.Read(b[:])
	n := (int64(b[0])<<24 | int64(b[1])<<16 | int64(b[2])<<8 | int64(b[3])) & 0x7fffffff
	out := d - span + time.Duration(n%int64(span*2+1))
	if min := d / 2; out < min {
		return min
	}
	return out
}

// GETServer is the origin for the GET front (CDN or direct).
type GETServer struct {
	Profile cdn.Profile
	Token   []byte
	Path    string
	Decoy   []string
	Info    NodeInfo
	Seen    *ecrypto.Nonces
	H2Conns byte // advertised Status h2_conns; 0 = client keeps one

	mu       sync.Mutex
	sessions map[string]*serverSess
	hs       map[string]*serverSess // 0x02 nonce → sess (GET Cookie retry)
	tune     *frontTune
}

// serverSess is one GET session: ordered uplink, exclusive downlink Puller.
type serverSess struct {
	id         string
	key        []byte
	hello      []byte // eph_n || Seal(Status) for idempotent handshake retry
	mux        *mux.Session
	once       sync.Once
	mu         sync.Mutex
	cond       *sync.Cond
	nextSeq    uint64
	downSeq    uint64
	replied    uint64
	pulling    bool
	held       map[uint64][]wire.Frame
	skipPull   map[uint64]struct{}
	replies    map[uint64][]byte
	replyQ     []uint64
	lastGET    time.Time
	dropReason string
}

const replyCacheN = 32

// NewGETServer builds a GET origin with the generic CDN profile.
func NewGETServer(token, path string) *GETServer {
	return NewGETServerProfile(token, path, cdn.GenericGET())
}

// NewGETServerProfile builds a GET origin with a named CDN profile (path, ext, type).
func NewGETServerProfile(token, path string, prof cdn.Profile) *GETServer {
	if path == "" {
		path = prof.Path
	}
	if prof.Ext == "" {
		prof.Ext = ".woff2"
	}
	if prof.ContentType == "" {
		prof.ContentType = "font/woff2"
	}
	return &GETServer{
		Profile:  prof,
		Token:    []byte(token),
		Path:     strings.TrimRight(path, "/"),
		Seen:     ecrypto.NewNonces(),
		sessions: map[string]*serverSess{},
		hs:       map[string]*serverSess{},
		tune:     newFrontTune(prof),
	}
}

// contentType is the response Content-Type (default font/woff2).
func (s *GETServer) contentType() string {
	if s.Profile.ContentType != "" {
		return s.Profile.ContentType
	}
	return "font/woff2"
}

// Match reports whether p is under this front's asset path.
func (s *GETServer) Match(p string) bool {
	return strings.HasPrefix(p, s.Path+"/")
}

// ServeHTTP handles the first GET (auth) and later GETs (header uplink, exclusive Puller).
func (s *GETServer) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet && r.Method != http.MethodHead {
		w.Header().Set("Allow", "GET, HEAD")
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}
	setCDNHeaders(w)
	sid, seqStr := parseGETPath(s.Path, r.URL.Path)
	seq, _ := strconv.ParseUint(seqStr, 10, 64)
	blob, _ := wire.DecodeUplink(uplinkHeader(r))

	var ss *serverSess
	tryHS := ecrypto.LooksLikeAuth(blob)
	if tryHS {
		a, err := frontAuth(s.Token, blob, s.Seen)
		if err != nil {
			ss = s.get(sid)
			if ss == nil {
				http.NotFound(w, r)
				traceMISS(sid, seq, "auth", r)
				return
			}
		} else {
			if a.Replay {
				s.mu.Lock()
				ss = s.hs[string(a.Nonce)]
				s.mu.Unlock()
				if ss == nil || len(ss.hello) == 0 {
					http.NotFound(w, r)
					traceMISS(sid, seq, "auth", r)
					return
				}
				w.Header().Set("X-Session", ss.id)
				w.Header().Set("Content-Type", s.contentType())
				w.Header().Set("Content-Length", strconv.Itoa(len(ss.hello)))
				if r.Method != http.MethodHead {
					_, _ = io.Copy(w, bytes.NewReader(ss.hello))
				}
				traceHS(ss, r, seq)
				return
			}
			ss = s.spawnWant(a.Key, a.Rest, a.Token, sid)
			if seq > 0 {
				ss.nextSeq = seq + 1
			} else {
				ss.nextSeq = 2
			}
			ss.downSeq = ss.nextSeq
			if ss.nextSeq > 0 {
				ss.replied = ss.nextSeq - 1
			}
			w.Header().Set("X-Session", ss.id)
			sid = ss.id
			st := s.Info.Status(ss.id)
			if len(s.Info.Token) == 0 {
				// Overlay instance_id is Hello, not the GET HTTP sid (X-Session).
				st = withDecoy(netconf.DefaultSession().Status(wire.OK, "", "", "", 1280, 0), s.Decoy)
			}
			finishStatus(&st, a.Token, ss.id, a.Rest, ss.mux, remoteAddr(r.RemoteAddr))
			if st.InstanceID == "" {
				if h, err := wire.DecodeHello(a.Rest); err == nil {
					st.InstanceID = h.InstanceID
				}
			}
			if s.tune != nil {
				st.HeaderBudget, st.Inflight = s.tune.advertised()
			}
			st.UplinkEnc = wire.UplinkCookie85
			st.H2Conns = s.H2Conns
			sealed, err := ecrypto.Seal(ss.key, st.Encode(), []byte(ss.id))
			if err != nil {
				http.Error(w, "unavailable", http.StatusBadGateway)
				return
			}
			ss.hello = ecrypto.WrapHello(a.EphN, sealed)
			s.mu.Lock()
			s.hs[string(a.Nonce)] = ss
			s.mu.Unlock()
			w.Header().Set("Content-Type", s.contentType())
			w.Header().Set("Content-Length", strconv.Itoa(len(ss.hello)))
			if r.Method != http.MethodHead {
				_, _ = io.Copy(w, bytes.NewReader(ss.hello))
			}
			traceHS(ss, r, seq)
			return
		}
	}
	if ss == nil {
		ss = s.get(sid)
		if ss == nil {
			http.NotFound(w, r)
			traceMISS(sid, seq, "nosid", r)
			return
		}
	}
	var frames []wire.Frame
	if len(blob) > 0 {
		plain, err := ecrypto.Open(ss.key, blob, []byte(ss.id))
		if err != nil {
			// Do not ingest as empty: that advances nextSeq and the
			// client retry of the same seq is dropped (TLS bad MAC).
			http.Error(w, "unavailable", http.StatusBadGateway)
			traceGET(ss, r, seq, nil, "badmac", false, 0)
			return
		}
		frames, err = wire.DecodeBatch(plain)
		if err != nil {
			http.Error(w, "unavailable", http.StatusBadGateway)
			traceGET(ss, r, seq, nil, "badwire", false, 0)
			return
		}
	}
	// HTTP/2 waiters sit until they are Puller. Jumping downSeq on
	// a 200ms timeout skipped Pulls and live GET download lagged WS.
	// HTTP/1.1: a Data GET must not sit behind another conn.
	// Upload Data on HTTP/2 used to wait the same 5s Puller queue:
	// Yandex Cookie TTFB went 238ms (pad) → 973ms (14 KiB up) and
	// CDN upload sat at ~3 Mbit. Return empty at once if not Puller;
	// jump stays false so downSeq is not skipped.
	turn := getH2PullerWait
	jump := false
	if r.ProtoMajor < 2 {
		jump = true
		if usefulMux(frames) {
			turn = 0
		} else {
			turn = getTurnWait
		}
	} else if usefulMux(frames) {
		turn = 0
	}
	frames, ev := wire.SplitTune(frames)
	if ev != 0 {
		s.tune.observe(ev)
	}
	if seq > 0 {
		if cached := ss.replayReply(seq); cached != nil {
			s.writeSealed(w, r, cached)
			traceGET(ss, r, seq, frames, "replay", false, 0)
			return
		}
	}
	if ev == 0 {
		s.tune.recover()
	}
	tPull := time.Now()
	down, pulled := ss.ingestAndPull(seq, frames, turn, jump)
	wait := time.Since(tPull)
	down = ss.withReport(down)
	if tf, ok := s.tune.takeReply(); ok {
		down = append([]wire.Frame{tf}, down...)
	}
	if ss.mux.Closed() {
		s.drop(ss.id)
	}
	if pulled {
		sealed, err := s.sealDown(ss, down)
		if err != nil {
			ss.endPull(seq)
			http.Error(w, "unavailable", http.StatusBadGateway)
			traceGET(ss, r, seq, frames, "seal", true, wait)
			return
		}
		sealed = ss.rememberReply(seq, sealed)
		ss.endPull(seq)
		s.writeSealed(w, r, sealed)
		traceGET(ss, r, seq, frames, "ingest", true, wait)
		return
	}
	if cached := ss.replayReply(seq); cached != nil {
		s.writeSealed(w, r, cached)
		traceGET(ss, r, seq, frames, "replay", false, wait)
		return
	}
	s.writeGET(w, r, ss, seq, down)
	traceGET(ss, r, seq, frames, viaAfterPull(ss, seq, false, false), false, wait)
}

// writeGET seals downlink (with pad to ~400 bytes) and writes the GET body.
func (s *GETServer) writeGET(w http.ResponseWriter, r *http.Request, ss *serverSess, seq uint64, down []wire.Frame) {
	sealed, err := s.sealDown(ss, down)
	if err != nil {
		http.Error(w, "unavailable", http.StatusBadGateway)
		return
	}
	if seq > 0 {
		sealed = ss.rememberReply(seq, sealed)
	}
	s.writeSealed(w, r, sealed)
}

func (s *GETServer) sealDown(ss *serverSess, down []wire.Frame) ([]byte, error) {
	rawSize := 0
	for _, f := range down {
		rawSize += wire.HeaderSize + len(f.Payload)
	}
	if rawSize < 400 {
		pad := make([]byte, 400-rawSize+randN(200))
		_, _ = rand.Read(pad)
		down = append(down, wire.Frame{Cmd: wire.CmdPad, Payload: pad})
	}
	payload, err := wire.EncodeBatch(down)
	if err != nil {
		return nil, err
	}
	return ecrypto.Seal(ss.key, payload, []byte(ss.id))
}

func (s *GETServer) writeSealed(w http.ResponseWriter, r *http.Request, sealed []byte) {
	w.Header().Set("Content-Type", s.contentType())
	w.Header().Set("Content-Length", strconv.Itoa(len(sealed)))
	if r.Method == http.MethodHead {
		return
	}
	_, _ = io.Copy(w, bytes.NewReader(sealed))
}

// spawn allocates a GET HTTP sid. Mux is adopted on handshake (member +
// instance_id + CapHold) the same way WS does, so a hold-gap handshake
// does not RST inner TCP. A second serveMux is not started on reuse.
func (s *GETServer) spawn(key, rest, token []byte) *serverSess {
	return s.spawnWant(key, rest, token, "")
}

// spawnWant is spawn with a client-chosen sid (24 hex). Path "new" and
// collisions fall back to RandomID; X-Session still echoes the live id.
func (s *GETServer) spawnWant(key, rest, token []byte, wantID string) *serverSess {
	ms, reused := adoptServerMux(rest, token)
	if !reused {
		if inst := helloInstance(rest); inst != "" {
			if old := s.takeByInstance(parkOwner(token), inst); old != nil && old.mux != nil && !old.mux.Closed() {
				// adoptServerMux already made a mux; drop it, keep the live one.
				ms.Close()
				ms = old.mux
				reused = true
			}
		}
	}
	id := wantID
	if !validGETSID(id) {
		id = ecrypto.RandomID()
	}
	ss := &serverSess{
		id:       id,
		key:      key,
		mux:      ms,
		held:     map[uint64][]wire.Frame{},
		skipPull: map[uint64]struct{}{},
		replies:  map[uint64][]byte{},
	}
	ss.cond = sync.NewCond(&ss.mu)
	s.mu.Lock()
	if s.sessions[ss.id] != nil {
		ss.id = ecrypto.RandomID()
	}
	s.sessions[ss.id] = ss
	s.mu.Unlock()
	if !reused {
		ss.once.Do(func() {
			go serveMux(ss.mux)
			go serveDgrams(ss.mux)
		})
	}
	return ss
}

// takeByInstance takes a live GET mux for this member+instance under another HTTP sid.
// GET has no connection drop, so the mux lives in the session map, not
// only in serverPark. Caller must not Close the returned mux.
func (s *GETServer) takeByInstance(owner, inst string) *serverSess {
	if inst == "" {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	for id, ss := range s.sessions {
		if ss == nil || ss.mux == nil {
			continue
		}
		if ss.mux.Owner() != owner || ss.mux.Instance() != inst || ss.mux.Closed() {
			continue
		}
		delete(s.sessions, id)
		return ss
	}
	return nil
}

// get returns the live session for id, or nil.
func (s *GETServer) get(id string) *serverSess {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.sessions[id]
}

// GET Puller hold. Idle pads used to sit 250ms as exclusive Puller;
// ICMP and TCP 1-byte then waited that plus an HTTP/2 waiter jump.
const (
	getDownlinkBytes = 256 << 10
	// Several streams (Speedtest ping during download). 32 KiB rotated
	// the Puller faster but on a CDN RTT (~240 ms) cut multi-stream
	// download to ~1–3 Mbit. other-stream pri already lets ping jump
	// mux.out; keep live bodies near the single-stream size.
	getDownlinkLive = 192 << 10
	// Fat Cookie uplink: cap this GET body so the slot recycles. Download
	// -only GETs keep 256/192 KiB. 32 KiB was too small on a 240 ms RTT.
	getDownlinkUp   = 96 << 10
	getDownlinkMore = 15 * time.Millisecond
	getDownlinkFill = 3 * time.Millisecond
	// Overlay ICMP is sync (FilterDgram). Internet ICMP with no live
	// TCP stream is async: hold this GET so the echo-reply can ride
	// the same body. pullHold skips this when StreamCount()>0.
	getDgramHold = 80 * time.Millisecond
	getTurnWait  = 5 * time.Millisecond
	// HTTP/2 waiters poll until they are Puller. Safety cap only.
	getH2PullerWait = 5 * time.Second
)

// pullHold is how long the exclusive Puller waits for mux downlink.
// Empty keepalive pads return immediately so they do not stall ping/upload.
func pullHold(frames []wire.Frame, ms *mux.Session) (first, more time.Duration) {
	if ms != nil && ms.OutQueued() > 0 {
		return 0, getDownlinkFill
	}
	n := muxDataBytes(frames)
	if n >= 2048 {
		return 0, 0
	}
	if n > 0 || hasCmd(frames, wire.CmdOpen) {
		return getDownlinkMore, getDownlinkFill
	}
	if hasCmd(frames, wire.CmdDgram) || hasCmd(frames, wire.CmdPing) {
		// Overlay ICMP is sync (FilterDgram). Internet ICMP is async:
		// hold this GET so the echo-reply can ride the same body.
		// TUN default dumps Apple QUIC/ICMP into CmdDgram; parking the
		// exclusive Puller 80ms while TCP streams are live lets ACKs
		// wait behind those GETs and the download window stalls.
		if ms != nil && ms.StreamCount() > 0 {
			return 0, getDownlinkFill
		}
		return getDgramHold, getDownlinkFill
	}
	return 0, 0
}

// muxDataBytes is the total CmdData payload in frames (bulk-upload detector).
func muxDataBytes(frames []wire.Frame) int {
	n := 0
	for _, f := range frames {
		if f.Cmd == wire.CmdData {
			n += len(f.Payload)
		}
	}
	return n
}

// usefulMux is true when frames carry Open, Data, Dgram, Leave, or Close.
func usefulMux(frames []wire.Frame) bool {
	for _, f := range frames {
		switch f.Cmd {
		case wire.CmdPad, wire.CmdPing, wire.CmdPong, wire.CmdTune, wire.CmdReport:
			continue
		default:
			return true
		}
	}
	return false
}

// hasCmd reports whether any frame is cmd.
func hasCmd(frames []wire.Frame, cmd byte) bool {
	for _, f := range frames {
		if f.Cmd == cmd {
			return true
		}
	}
	return false
}

// downlinkMax is the GET body cap. Download-only stays 256/192 KiB.
// ≥2 KiB CmdData uplink shrinks to 96 KiB so Cookie GETs recycle.
func downlinkMax(ms *mux.Session, up []wire.Frame) int {
	max := getDownlinkBytes
	if ms != nil && ms.StreamCount() > 1 {
		max = getDownlinkLive
	}
	if muxDataBytes(up) >= 2<<10 && max > getDownlinkUp {
		max = getDownlinkUp
	}
	return max
}

// pullDownlink holds up to first for mux, then more after useful data.
func pullDownlink(ms *mux.Session, first, more time.Duration, up []wire.Frame) []wire.Frame {
	deadline := time.Now()
	if first > 0 {
		deadline = time.Now().Add(first)
	}
	max := downlinkMax(ms, up)
	var down []wire.Frame
	n := 0
	for n < max {
		wait := time.Until(deadline)
		if wait < 0 {
			wait = 0
		}
		chunk := ms.Pull(max-n, wait)
		if len(chunk) == 0 {
			if usefulMux(down) || wait == 0 {
				break
			}
			continue
		}
		down = append(down, chunk...)
		for _, f := range chunk {
			n += wire.HeaderSize + len(f.Payload)
		}
		if usefulMux(down) {
			if more <= 0 {
				deadline = time.Now()
			} else {
				deadline = time.Now().Add(more)
			}
		}
	}
	return down
}

// ingestAndPull applies uplink in seq order. Only the in-order GET Pulls
// mux downlink. pullLoop assigned bodies to whatever GET was free; if that
// HTTP response was dropped, mux Data vanished (Speedtest/YouTube TLS holes).
func (ss *serverSess) ingestAndPull(seq uint64, frames []wire.Frame, waitTurn time.Duration, jump bool) ([]wire.Frame, bool) {
	if seq == 0 {
		if len(frames) > 0 {
			ss.mux.Push(frames)
		}
		first, more := pullHold(frames, ss.mux)
		return pullDownlink(ss.mux, first, more, frames), false
	}
	ss.mu.Lock()
	ss.pushUplinkLocked(seq, frames)
	if ss.downSeq == 0 {
		ss.downSeq = seq
	}
	ss.drainSkipPullLocked()
	if seq < ss.downSeq {
		ss.mu.Unlock()
		return nil, false
	}
	deadline := time.Time{}
	if waitTurn > 0 {
		deadline = time.Now().Add(waitTurn)
	}
	for seq != ss.downSeq || ss.pulling {
		if seq < ss.downSeq {
			ss.mu.Unlock()
			return nil, false
		}
		ss.drainSkipPullLocked()
		if seq == ss.downSeq && !ss.pulling {
			break
		}
		notTurn := seq != ss.downSeq
		timedOut := (waitTurn <= 0 && notTurn) || (!deadline.IsZero() && time.Now().After(deadline))
		if timedOut {
			if jump && notTurn && seq > ss.replied {
				ss.replied = seq
			} else if notTurn {
				if ss.skipPull == nil {
					ss.skipPull = map[uint64]struct{}{}
				}
				if len(ss.skipPull) >= 256 {
					ss.dropReason = "skip_overflow"
					ss.mu.Unlock()
					ss.mux.Close()
					return nil, false
				}
				ss.skipPull[seq] = struct{}{}
			}
			if ss.cond != nil {
				ss.cond.Broadcast()
			}
			ss.mu.Unlock()
			return nil, false
		}
		if ss.cond == nil {
			ss.mu.Unlock()
			time.Sleep(time.Millisecond)
			ss.mu.Lock()
			continue
		}
		wait := 2 * time.Millisecond
		if !deadline.IsZero() {
			if remain := time.Until(deadline); remain < wait {
				wait = remain
			}
		}
		if wait < 0 {
			wait = 0
		}
		timer := time.AfterFunc(wait, func() {
			ss.mu.Lock()
			if ss.cond != nil {
				ss.cond.Broadcast()
			}
			ss.mu.Unlock()
		})
		ss.cond.Wait()
		timer.Stop()
	}
	if seq < ss.downSeq {
		ss.mu.Unlock()
		return nil, false
	}
	ss.pulling = true
	ss.mu.Unlock()
	first, more := pullHold(frames, ss.mux)
	down := pullDownlink(ss.mux, first, more, frames)
	return down, true
}

func (ss *serverSess) endPull(seq uint64) {
	ss.mu.Lock()
	defer ss.mu.Unlock()
	if !ss.pulling {
		return
	}
	ss.pulling = false
	if seq > ss.replied {
		ss.replied = seq
	}
	ss.downSeq = ss.replied + 1
	ss.drainSkipPullLocked()
	if ss.cond != nil {
		ss.cond.Broadcast()
	}
}

func (ss *serverSess) rememberReply(seq uint64, body []byte) []byte {
	if seq == 0 || len(body) == 0 {
		return body
	}
	ss.mu.Lock()
	defer ss.mu.Unlock()
	if ss.replies == nil {
		ss.replies = map[uint64][]byte{}
	}
	if prev, ok := ss.replies[seq]; ok {
		return prev
	}
	ss.replies[seq] = body
	ss.replyQ = append(ss.replyQ, seq)
	for len(ss.replyQ) > replyCacheN {
		old := ss.replyQ[0]
		ss.replyQ = ss.replyQ[1:]
		delete(ss.replies, old)
	}
	return body
}

func (ss *serverSess) replayReply(seq uint64) []byte {
	if seq == 0 {
		return nil
	}
	ss.mu.Lock()
	defer ss.mu.Unlock()
	if ss.replies == nil {
		return nil
	}
	return ss.replies[seq]
}

// withReport prepends CmdReport: highest contiguous ingested GET seq.
func (ss *serverSess) withReport(down []wire.Frame) []wire.Frame {
	ss.mu.Lock()
	applied := uint64(0)
	if ss.nextSeq > 0 {
		applied = ss.nextSeq - 1
	}
	ss.mu.Unlock()
	return append([]wire.Frame{wire.ReportFrame(applied)}, down...)
}

// drainSkipPullLocked advances downSeq over Data GETs that already
// returned empty (HTTP/2 upload workers). ss.mu must be held.
func (ss *serverSess) drainSkipPullLocked() {
	if ss.skipPull == nil {
		return
	}
	for {
		if _, ok := ss.skipPull[ss.downSeq]; !ok {
			return
		}
		delete(ss.skipPull, ss.downSeq)
		ss.downSeq++
		if ss.replied < ss.downSeq-1 {
			ss.replied = ss.downSeq - 1
		}
	}
}

// pushUplink applies uplink in seq order, holding out-of-order GETs.
func (ss *serverSess) pushUplink(seq uint64, frames []wire.Frame) {
	if seq == 0 {
		ss.mux.Push(frames)
		return
	}
	ss.mu.Lock()
	defer ss.mu.Unlock()
	ss.pushUplinkLocked(seq, frames)
}

// pushUplinkLocked is pushUplink with ss.mu already held.
func (ss *serverSess) pushUplinkLocked(seq uint64, frames []wire.Frame) {
	if ss.nextSeq == 0 {
		ss.nextSeq = seq
	}
	if seq < ss.nextSeq {
		return
	}
	if seq > ss.nextSeq {
		// Inflight 48 plus the next wave (Speedtest download→upload)
		// used to hit 64 and Close the mux: download looked fine, upload died.
		if len(ss.held) >= 256 {
			ss.dropReason = "held_overflow"
			ss.mux.Close()
			return
		}
		ss.held[seq] = frames
		return
	}
	ss.mux.Push(frames)
	ss.nextSeq++
	for {
		extra, ok := ss.held[ss.nextSeq]
		if !ok {
			break
		}
		delete(ss.held, ss.nextSeq)
		ss.mux.Push(extra)
		ss.nextSeq++
	}
}

// drop forgets the GET HTTP sid. CapHold parks the mux (WS dropServerMux)
// so a later handshake with the same instance_id can adopt it. Leave already
// closed the mux; park then no-ops.
func (s *GETServer) drop(id string) {
	s.mu.Lock()
	ss := s.sessions[id]
	delete(s.sessions, id)
	s.mu.Unlock()
	if ss != nil {
		ss.mu.Lock()
		reason := ss.dropReason
		last := ss.replied
		ss.mu.Unlock()
		if reason == "" {
			reason = "mux"
		}
		sessEvent("DROP", "sid="+shortSID(id), fmt.Sprintf("last_seq=%d", last), "reason="+reason)
		dropServerMux(ss.mux)
	}
}

// parseGETPath splits {base}/{session}/{seq}.ext into sid and seq.
func parseGETPath(prefix, p string) (sid string, seq string) {
	rest := strings.TrimPrefix(p, prefix+"/")
	parts := strings.Split(rest, "/")
	if len(parts) == 0 {
		return "", ""
	}
	sid = parts[0]
	if len(parts) > 1 {
		seq = strings.TrimSuffix(parts[1], filepath.Ext(parts[1]))
	}
	return sid, seq
}

// setCDNHeaders disables caching so a CDN does not store AEAD bodies.
func setCDNHeaders(w http.ResponseWriter) {
	w.Header().Set("Cache-Control", "private, no-store, no-cache, must-revalidate, max-age=0")
	w.Header().Set("Pragma", "no-cache")
	w.Header().Set("Expires", "0")
	w.Header().Set("CDN-Cache-Control", "no-store")
	w.Header().Set("X-Accel-Expires", "0")
	w.Header().Set("Vary", "Cookie")
}

// uplinkHeader is Cookie session= (GET uplink). An edge may prepend
// its own Cookie; scan every Cookie header.
func uplinkHeader(r *http.Request) string {
	if r == nil {
		return ""
	}
	for _, raw := range r.Header.Values("Cookie") {
		if v := sessionCookieValue(raw); v != "" {
			return v
		}
	}
	return ""
}

func sessionCookieValue(raw string) string {
	const key = "session="
	for _, part := range strings.Split(raw, ";") {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, key) {
			return strings.TrimPrefix(part, key)
		}
	}
	return ""
}

// validGETSID is a client-chosen GET HTTP sid (RandomID: 24 lowercase hex).
func validGETSID(sid string) bool {
	if len(sid) != 24 {
		return false
	}
	for i := 0; i < len(sid); i++ {
		c := sid[i]
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return false
		}
	}
	return true
}

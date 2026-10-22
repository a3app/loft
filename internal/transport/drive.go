package transport

// Drive-debug log: handshake, radio, overlay ping, stalls, path, resumes.
// iPhone hotspot keeps 172.20.10.7 across tower hops; WatchPath misses those
// stalls, so WatchStall redials when mux ping or radio TCP dies.

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/a3app/loft/internal/mux"
	"github.com/a3app/loft/internal/protect"
)

// Drive writes one-line events to a writer (file + stderr).
type Drive struct {
	mu       sync.Mutex
	w        io.Writer
	f        *os.File
	front    atomic.Value // string host:port
	last     PathInfo
	fails    int
	ticks    int
	fired    time.Time
	seenByte time.Time
	prevUp   uint64
	prevDn   uint64
	prime    bool
}

var driveLog atomic.Pointer[Drive]

// SetDrive installs the drive logger used by SuperviseOn. Nil clears it.
func SetDrive(d *Drive) { driveLog.Store(d) }

// NewDrive writes timestamped events to w.
func NewDrive(w io.Writer) *Drive {
	return &Drive{w: w}
}

// DefaultDriveLog is ~/Library/Logs/loft-drive-YYYYMMDD-HHMMSS.log on Darwin.
func DefaultDriveLog() string {
	stamp := time.Now().Format("loft-drive-20060102-150405.log")
	home, err := os.UserHomeDir()
	if err != nil {
		return filepath.Join(os.TempDir(), stamp)
	}
	dir := filepath.Join(home, "Library", "Logs")
	if runtime.GOOS != "darwin" {
		dir = filepath.Join(home, ".loft", "logs")
	}
	_ = os.MkdirAll(dir, 0755)
	return filepath.Join(dir, stamp)
}

// OpenDriveLog appends to path and tees lines to stderr.
func OpenDriveLog(path string) (*Drive, error) {
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		return nil, err
	}
	return &Drive{w: io.MultiWriter(f, os.Stderr), f: f}, nil
}

// Close flushes the log file.
func (d *Drive) Close() error {
	if d == nil || d.f == nil {
		return nil
	}
	return d.f.Close()
}

// SetFront stores the outbound host:port for node TCP probes.
func (d *Drive) SetFront(raw string) {
	if d == nil {
		return
	}
	d.front.Store(frontHostPort(raw))
}

// Event writes one line: RFC3339 ms, kind, then key=value fields.
func (d *Drive) Event(kind string, kv ...string) {
	if d == nil {
		return
	}
	ts := time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
	var b strings.Builder
	b.WriteString(ts)
	b.WriteByte(' ')
	b.WriteString(kind)
	for _, p := range kv {
		p = strings.ReplaceAll(p, "\n", " ")
		p = strings.ReplaceAll(p, "\r", " ")
		if p == "" {
			continue
		}
		b.WriteByte(' ')
		b.WriteString(p)
	}
	b.WriteByte('\n')
	line := b.String()
	d.mu.Lock()
	defer d.mu.Unlock()
	if d.w != nil {
		_, _ = io.WriteString(d.w, line)
	}
	if d.f != nil {
		_ = d.f.Sync()
	}
}

func driveEvent(kind string, kv ...string) {
	if d := driveLog.Load(); d != nil {
		d.Event(kind, kv...)
	}
}

func driveMs(d time.Duration) string {
	if d < 0 {
		d = 0
	}
	return fmt.Sprintf("%dms", d.Milliseconds())
}

func driveErr(err error) string {
	if err == nil {
		return ""
	}
	s := strings.ReplaceAll(err.Error(), "\n", " ")
	if len(s) > 180 {
		s = s[:180] + "..."
	}
	return s
}

func frontHostPort(raw string) string {
	s := strings.TrimSpace(raw)
	s = strings.Replace(s, "wss://", "https://", 1)
	s = strings.Replace(s, "ws://", "http://", 1)
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil {
		return ""
	}
	host := u.Host
	if host == "" {
		return ""
	}
	if _, _, err := net.SplitHostPort(host); err != nil {
		port := "443"
		if u.Scheme == "http" || u.Scheme == "ws" {
			port = "80"
		}
		host = net.JoinHostPort(u.Hostname(), port)
	}
	return host
}

const (
	stallNeed   = 3
	stallCool   = 8 * time.Second
	stallQuiet  = 4 * time.Second
	nodeProbeTO = 2500 * time.Millisecond
	muxPingTO   = 2500 * time.Millisecond
)

// stallTick is WatchStall's sample period. Tests shorten it.
var stallTick = time.Second

// WatchStall samples path/radio/overlay ping once a second. A brief cell
// hole keeps mux. Persistent mux ping-dead while the node SYN still works
// is GET HOL: fire a path event so Supervise rebinds GET (or Parks others).
// d may be nil: stall still fires, TICK lines stay off.
func WatchStall(ctx context.Context, h *Holder, d *Drive) <-chan struct{} {
	ch := make(chan struct{}, 1)
	if h == nil {
		return ch
	}
	if d == nil {
		d = NewDrive(io.Discard)
	}
	go func() {
		t := time.NewTicker(stallTick)
		defer t.Stop()
		d.last = SamplePath()
		d.Event("PATH", pathKV(d.last)...)
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				d.tick(ctx, h, ch)
			}
		}
	}()
	return ch
}

func (d *Drive) tick(ctx context.Context, h *Holder, fire chan<- struct{}) {
	p := SamplePath()
	if p.Key() != d.last.Key() {
		d.Event("PATH", append(pathKV(p), "was="+d.last.Egress)...)
		d.last = p
	}
	var (
		radioMs, nodeMs, pingMs    time.Duration
		radioErr, nodeErr, pingErr error
		wg                         sync.WaitGroup
	)
	wg.Add(1)
	go func() {
		defer wg.Done()
		radioMs, radioErr = probeRadio(ctx)
	}()
	if host, _ := d.front.Load().(string); host != "" {
		wg.Add(1)
		go func() {
			defer wg.Done()
			nodeMs, nodeErr = probeTCP(ctx, host, nodeProbeTO)
		}()
	}
	sess := h.Session()
	if sess != nil {
		wg.Add(1)
		go func() {
			defer wg.Done()
			pctx, cancel := context.WithTimeout(ctx, muxPingTO)
			defer cancel()
			pingMs, pingErr = sess.Ping(pctx)
		}()
	}
	wg.Wait()
	// Mux payload only. GET pad HTTP would look like live overlay and hide HOL.
	var up, down uint64
	if sess != nil {
		up, down = sess.Bytes()
	}
	var ru, rd uint64
	if d.prime {
		if up >= d.prevUp {
			ru = up - d.prevUp
		}
		if down >= d.prevDn {
			rd = down - d.prevDn
		}
	}
	d.prime = true
	d.prevUp, d.prevDn = up, down
	d.ticks++
	kv := pathKV(p)
	kv = append(kv,
		"radio="+probeKV(radioMs, radioErr),
		"node="+probeKV(nodeMs, nodeErr),
		"ping="+pingKV(sess, pingMs, pingErr),
		fmt.Sprintf("up=%d", ru),
		fmt.Sprintf("down=%d", rd),
		"overlay="+OverlayIP(),
		fmt.Sprintf("live=%d", btoi(sess != nil && !sess.Closed())),
	)
	d.Event("TICK", kv...)
	if d.ticks%10 == 0 {
		if s := h.TripSummary(); s != "" {
			d.Event("GET", s)
		}
	}

	if sess == nil {
		d.fails = 0
		return
	}
	if sess.Closed() {
		if d.fired.IsZero() || time.Since(d.fired) >= stallCool {
			d.fireStall(p, fire, "mux")
		}
		return
	}
	radioDead := radioErr != nil
	pingDead := pingErr != nil
	nodeDead := nodeErr != nil
	if ru > 0 || rd > 0 {
		d.seenByte = time.Now()
	}
	recent := !d.seenByte.IsZero() && time.Since(d.seenByte) < stallQuiet
	// Ping-dead while GET bodies are still moving is bulk, not HOL.
	// Rebind then cancels in-flight GETs and RST the SOCKS stream.
	stall := overlayStall(pingDead, radioDead, nodeDead, recent)
	if stall {
		d.Event("STALL", "reason="+stallWhy(radioDead, pingDead, nodeDead), "radio="+probeKV(radioMs, radioErr), "ping="+pingKV(sess, pingMs, pingErr), "node="+probeKV(nodeMs, nodeErr))
	}
	if d.noteStall(stall) {
		d.fireStall(p, fire, "stall")
	}
}

func (d *Drive) fireStall(p PathInfo, fire chan<- struct{}, why string) {
	if d == nil {
		return
	}
	d.fired = time.Now()
	d.fails = 0
	if fire != nil {
		select {
		case fire <- struct{}{}:
		default:
		}
	}
	d.Event("PATH", append(pathKV(p), "why="+why)...)
}

// overlayStall is persistent HOL: mux ping-dead and no overlay bytes for
// stallQuiet, or both radio and node SYN dead. Recent bytes are bulk.
func overlayStall(pingDead, radioDead, nodeDead, recentBytes bool) bool {
	if recentBytes {
		return radioDead && nodeDead
	}
	return pingDead || (radioDead && nodeDead)
}

// noteStall counts consecutive stall samples. True after stallNeed in a
// row, then cooldown so GET rebind does not churn the H2 pool every tick.
func (d *Drive) noteStall(stall bool) bool {
	if d == nil {
		return false
	}
	if !stall {
		d.fails = 0
		return false
	}
	d.fails++
	if d.fails < stallNeed {
		return false
	}
	if !d.fired.IsZero() && time.Since(d.fired) < stallCool {
		return false
	}
	d.fired = time.Now()
	d.fails = 0
	return true
}

func stallWhy(radio, ping, node bool) string {
	switch {
	case radio && ping && node:
		return "radio+ping+node"
	case radio && ping:
		return "radio+ping"
	case radio && node:
		return "radio+node"
	case ping && node:
		return "ping+node"
	case node:
		return "node"
	case radio:
		return "radio"
	default:
		return "ping"
	}
}

func pathKV(p PathInfo) []string {
	return []string{
		"if=" + p.IfName,
		"ip=" + p.IP,
		"gw=" + p.GW,
		"egress=" + p.Egress,
	}
}

func probeKV(dt time.Duration, err error) string {
	if err != nil {
		return "dead/" + driveMs(dt)
	}
	return "ok/" + driveMs(dt)
}

func pingKV(sess *mux.Session, dt time.Duration, err error) string {
	if sess == nil {
		return "n/a"
	}
	if err != nil {
		return "dead/" + driveMs(dt)
	}
	return "ok/" + driveMs(dt)
}

func btoi(v bool) int {
	if v {
		return 1
	}
	return 0
}

func probeRadio(ctx context.Context) (time.Duration, error) {
	if g := defaultGateway(); g != "" {
		dt, err := probeTCP(ctx, net.JoinHostPort(g, "80"), 400*time.Millisecond)
		if radioUp(err) {
			return dt, nil
		}
	}
	dt, err := probeTCP(ctx, "1.1.1.1:443", 500*time.Millisecond)
	if radioUp(err) {
		return dt, nil
	}
	dt, err = probeTCP(ctx, "8.8.8.8:443", 500*time.Millisecond)
	if radioUp(err) {
		return dt, nil
	}
	return dt, err
}

func radioUp(err error) bool {
	if err == nil {
		return true
	}
	s := strings.ToLower(err.Error())
	return strings.Contains(s, "refused") || strings.Contains(s, "reset")
}

func probeTCP(ctx context.Context, addr string, d time.Duration) (time.Duration, error) {
	if addr == "" {
		return 0, fmt.Errorf("empty")
	}
	nctx, cancel := context.WithTimeout(ctx, d)
	defer cancel()
	t0 := time.Now()
	c, err := protect.NetDialer(d).DialContext(nctx, "tcp4", addr)
	dt := time.Since(t0)
	if err != nil {
		return dt, err
	}
	_ = c.Close()
	return dt, nil
}

package ndms

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/a3app/loft/clients/go/inbound"
	"github.com/a3app/loft/internal/config"
	"github.com/a3app/loft/internal/transport"
	"github.com/a3app/loft/internal/trojan"
	"github.com/a3app/loft/internal/vless"
	"github.com/a3app/loft/internal/wire"
)

// Snap is the child's status file. The panel reads it.
type Snap struct {
	State   string   `json:"state"` // connecting, up, error or off
	Err     string   `json:"err,omitempty"`
	Overlay string   `json:"overlay,omitempty"`
	Gateway string   `json:"gateway,omitempty"` // loft only; proxy tuns have no overlay gateway
	MTU     int      `json:"mtu,omitempty"`
	Front   string   `json:"front,omitempty"` // live outbound type
	Node    string   `json:"node,omitempty"`  // hostname of the live front
	Reserve int      `json:"reserve,omitempty"`
	QUIC    string   `json:"quic,omitempty"` // agreed congestion control; loft only
	RX      uint64   `json:"rx"`
	TX      uint64   `json:"tx"`
	Hosts   []string `json:"hosts,omitempty"` // IPv4 pins currently installed
	ISP     string   `json:"isp,omitempty"`
	Opkg    string   `json:"opkg,omitempty"` // NDM name, OpkgTunN
}

// DefaultStateDir is where status JSON is written. LOFT_STATE overrides it.
func DefaultStateDir() string {
	if d := strings.TrimSpace(os.Getenv("LOFT_STATE")); d != "" {
		return d
	}
	return "/opt/var/run/loft"
}

// RunSession is the child: one connection, one TUN, ndmc on the way up
// and down. It returns when ctx is cancelled or the firmware is too old.
func RunSession(ctx context.Context, cfgPath, id, stateDir string) error {
	if stateDir == "" {
		stateDir = DefaultStateDir()
	}
	if err := os.MkdirAll(stateDir, 0o755); err != nil {
		return err
	}
	st := OpenStore(cfgPath)
	f, err := st.Load()
	if err != nil {
		return err
	}
	var c Conn
	found := false
	for _, x := range f.Connections {
		if x.ID == id {
			c = x
			found = true
			break
		}
	}
	if !found {
		return fmt.Errorf("connection %s", id)
	}
	log.SetOutput(redactWriter{w: os.Stderr})
	log.SetFlags(log.Ltime | log.Lmsgprefix)
	log.SetPrefix(c.Name + ": ")

	rt, err := c.Runtime()
	if err != nil {
		_ = writeSnap(snapPath(stateDir, id), Snap{State: "error", Err: err.Error(), Opkg: rt.NDM})
		return err
	}
	// Missing ndmc, or text that does not parse, is not a refusal.
	// TooOld is only a major that was actually read and is below 5.
	if text, err := ndmcText(ctx, Bin{}, "show version"); err == nil && TooOld(text) {
		msg := "KeeneticOS 5.0+ required (OpkgTun)"
		_ = writeSnap(snapPath(stateDir, id), Snap{State: "error", Err: msg, Opkg: rt.NDM})
		return fmt.Errorf("%s", msg)
	}
	inst := filepath.Join(filepath.Dir(cfgPath), "instance")
	_ = os.MkdirAll(inst, 0o700)
	transport.SetInstanceFile(filepath.Join(inst, id))

	lv := &live{
		id: c.ID, name: c.Name, kind: rt.Kind, tun: rt.Tun, ndm: rt.NDM,
		mtu: rt.MTU, hosts: rt.Hosts, isp: strings.TrimSpace(f.Keenetic.ISP),
		rci: f.Keenetic.RCI, path: snapPath(stateDir, id),
		state: "connecting",
	}
	if rt.Loft != nil {
		lv.tunAddr = rt.Loft.Listen.TunAddr
		// Reserve is the configured roster minus the live front, not the
		// peers the session has learned.
		lv.reserve = len(rt.Loft.Outbounds) - 1
		if lv.reserve < 0 {
			lv.reserve = 0
		}
	}
	// write before the ticker starts, and before down is deferred, so the
	// first snap exists and write does not run against a half-torn session.
	lv.write()
	defer lv.down()

	tick := time.NewTicker(2 * time.Second)
	defer tick.Stop()
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case <-tick.C:
				lv.touch()
			}
		}
	}()

	switch rt.Kind {
	case "vless", "trojan":
		return lv.runProxy(ctx, rt)
	default:
		return lv.runLoft(ctx, rt)
	}
}

// live is one child session. mu covers the snap fields and dev.
// bring and publish run with mu held. write does not take the lock;
// its callers already hold it, except the first write in RunSession,
// which happens before any other goroutine exists.
type live struct {
	mu      sync.Mutex
	id      string
	name    string
	kind    string
	tun     string
	ndm     string
	tunAddr string
	mtu     int
	hosts   []string
	pins    []string
	isp     string
	rci     string
	path    string
	state   string
	err     string
	front   string
	node    string
	overlay string
	reserve int
	dev     *inbound.Device
}

// runLoft supervises the loft session. Each time an outbound comes up,
// bring opens the tun on the first call and only re-attaches the dialer
// after that, so OpkgTun does not flap in the priority list.
func (lv *live) runLoft(ctx context.Context, rt Runtime) error {
	h := &transport.Holder{}
	pathCh := transport.MergePath(ctx, transport.WatchPath(ctx), transport.WatchStall(ctx, h, nil))
	return transport.SuperviseOn(ctx, rt.Loft, pathCh, func(d transport.Dialer, o config.Outbound) {
		putDialer(h, d)
		lv.mu.Lock()
		lv.front = o.Type
		lv.node = hostOf(o.URL)
		lv.mu.Unlock()
		log.Printf("%s up", o.Type)
		lv.bring(h)
	})
}

// runProxy probes until the context ends. The wait grows from 1s to 30s.
// After Probe succeeds the dialer stays up until ctx is cancelled.
// A dead process is the parent's problem: Reconcile restarts the child.
func (lv *live) runProxy(ctx context.Context, rt Runtime) error {
	delay := time.Second
	for {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		d, err := probeProxy(ctx, rt)
		if err != nil {
			lv.fail(err)
			log.Printf("%s", Redact(err.Error()))
			if !sleepCtx(ctx, delay) {
				return ctx.Err()
			}
			if delay < 30*time.Second {
				delay *= 2
			}
			continue
		}
		lv.mu.Lock()
		lv.front = rt.Kind
		lv.node = hostOf(rt.Proxy)
		lv.overlay = rt.IP
		lv.mu.Unlock()
		h := &transport.Holder{}
		h.Set(d)
		lv.bring(h)
		<-ctx.Done()
		_ = d.Close()
		return ctx.Err()
	}
}

// probeProxy builds a vless or trojan client and waits for Probe.
// Config.Log is func(string). log.Printf does not have that type; logLine does.
func probeProxy(ctx context.Context, rt Runtime) (transport.Dialer, error) {
	switch rt.Kind {
	case "vless":
		c, err := vless.New(vless.Config{URL: rt.Proxy, Log: logLine})
		if err != nil {
			return nil, err
		}
		if err := c.Probe(ctx); err != nil {
			_ = c.Close()
			return nil, err
		}
		return c, nil
	case "trojan":
		c, err := trojan.New(trojan.Config{URL: rt.Proxy, Log: logLine})
		if err != nil {
			return nil, err
		}
		if err := c.Probe(ctx); err != nil {
			_ = c.Close()
			return nil, err
		}
		return c, nil
	default:
		return nil, fmt.Errorf("kind")
	}
}

// putDialer installs d on the holder. An l3 front that implements IPPump
// is swapped into the existing L3Net so the tun stack survives the
// reconnect. Any other dialer is wrapped once as an L3 net.
func putDialer(h *transport.Holder, d transport.Dialer) {
	if live, ok := h.Live().(*inbound.L3Net); ok {
		if p, ok := d.(transport.IPPump); ok {
			live.Swap(d, p)
			return
		}
		h.Set(d)
		return
	}
	w, err := inbound.WrapL3(d)
	if err != nil {
		log.Printf("l3: %v", err)
		return
	}
	h.Set(w)
}

// bring opens the TUN on the first call and registers it with ndmc.
// Later calls only re-attach the dialer. The kernel gets no default route.
func (lv *live) bring(h *transport.Holder) {
	lv.mu.Lock()
	defer lv.mu.Unlock()
	if lv.dev == nil {
		ip := lv.overlay
		if lv.kind == "loft" {
			ip = transport.OverlayIP()
		}
		if ip == "" {
			lv.state, lv.err = "error", "no overlay address"
			lv.write()
			return
		}
		cidr := ip + "/32"
		if lv.kind == "loft" {
			cidr = inbound.OverlayCIDR(ip, lv.tunAddr)
		}
		dev, err := inbound.StartRoutedTUN(lv.tun, cidr, h)
		if err != nil {
			lv.state, lv.err = "error", err.Error()
			log.Printf("tun: %v", err)
			lv.write()
			return
		}
		lv.dev = dev
		lv.overlay = strings.Split(cidr, "/")[0]
	}
	lv.dev.Attach(h)
	lv.publish()
}

// publish asks ndmc to create or update OpkgTunN. Caller holds mu.
// Front hostnames are resolved here and pinned on the ISP interface
// before "interface up", so the session's own packets leave via the ISP
// and do not hairpin into the tun. An ndmc error leaves the tun up and
// is written on the snap; the next callback tries again.
func (lv *live) publish() {
	if lv.isp == "" {
		isp, err := DiscoverISP(context.Background(), lv.rci, Bin{})
		if err != nil {
			log.Printf("isp: %v", err)
		}
		lv.isp = isp
	}
	pins := resolve4(context.Background(), lv.hosts)
	lv.pins = pins
	if lv.isp == "" && len(pins) > 0 {
		log.Printf("no ISP interface; front addresses are not pinned")
	}
	lines, err := UpLines(Up{
		Index: indexOf(lv.tun), Desc: lv.name, IP: lv.overlay,
		MTU: lv.mtu, ISP: lv.isp, Hosts: pins,
	})
	if err != nil {
		lv.state, lv.err = "error", err.Error()
		lv.write()
		return
	}
	if err := Apply(context.Background(), Bin{}, lines); err != nil {
		lv.state, lv.err = "error", err.Error()
		log.Printf("%v", err)
		lv.write()
		return
	}
	lv.state, lv.err = "up", ""
	log.Printf("ndmc %s up", lv.ndm)
	lv.write()
}

// down closes the tun and runs DownLines. The NDM object stays, so a
// priority that names this OpkgTun is kept. DeleteLines is the parent's
// job, and only when the connection itself is removed.
func (lv *live) down() {
	lv.mu.Lock()
	defer lv.mu.Unlock()
	if lv.dev != nil {
		_ = lv.dev.Close()
		lv.dev = nil
	}
	lines, err := DownLines(Up{Index: indexOf(lv.tun), ISP: lv.isp, Hosts: lv.pins})
	if err == nil {
		_ = Apply(context.Background(), Bin{}, lines)
		log.Printf("ndmc %s down", lv.ndm)
	}
	lv.state = "off"
	lv.write()
}

// fail records a redacted error on the snap. The tun, if any, stays.
func (lv *live) fail(err error) {
	lv.mu.Lock()
	defer lv.mu.Unlock()
	lv.state = "error"
	if err != nil {
		lv.err = Redact(err.Error())
	}
	lv.write()
}

// touch refreshes byte counters and the agreed QUIC mode on the snap.
func (lv *live) touch() {
	lv.mu.Lock()
	defer lv.mu.Unlock()
	lv.write()
}

// write stores the snap. QUIC is reported only for loft: CCInUse is the
// mode the session agreed, and the fixed rates stay in the YAML because
// the counter does not carry them. Bytes come from sysfs for this opkgtun.
func (lv *live) write() {
	rx, tx := tunBytes(lv.tun)
	q := ""
	if lv.kind == "loft" {
		q = wire.CCName(transport.CCInUse())
	}
	gw := ""
	if lv.kind == "loft" {
		gw = transport.OverlayGateway()
	}
	_ = writeSnap(lv.path, Snap{
		State: lv.state, Err: lv.err, Overlay: lv.overlay, Gateway: gw,
		MTU: lv.mtu, Front: lv.front, Node: lv.node, Reserve: lv.reserve,
		QUIC: q, RX: rx, TX: tx, Hosts: append([]string(nil), lv.pins...),
		ISP: lv.isp, Opkg: lv.ndm,
	})
}

// writeSnap replaces the status file via a sibling temp and rename.
func writeSnap(path string, s Snap) error {
	if path == "" {
		return fmt.Errorf("id")
	}
	b, err := json.Marshal(s)
	if err != nil {
		return err
	}
	tmp := path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o644); err != nil {
		return err
	}
	return os.Rename(tmp, path)
}

// readSnap returns a zero Snap when the file is missing or not JSON.
func readSnap(path string) Snap {
	if path == "" {
		return Snap{}
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return Snap{}
	}
	var s Snap
	if err := json.Unmarshal(b, &s); err != nil {
		return Snap{}
	}
	return s
}

// snapPath joins dir and id.json. id must match idRe, and the result
// must stay inside dir, so a crafted id cannot write outside the state directory.
func snapPath(dir, id string) string {
	if !idRe.MatchString(id) {
		return ""
	}
	p := filepath.Join(dir, id+".json")
	if filepath.Dir(p) != filepath.Clean(dir) {
		return ""
	}
	return p
}

// indexOf parses N from opkgtunN. A name that is not that form is 0.
func indexOf(tun string) int {
	n, err := strconv.Atoi(strings.TrimPrefix(tun, "opkgtun"))
	if err != nil {
		return 0
	}
	return n
}

// hostOf is the hostname of a front URL, shown as the node name.
func hostOf(raw string) string {
	h := hostsOfURL(raw)
	if len(h) == 0 {
		return ""
	}
	return h[0]
}

// resolve4 turns hostnames into IPv4 addresses. Literals pass through.
// Each lookup waits at most 2s and uses the main table: no default route
// has been installed yet. A failure is skipped. An empty result still
// brings the interface up; the pins are simply absent.
func resolve4(ctx context.Context, hosts []string) []string {
	var out []string
	seen := map[string]bool{}
	add := func(s string) {
		if s == "" || seen[s] {
			return
		}
		seen[s] = true
		out = append(out, s)
	}
	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			if v4 := ip.To4(); v4 != nil {
				add(v4.String())
			}
			continue
		}
		c, cancel := context.WithTimeout(ctx, 2*time.Second)
		ips, err := net.DefaultResolver.LookupIP(c, "ip4", h)
		cancel()
		if err != nil {
			continue
		}
		for _, ip := range ips {
			if v4 := ip.To4(); v4 != nil {
				add(v4.String())
			}
		}
	}
	return out
}

// tunBytes reads rx and tx from sysfs. A name that is not opkgtun plus
// digits returns zeros, so a crafted name cannot read another interface.
func tunBytes(name string) (rx, tx uint64) {
	if !tunOK(name) {
		return 0, 0
	}
	return readUint("/sys/class/net/" + name + "/statistics/rx_bytes"),
		readUint("/sys/class/net/" + name + "/statistics/tx_bytes")
}

// tunOK is true when name is "opkgtun" and a decimal index.
func tunOK(name string) bool {
	if !strings.HasPrefix(name, "opkgtun") {
		return false
	}
	_, err := strconv.Atoi(strings.TrimPrefix(name, "opkgtun"))
	return err == nil
}

// readUint reads one sysfs counter. A missing file is zero.
func readUint(path string) uint64 {
	b, err := os.ReadFile(path)
	if err != nil {
		return 0
	}
	n, _ := strconv.ParseUint(strings.TrimSpace(string(b)), 10, 64)
	return n
}

// logLine is the vless and trojan Config.Log hook.
func logLine(s string) { log.Print(s) }

// sleepCtx waits for d or for ctx. False means the caller should return.
func sleepCtx(ctx context.Context, d time.Duration) bool {
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return false
	case <-t.C:
		return true
	}
}

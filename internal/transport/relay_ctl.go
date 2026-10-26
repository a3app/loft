package transport

// Relay ctl:1 enrol and heartbeat; signed slice applies drain and decoy.
//
// ctl:1 is the control channel between a relay and its controller, carried
// as one stream on the trunk. The relay enrols (sends its id, public URL,
// decoy and fronts), the controller answers with a signed slice (this
// relay's config: drain, decoy, heartbeat interval), and then the relay
// sends a heartbeat (beat) with its connection count and uptime. A relay that
// stops beating is dropped from the roster, so clients stop using it.

import (
	"bytes"
	"context"
	"encoding/hex"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/a3app/loft/internal/config"
	"github.com/a3app/loft/internal/mux"
	"github.com/a3app/loft/internal/version"
	"github.com/a3app/loft/internal/wire"
)

// relayRuntime is live relay state: conn count, drain, and the decoy listener.
// Drain means "accept no new member connections": the controller sets it to
// take a relay out of service without cutting the connections it carries.
type relayRuntime struct {
	started time.Time
	conns   atomic.Int32
	drain   atomic.Bool
	decoy   *decoyListener
	// ctlUp is true while ctl:1 to the controller is enrolled; downSince
	// (unix nanoseconds) is when it last went down, or the start.
	ctlUp     atomic.Bool
	downSince atomic.Int64
}

// setCtlUp records whether this relay reaches the controller now.
func (rt *relayRuntime) setCtlUp(up bool) {
	if !up && rt.ctlUp.Load() {
		rt.downSince.Store(time.Now().UnixNano())
	}
	rt.ctlUp.Store(up)
}

// downFor is how long the controller has been unreachable (0 while it is).
func (rt *relayRuntime) downFor() time.Duration {
	if rt.ctlUp.Load() {
		return 0
	}
	since := rt.downSince.Load()
	if since == 0 {
		since = rt.started.UnixNano()
	}
	return time.Since(time.Unix(0, since))
}

// apply sets drain and, when present, replaces the decoy origin from the slice.
func (rt *relayRuntime) apply(sl wire.Slice) {
	if sl.Drain {
		rt.drain.Store(true)
	} else {
		rt.drain.Store(false)
	}
	if sl.Decoy != "" && rt.decoy != nil {
		rt.decoy.SetOrigins([]string{sl.Decoy})
	}
}

// runRelayCtl reconnects ctl:1 until ctx is done.
func runRelayCtl(ctx context.Context, h *Holder, cfg *config.ServerConfig, rt *relayRuntime, pin *relayPin, standby string) {
	id := cfg.ID
	if id == "" {
		id = "relay"
	}
	url := cfg.Advertise
	if url == "" {
		url = "https://" + strings.TrimPrefix(cfg.Listen, ":")
	}
	decoy := ""
	if len(cfg.Decoy) > 0 {
		decoy = cfg.Decoy[0]
	}
	req := wire.EnrolReq{ID: id, URL: url, Decoy: decoy, Fronts: cfg.EnabledFronts()}
	beatMS := uint32(defaultBeatMS)

	for {
		select {
		case <-ctx.Done():
			return
		default:
		}
		sess := waitSession(h, 5*time.Second)
		if sess == nil {
			time.Sleep(time.Second)
			continue
		}
		if err := relayCtlSession(ctx, sess, req, rt, &beatMS, pin, standby); err != nil {
			log.Printf("loft-relay: ctl %v", err)
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(2 * time.Second):
		}
	}
}

// relayCtlSession sends enrol_req, applies enrol_ok/slice, then beats.
// The slice must be signed by the pinned controller key: the one the relay
// was installed with, or the key a KeyRoll from it handed over to. A valid
// signature by any other key is refused.
// It returns when the stream fails or ctx ends; runRelayCtl then enrols
// again, so a controller restart is picked up without restarting the relay.
func relayCtlSession(ctx context.Context, sess *mux.Session, req wire.EnrolReq, rt *relayRuntime, beatMS *uint32, pin *relayPin, standby string) error {
	st, err := sess.Open(net.JoinHostPort(wire.CtlHost, wire.CtlPort))
	if err != nil {
		return err
	}
	defer st.Close()
	if err := writeMsg(st, wire.EncodeFlt(wire.FltEnrolReq, req.Encode())); err != nil {
		return err
	}
	raw, err := readMsg(st)
	if err != nil {
		return err
	}
	kind, body, err := wire.DecodeFlt(raw)
	if err != nil {
		return err
	}
	if kind == wire.FltKeyRoll {
		pin.roll(body)
		if raw, err = readMsg(st); err != nil {
			return err
		}
		if kind, body, err = wire.DecodeFlt(raw); err != nil {
			return err
		}
	}
	if kind == wire.FltEnrolNo {
		_, reason := wire.DecodeEnrolNo(body)
		return errEnrol(reason)
	}
	if kind != wire.FltEnrolOK && kind != wire.FltSlice {
		return errEnrol("unexpected")
	}
	sl, err := wire.DecodeSlice(body)
	if err != nil {
		return err
	}
	if err := sl.Verify(); err != nil {
		return err
	}
	if !bytes.Equal(sl.Pub, pin.key()) {
		return errEnrol("slice not signed by controller_pub")
	}
	rt.apply(sl)
	if sl.BeatMS > 0 {
		*beatMS = sl.BeatMS
	}
	rt.setCtlUp(true)
	defer rt.setCtlUp(false)
	// A standby controller keeps the state the controller sends after
	// enrol_ok; the stream is read here while the loop below writes beats.
	if standby != "" {
		go readStandbyState(st, standby)
	}
	t := time.NewTicker(time.Duration(*beatMS) * time.Millisecond)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-t.C:
			b := wire.Beat{
				Load:   0,
				Conns:  uint32(rt.conns.Load()),
				Prog:   version.Line("loft-node"),
				Uptime: uint32(time.Since(rt.started).Seconds()),
			}
			if err := writeMsg(st, wire.EncodeFlt(wire.FltBeat, b.Encode())); err != nil {
				return err
			}
		}
	}
}

// readStandbyState stores each state message under dir until the stream
// ends. Each file is replaced whole, then "received" records when.
func readStandbyState(st io.Reader, dir string) {
	for {
		raw, err := readMsg(st)
		if err != nil {
			return
		}
		kind, body, err := wire.DecodeFlt(raw)
		if err != nil || kind != wire.FltState {
			continue
		}
		files, err := wire.DecodeState(body)
		if err != nil {
			continue
		}
		if err := saveStandby(dir, files); err != nil {
			log.Printf("loft-relay: standby state: %v", err)
		}
	}
}

// saveStandby writes the controller's files into dir (0700, files 0600).
// Names are plain file names: anything with a path separator is dropped.
func saveStandby(dir string, files map[string][]byte) error {
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	for name, b := range files {
		if name != filepath.Base(name) || strings.HasPrefix(name, ".") {
			continue
		}
		tmp := filepath.Join(dir, name+".new")
		if err := os.WriteFile(tmp, b, 0o600); err != nil {
			return err
		}
		if err := os.Rename(tmp, filepath.Join(dir, name)); err != nil {
			return err
		}
	}
	return os.WriteFile(filepath.Join(dir, "received"), []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o600)
}

// relayPin is the controller key a relay trusts. base is controller_pub
// from node.conf; cur moves on when a KeyRoll signed by cur arrives, and is
// kept in path ("base cur" in hex) so it survives a restart while node.conf
// still names base.
type relayPin struct {
	mu   sync.Mutex
	base []byte
	cur  []byte
	ser  uint64
	path string
}

func (p *relayPin) key() []byte {
	p.mu.Lock()
	defer p.mu.Unlock()
	return p.cur
}

// load takes the saved key when it was rolled from the configured one.
func (p *relayPin) load() {
	b, err := os.ReadFile(p.path)
	if err != nil {
		return
	}
	f := strings.Fields(string(b))
	if len(f) != 2 || f[0] != hex.EncodeToString(p.base) {
		return
	}
	if cur, err := hex.DecodeString(f[1]); err == nil && len(cur) == 32 {
		p.cur = cur
	}
}

// roll applies a KeyRoll signed by the current key and saves the result.
func (p *relayPin) roll(raw []byte) {
	k, err := wire.DecodeKeyRoll(raw)
	if err != nil || k.Verify(time.Now()) != nil {
		return
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	if !bytes.Equal(k.Old, p.cur) || k.Serial <= p.ser {
		return
	}
	p.cur, p.ser = append([]byte(nil), k.New...), k.Serial
	if p.path != "" {
		_ = os.WriteFile(p.path, []byte(hex.EncodeToString(p.base)+" "+hex.EncodeToString(p.cur)+"\n"), 0o600)
	}
	log.Printf("loft-relay: controller key changed to %x…", p.cur[:4])
}

// enrolError is a ctl enrol_no reason.
type enrolError string

// Error prefixes the enrol_no reason.
func (e enrolError) Error() string { return "enrol: " + string(e) }

// errEnrol wraps a controller enrol_no reason.
func errEnrol(s string) error { return enrolError(s) }

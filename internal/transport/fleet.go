package transport

// Controller fleet: enrol and beat on ctl:1, silent-evict, signed roster and slices.

import (
	"crypto/ed25519"
	"io"
	"sync"
	"time"

	"github.com/a3app/loft/internal/config"
	ecrypto "github.com/a3app/loft/internal/crypto"
	"github.com/a3app/loft/internal/mux"
	"github.com/a3app/loft/internal/wire"
)

// defaultBeatMS is the ctl heartbeat interval; evictAfter missed beats drop the relay (floor 15s).
const (
	defaultBeatMS = 10000
	evictAfter    = 3
)

// Fleet is the controller's live set of relays. Roster bytes are re-signed
// on each change so Status always carries current fronts.
type Fleet struct {
	priv   ed25519.PrivateKey
	cfg    *config.ServerConfig
	mu     sync.Mutex
	live   map[string]*liveRelay
	serial uint64
	gens   uint64
	cached []byte
}

// liveRelay is one enrolled edge: last beat, load, and ctl closer.
type liveRelay struct {
	info     wire.RelayInfo
	beatMS   uint32
	last     time.Time
	load     byte
	conns    uint32
	gen      uint64
	ctlClose func()
}

// NewFleet starts an empty live set, signs the initial roster, and watches beats.
func NewFleet(priv ed25519.PrivateKey, cfg *config.ServerConfig) *Fleet {
	f := &Fleet{
		priv:   priv,
		cfg:    cfg,
		live:   map[string]*liveRelay{},
		serial: uint64(time.Now().Unix()),
	}
	f.resign()
	go f.watch()
	return f
}

// RosterBytes returns the current signed roster (advertise plus live relays).
func (f *Fleet) RosterBytes() []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]byte(nil), f.cached...)
}

// watch evicts silent relays once per second.
func (f *Fleet) watch() {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	for range t.C {
		f.evictSilent()
	}
}

// evictSilent drops relays that missed evictAfter beats (floor 15s) and resigns.
func (f *Fleet) evictSilent() {
	f.mu.Lock()
	defer f.mu.Unlock()
	now := time.Now()
	changed := false
	for id, r := range f.live {
		lim := time.Duration(r.beatMS) * evictAfter * time.Millisecond
		if lim < 15*time.Second {
			lim = 15 * time.Second
		}
		if now.Sub(r.last) > lim {
			if r.ctlClose != nil {
				r.ctlClose()
			}
			delete(f.live, id)
			changed = true
		}
	}
	if changed {
		f.serial++
		f.resign()
	}
}

// allowed is enrol:open or an id listed in relays: that is not drain.
func (f *Fleet) allowed(id string) bool {
	if f.cfg == nil {
		return false
	}
	if f.cfg.Enrol == "open" {
		return true
	}
	if id == "" {
		return false
	}
	for _, e := range f.cfg.Relays {
		if e.ID == id && !e.Drain {
			return true
		}
	}
	return false
}

// yamlEntry returns the controller relays: row for id, if any.
func (f *Fleet) yamlEntry(id string) (config.RelayEntry, bool) {
	if f.cfg == nil {
		return config.RelayEntry{}, false
	}
	for _, e := range f.cfg.Relays {
		if e.ID == id {
			return e, true
		}
	}
	return config.RelayEntry{}, false
}

// ServeCtl is ctl:1: enrol_req, enrol_ok/enrol_no, then beats until the stream dies.
func (f *Fleet) ServeCtl(st *mux.Stream) {
	defer st.Close()
	raw, err := readMsg(st)
	if err != nil {
		return
	}
	kind, body, err := wire.DecodeFlt(raw)
	if err != nil || kind != wire.FltEnrolReq {
		_ = writeMsg(st, wire.EncodeFlt(wire.FltEnrolNo, wire.EncodeEnrolNo(wire.EnrolForm, "want enrol")))
		return
	}
	req, err := wire.DecodeEnrolReq(body)
	if err != nil {
		_ = writeMsg(st, wire.EncodeFlt(wire.FltEnrolNo, wire.EncodeEnrolNo(wire.EnrolForm, err.Error())))
		return
	}
	if !f.allowed(req.ID) {
		_ = writeMsg(st, wire.EncodeFlt(wire.FltEnrolNo, wire.EncodeEnrolNo(wire.EnrolUnknown, "id")))
		return
	}
	sl, gen := f.admit(req, func() { _ = st.Close() })
	if err := writeMsg(st, wire.EncodeFlt(wire.FltEnrolOK, sl.Encode())); err != nil {
		f.dropIf(req.ID, gen)
		return
	}
	beatTO := time.Duration(sl.BeatMS) * time.Millisecond
	if beatTO < time.Second {
		beatTO = 10 * time.Second
	}
	for {
		_ = st.SetReadDeadline(time.Now().Add(beatTO * evictAfter))
		raw, err := readMsg(st)
		if err != nil {
			f.dropIf(req.ID, gen)
			return
		}
		kind, body, err := wire.DecodeFlt(raw)
		if err != nil {
			continue
		}
		switch kind {
		case wire.FltBeat:
			b, err := wire.DecodeBeat(body)
			if err != nil {
				continue
			}
			f.touch(req.ID, b)
		case wire.FltEnrolReq:
			// ignore
		default:
			continue
		}
	}
}

// admit replaces any previous session for id, YAML-overrides URL/decoy/fronts, and signs a slice.
func (f *Fleet) admit(req wire.EnrolReq, closer func()) (wire.Slice, uint64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if old, ok := f.live[req.ID]; ok && old.ctlClose != nil {
		old.ctlClose()
	}
	info := wire.RelayInfo{ID: req.ID, URL: req.URL, Decoy: req.Decoy, Fronts: req.Fronts, Weight: 100}
	if e, ok := f.yamlEntry(req.ID); ok {
		if e.URL != "" {
			info.URL = e.URL
		}
		if e.Decoy != "" {
			info.Decoy = e.Decoy
		}
		if len(e.Fronts) > 0 {
			info.Fronts = append([]string(nil), e.Fronts...)
		}
		info.Drain = e.Drain
		if e.Weight > 0 {
			info.Weight = e.Weight
		}
		if e.Path != "" {
			info.Path = e.Path
		}
		if e.MaskPub != "" {
			info.MaskPub = parseHexPub(e.MaskPub)
		}
	}
	f.gens++
	gen := f.gens
	f.live[req.ID] = &liveRelay{
		info:     info,
		beatMS:   defaultBeatMS,
		last:     time.Now(),
		gen:      gen,
		ctlClose: closer,
	}
	f.serial++
	f.resign()
	return f.sliceLocked(info), gen
}

// sliceLocked signs a 24h slice for one relay (caller holds f.mu).
func (f *Fleet) sliceLocked(info wire.RelayInfo) wire.Slice {
	sl := wire.Slice{
		Serial:  f.serial,
		ID:      info.ID,
		URL:     info.URL,
		Decoy:   info.Decoy,
		Fronts:  append([]string(nil), info.Fronts...),
		Drain:   info.Drain,
		Weight:  info.Weight,
		BeatMS:  defaultBeatMS,
		Expires: time.Now().Add(24 * time.Hour).Unix(),
	}
	return ecrypto.SignSlice(f.priv, sl)
}

// touch records a heartbeat's load and conn count.
func (f *Fleet) touch(id string, b wire.Beat) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.live[id]
	if !ok {
		return
	}
	r.last = time.Now()
	r.load = b.Load
	r.conns = b.Conns
}

// drop removes id from the live set regardless of generation.
func (f *Fleet) drop(id string) { f.dropIf(id, 0) }

// dropIf removes id unless gen is set and does not match the live session.
func (f *Fleet) dropIf(id string, gen uint64) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.live[id]
	if !ok {
		return
	}
	if gen != 0 && r.gen != gen {
		return
	}
	delete(f.live, id)
	f.serial++
	f.resign()
}

// SetDrain marks a live relay drain (omitted from the signed roster) and resigns.
func (f *Fleet) SetDrain(id string, drain bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	r, ok := f.live[id]
	if !ok {
		return
	}
	r.info.Drain = drain
	f.serial++
	f.resign()
}

// resign rebuilds cached roster bytes: advertise as id self, plus non-drain live relays.
func (f *Fleet) resign() {
	r := wire.Roster{Serial: f.serial}
	if f.cfg != nil && f.cfg.Advertise != "" {
		self := wire.RelayInfo{ID: "self", URL: f.cfg.Advertise, Fronts: f.cfg.EnabledFronts(), Weight: 100, Path: f.cfg.WSPath}
		if len(f.cfg.Decoy) > 0 {
			self.Decoy = f.cfg.Decoy[0]
		}
		if f.cfg.Mask {
			self.MaskPub = NodeMaskPub([]byte(f.cfg.Token))
		}
		r.Relays = append(r.Relays, self)
	}
	for _, e := range f.live {
		if e.info.Drain {
			continue
		}
		r.Relays = append(r.Relays, e.info)
	}
	if f.priv != nil {
		r = ecrypto.SignRoster(f.priv, r)
		f.cached = r.Encode()
	} else {
		f.cached = nil
	}
}

// parseHexPub decodes a hex mask public key; odd trailing nibble is dropped.
func parseHexPub(s string) []byte {
	var out []byte
	for i := 0; i+1 < len(s); i += 2 {
		var v byte
		for _, c := range []byte{s[i], s[i+1]} {
			v <<= 4
			switch {
			case c >= '0' && c <= '9':
				v |= c - '0'
			case c >= 'a' && c <= 'f':
				v |= c - 'a' + 10
			case c >= 'A' && c <= 'F':
				v |= c - 'A' + 10
			}
		}
		out = append(out, v)
	}
	return out
}

// PushSlice writes a signed FltSlice for id onto w (ctl downlink).
func (f *Fleet) PushSlice(id string, w io.Writer) error {
	f.mu.Lock()
	r, ok := f.live[id]
	if !ok {
		f.mu.Unlock()
		return io.ErrClosedPipe
	}
	sl := f.sliceLocked(r.info)
	f.mu.Unlock()
	return writeMsg(w, wire.EncodeFlt(wire.FltSlice, sl.Encode()))
}

// SetRelays replaces the allowlist (on reload) and re-signs the roster;
// a relay no longer listed is dropped.
func (f *Fleet) SetRelays(rel []config.RelayEntry, enrol string) {
	f.mu.Lock()
	cfg := *f.cfg
	cfg.Relays, cfg.Enrol = rel, enrol
	f.cfg = &cfg
	var gone []string
	for id := range f.live {
		if !f.allowed(id) {
			gone = append(gone, id)
		}
	}
	f.mu.Unlock()
	for _, id := range gone {
		f.drop(id)
	}
	f.mu.Lock()
	f.serial++
	f.resign()
	f.mu.Unlock()
}

// LiveRelay is one connected relay, for the status file.
type LiveRelay struct {
	ID    string    `json:"id"`
	URL   string    `json:"url,omitempty"`
	Load  byte      `json:"load"`
	Conns uint32    `json:"conns"`
	Beat  time.Time `json:"beat"`
}

// Live lists the connected relays.
func (f *Fleet) Live() []LiveRelay {
	f.mu.Lock()
	defer f.mu.Unlock()
	out := []LiveRelay{}
	for id, r := range f.live {
		out = append(out, LiveRelay{ID: id, URL: r.info.URL, Load: r.load, Conns: r.conns, Beat: r.last})
	}
	return out
}

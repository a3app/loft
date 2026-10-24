package transport

// Mux hold: keep streams across a brief front drop (metro, LTE, LEO handover).
// Cap hold + Status gap_hold_ms. Leave still tears the session.

import (
	"sync"
	"time"

	"github.com/a3app/loft/internal/mux"
	"github.com/a3app/loft/internal/netconf"
	"github.com/a3app/loft/internal/wire"
)

type parkedMux struct {
	sess  *mux.Session
	until time.Time
}

var (
	serverPark   sync.Map // member_id NUL instance_id -> parkedMux
	clientHoldMu sync.Mutex
	clientHold   struct {
		sess  *mux.Session
		sid   string
		key   []byte
		seq   uint64
		gen   uint64
		until time.Time
	}
	visGapMS  uint32
	visCaps   wire.Cap
	gapHoldFn = defaultGapHold
)

func defaultGapHold() time.Duration {
	if visGapMS > 0 {
		return time.Duration(visGapMS) * time.Millisecond
	}
	if o := NetSwitch(); o != nil && o.N() != nil {
		return o.N().Session.Gap()
	}
	return netconf.DefaultSession().Gap()
}

func gapHoldDuration() time.Duration { return gapHoldFn() }

func rememberHold(st wire.Status) {
	if st.GapHoldMS > 0 {
		visGapMS = st.GapHoldMS
	}
	visCaps = st.Caps
}

func clientHolds() bool {
	return visCaps == 0 || visCaps.Has(wire.CapHold)
}

// clientHoldMux is the mux that SOCKS/TUN streams sit on. Reused across
// Park until gap_hold expires, then a new mux (inner TCP RSTs).
func clientHoldMux() *mux.Session {
	s, _ := takeClientHold()
	return s
}

// takeClientHold hands out the parked mux, or a new one. Each handout bumps
// gen. Close of an older front must not Session.Close the mux a newer front
// already owns: Holder.Set closes the previous dialer after the next Start.
func takeClientHold() (*mux.Session, uint64) {
	clientHoldMu.Lock()
	defer clientHoldMu.Unlock()
	now := time.Now()
	clientHold.gen++
	if clientHold.sess != nil && !clientHold.sess.Closed() && now.Before(clientHold.until) {
		return clientHold.sess, clientHold.gen
	}
	if clientHold.sess != nil {
		clientHold.sess.Close()
	}
	clientHold.sess = mux.NewClient()
	clientHold.sess.SetHold(true)
	clientHold.sid, clientHold.key, clientHold.seq = "", nil, 0
	clientHold.until = now.Add(24 * time.Hour)
	return clientHold.sess, clientHold.gen
}

// holdOwner is false after a newer front's Start has taken the hold.
// That front's dialer is already on this mux. Leave from the old Close
// would drop the server session under the live streams.
func holdOwner(gen uint64) bool {
	clientHoldMu.Lock()
	defer clientHoldMu.Unlock()
	return gen != 0 && clientHold.gen == gen
}

// releaseClientHold closes the parked mux only when gen is still the owner.
// False means a newer front has taken the hold.
func releaseClientHold(gen uint64) bool {
	clientHoldMu.Lock()
	defer clientHoldMu.Unlock()
	if gen == 0 || clientHold.gen != gen {
		return false
	}
	if clientHold.sess != nil {
		clientHold.sess.Close()
		clientHold.sess = nil
	}
	clientHold.sid, clientHold.key, clientHold.seq = "", nil, 0
	return true
}

func parkClient(sess *mux.Session, sid string, key []byte, seq uint64) {
	if sess == nil {
		return
	}
	clientHoldMu.Lock()
	defer clientHoldMu.Unlock()
	clientHold.sess = sess
	clientHold.sid = sid
	clientHold.key = append([]byte(nil), key...)
	clientHold.seq = seq
	clientHold.until = time.Now().Add(gapHoldDuration())
}

func clientHoldGET() (sid string, key []byte, seq uint64) {
	clientHoldMu.Lock()
	defer clientHoldMu.Unlock()
	if clientHold.sess == nil || clientHold.sess.Closed() || time.Now().After(clientHold.until) {
		return "", nil, 0
	}
	return clientHold.sid, append([]byte(nil), clientHold.key...), clientHold.seq
}

// forgetGETSid drops a parked GET HTTP sid the origin already 404'd.
// Mux stays until Close; the next Start must 0x02 instead of replaying the sid.
func forgetGETSid() {
	clientHoldMu.Lock()
	defer clientHoldMu.Unlock()
	clientHold.sid, clientHold.key, clientHold.seq = "", nil, 0
}

func clearClientHold() {
	clientHoldMu.Lock()
	defer clientHoldMu.Unlock()
	if clientHold.sess != nil {
		clientHold.sess.Close()
		clientHold.sess = nil
	}
	clientHold.sid, clientHold.key, clientHold.seq = "", nil, 0
}

// parkOwner is the member id from the authenticated token. Empty ring (shared
// token) is one park namespace. Hello.MemberID is not used.
func parkOwner(token []byte) string {
	if AuthKeys == nil || AuthKeys.Len() == 0 {
		return ""
	}
	return AuthKeys.IDFor(token)
}

func parkKey(owner, inst string) string {
	if inst == "" {
		return ""
	}
	return owner + "\x00" + inst
}

// adoptServerMux reuses a parked mux for the same member+instance, or starts a new one.
func adoptServerMux(rest, token []byte) (ms *mux.Session, reused bool) {
	h, err := wire.DecodeHello(rest)
	inst := ""
	if err == nil {
		inst = h.InstanceID
	}
	owner := parkOwner(token)
	if inst != "" && err == nil && h.Caps.Has(wire.CapHold) {
		if p := takeParked(owner, inst); p != nil {
			attachHello(p, rest)
			p.SetOwner(owner)
			return p, true
		}
	}
	ms = mux.NewServer()
	attachHello(ms, rest)
	ms.SetOwner(owner)
	return ms, false
}

func helloInstance(rest []byte) string {
	h, err := wire.DecodeHello(rest)
	if err != nil {
		return ""
	}
	return h.InstanceID
}

func takeParked(owner, inst string) *mux.Session {
	k := parkKey(owner, inst)
	if k == "" {
		return nil
	}
	v, ok := serverPark.Load(k)
	if !ok {
		return nil
	}
	p := v.(parkedMux)
	serverPark.Delete(k)
	if p.sess == nil || p.sess.Closed() || time.Now().After(p.until) {
		if p.sess != nil {
			p.sess.Close()
		}
		return nil
	}
	return p.sess
}

func parkServer(ms *mux.Session) {
	if ms == nil || ms.Closed() {
		return
	}
	inst := ms.Instance()
	if inst == "" || !ms.Holding() {
		ms.Close()
		return
	}
	k := parkKey(ms.Owner(), inst)
	d := gapHoldDuration()
	serverPark.Store(k, parkedMux{sess: ms, until: time.Now().Add(d)})
	time.AfterFunc(d, func() {
		v, ok := serverPark.Load(k)
		if !ok {
			return
		}
		p := v.(parkedMux)
		if p.sess != ms {
			return
		}
		serverPark.Delete(k)
		if !p.sess.Closed() {
			p.sess.Close()
		}
	})
}

func dropServerMux(ms *mux.Session) {
	if ms == nil {
		return
	}
	if ms.Holding() {
		parkServer(ms)
		return
	}
	ms.Close()
}

type parker interface {
	Park() error
}

func parkOrClose(d Dialer) error {
	if d == nil {
		return nil
	}
	if s := d.Session(); s == nil || s.Closed() {
		return d.Close()
	}
	if p, ok := d.(parker); ok && clientHolds() {
		return p.Park()
	}
	return d.Close()
}

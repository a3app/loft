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
	serverPark   sync.Map // instance_id -> parkedMux
	clientHoldMu sync.Mutex
	clientHold   struct {
		sess  *mux.Session
		sid   string
		key   []byte
		seq   uint64
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
	if NetSwitch != nil && NetSwitch.Net != nil {
		return NetSwitch.Net.Session.Gap()
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
	clientHoldMu.Lock()
	defer clientHoldMu.Unlock()
	now := time.Now()
	if clientHold.sess != nil && !clientHold.sess.Closed() && now.Before(clientHold.until) {
		return clientHold.sess
	}
	if clientHold.sess != nil {
		clientHold.sess.Close()
	}
	clientHold.sess = mux.NewClient()
	clientHold.sess.SetHold(true)
	clientHold.sid, clientHold.key, clientHold.seq = "", nil, 0
	clientHold.until = now.Add(24 * time.Hour)
	return clientHold.sess
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

func clearClientHold() {
	clientHoldMu.Lock()
	defer clientHoldMu.Unlock()
	if clientHold.sess != nil {
		clientHold.sess.Close()
		clientHold.sess = nil
	}
	clientHold.sid, clientHold.key, clientHold.seq = "", nil, 0
}

// adoptServerMux reuses a parked mux for the same instance, or starts a new one.
func adoptServerMux(rest []byte) (ms *mux.Session, reused bool) {
	h, err := wire.DecodeHello(rest)
	inst := ""
	if err == nil {
		inst = h.InstanceID
		if inst != "" && h.Caps.Has(wire.CapHold) {
			if p := takeParked(inst); p != nil {
				attachHello(p, rest)
				return p, true
			}
		}
	}
	ms = mux.NewServer()
	attachHello(ms, rest)
	return ms, false
}

func takeParked(inst string) *mux.Session {
	if inst == "" {
		return nil
	}
	v, ok := serverPark.Load(inst)
	if !ok {
		return nil
	}
	p := v.(parkedMux)
	serverPark.Delete(inst)
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
	d := gapHoldDuration()
	serverPark.Store(inst, parkedMux{sess: ms, until: time.Now().Add(d)})
	time.AfterFunc(d, func() {
		v, ok := serverPark.Load(inst)
		if !ok {
			return
		}
		p := v.(parkedMux)
		if p.sess != ms {
			return
		}
		serverPark.Delete(inst)
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
	if p, ok := d.(parker); ok && clientHolds() {
		return p.Park()
	}
	return d.Close()
}

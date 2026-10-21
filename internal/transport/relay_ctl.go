package transport

// Relay ctl:1 enrol and heartbeat; signed slice applies drain and decoy.

import (
	"context"
	"log"
	"net"
	"strings"
	"sync/atomic"
	"time"

	"github.com/a3app/loft/internal/config"
	"github.com/a3app/loft/internal/mux"
	"github.com/a3app/loft/internal/version"
	"github.com/a3app/loft/internal/wire"
)

// relayRuntime is live relay state: conn count, drain, and the decoy listener.
type relayRuntime struct {
	started time.Time
	conns   atomic.Int32
	drain   atomic.Bool
	decoy   *decoyListener
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
func runRelayCtl(ctx context.Context, h *Holder, cfg *config.ServerConfig, rt *relayRuntime) {
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
		if err := relayCtlSession(ctx, sess, req, rt, &beatMS); err != nil {
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
func relayCtlSession(ctx context.Context, sess *mux.Session, req wire.EnrolReq, rt *relayRuntime, beatMS *uint32) error {
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
	rt.apply(sl)
	if sl.BeatMS > 0 {
		*beatMS = sl.BeatMS
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

// enrolError is a ctl enrol_no reason.
type enrolError string

// Error prefixes the enrol_no reason.
func (e enrolError) Error() string { return "enrol: " + string(e) }

// errEnrol wraps a controller enrol_no reason.
func errEnrol(s string) error { return enrolError(s) }

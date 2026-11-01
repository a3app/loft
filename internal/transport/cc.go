package transport

// Congestion control choice. The client asks in Hello (off, bbr, fixed),
// the node answers in Status, and each side then switches the pacing of
// what it sends. Off is the system default and is what every session gets
// unless both the client asks and the node allows. Only QUIC fronts (quic,
// h3, and l3 over HTTP/3) switch: both sides set their QUIC connection to
// BBR or a fixed rate. TCP fronts keep the kernel's default: in tests
// kernel BBR made them slower, never faster. Through a relay the node has
// no QUIC connection of the client's, so the answer there is off.

import (
	"context"
	"sync/atomic"

	"github.com/quic-go/quic-go"
	"github.com/quic-go/quic-go/congestion"

	"github.com/a3app/loft/internal/wire"
)

// ccPolicy is the node's offer: allowed ids and fixed-rate caps. Set from
// node.conf at start; a nil value means off.
var ccPolicy atomic.Pointer[wire.CCOffer]

// clientCC is the client's Hello ask.
var clientCC atomic.Pointer[wire.Hello]

// SetCCPolicy replaces the node's congestion control offer. Empty allow
// turns it off.
func SetCCPolicy(allow []byte, maxUp, maxDown uint32) {
	if len(allow) == 0 {
		ccPolicy.Store(nil)
		return
	}
	ccPolicy.Store(&wire.CCOffer{Allow: append([]byte(nil), allow...), MaxUp: maxUp, MaxDown: maxDown})
}

// SetClientCC sets the Hello ask: id and, for fixed, the rates in kbit/s.
// Supervise sets it from the client config; tools that dial directly call it.
func SetClientCC(id byte, up, down uint32) {
	clientCC.Store(&wire.Hello{CC: id, CCUp: up, CCDown: down})
}

// stampCC writes the client's ask into h.
func stampCC(h *wire.Hello) {
	if c := clientCC.Load(); c != nil {
		h.CC, h.CCUp, h.CCDown = c.CC, c.CCUp, c.CCDown
	}
}

// applyCC answers the Hello ask in st and switches the QUIC connection
// under the session. under is the *quic.Conn; anything else (nil, a TCP
// conn) means there is nothing to switch and the answer is off. Call it
// before Seal of Status.
func applyCC(st *wire.Status, front string, hello []byte, under any) {
	if st == nil {
		return
	}
	st.CC, st.CCUp, st.CCDown = wire.CCDefault, 0, 0
	p := ccPolicy.Load()
	if p == nil {
		return
	}
	h, err := wire.DecodeHello(hello)
	if err != nil || h.CC == wire.CCDefault {
		return
	}
	o := *p
	q, _ := under.(*quic.Conn)
	o.QUIC = q != nil
	id, up, down := wire.AgreeCC(front, h.CC, h.CCUp, h.CCDown, o)
	if id == wire.CCDefault {
		return
	}
	switchQUIC(q, id, down)
	st.CC, st.CCUp, st.CCDown = id, up, down
}

// ccInUse is what the node chose for the client's current session.
var ccInUse atomic.Uint32

// CCInUse is the congestion control of the client's current session.
func CCInUse() byte { return byte(ccInUse.Load()) }

// takeCC is the client side: it switches the client's QUIC connection to
// what Status chose. under is the *quic.Conn, or anything else to do nothing
// (TCP fronts keep the kernel's default).
func takeCC(st wire.Status, under any) {
	ccInUse.Store(uint32(st.CC))
	if q, ok := under.(*quic.Conn); ok && st.CC != wire.CCDefault {
		switchQUIC(q, st.CC, st.CCUp)
	}
}

// laneCC applies a session's congestion control to an extra lane (0x04
// attach) carried by under. BBR switches a QUIC lane like the first
// connection; TCP lanes keep the kernel's default. A fixed-rate session
// takes no lanes: they would multiply its rate, so laneCC reports false and
// the lane is refused.
func laneCC(id byte, under any) bool {
	switch id {
	case wire.CCFixed:
		return false
	case wire.CCBBR:
		if q, ok := under.(*quic.Conn); ok {
			switchQUIC(q, id, 0)
		}
	}
	return true
}

// switchQUIC sets q's sender to BBR, or to a fixed rate of kbps. A fixed
// rate of 0 leaves that direction on the default.
func switchQUIC(q *quic.Conn, id byte, kbps uint32) {
	switch {
	case id == wire.CCBBR:
		q.SetCongestionControl(congestion.BBR())
	case id == wire.CCFixed && kbps > 0:
		q.SetCongestionControl(congestion.Fixed(uint64(kbps) * 1000 / 8))
	}
}

type sockKey struct{}

// quicContext is the http3.Server ConnContext: it keeps the QUIC conn in
// the request context so an h3 front can switch its sender.
func quicContext(ctx context.Context, c *quic.Conn) context.Context {
	return context.WithValue(ctx, sockKey{}, c)
}

// sockOf is the conn quicContext kept, or nil (h2 and ws run on TCP).
func sockOf(ctx context.Context) any {
	return ctx.Value(sockKey{})
}

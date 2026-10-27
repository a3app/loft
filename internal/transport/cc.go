package transport

// Congestion control choice. The client asks in Hello (off, bbr, fixed),
// the node answers in Status, and each side then switches the pacing of
// what it sends. Off is the system default and is what every session gets
// unless both the client asks and the node allows. Only QUIC fronts switch;
// TCP fronts keep the kernel's default: in tests kernel BBR made them
// slower, never faster. This build cannot switch a QUIC sender yet, so the
// node answers off.

import (
	"context"
	"net"
	"sync/atomic"

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

func setClientCC(id byte, up, down uint32) {
	clientCC.Store(&wire.Hello{CC: id, CCUp: up, CCDown: down})
}

// stampCC writes the client's ask into h.
func stampCC(h *wire.Hello) {
	if c := clientCC.Load(); c != nil {
		h.CC, h.CCUp, h.CCDown = c.CC, c.CCUp, c.CCDown
	}
}

// applyCC answers the Hello ask in st. sock is the TCP connection under
// TLS, or nil; TCP stays on the kernel's default, so the answer is off.
// Call it before Seal of Status.
func applyCC(st *wire.Status, front string, hello []byte, sock net.Conn) {
	if st == nil {
		return
	}
	st.CC, st.CCUp, st.CCDown = wire.CCDefault, 0, 0
}

type sockKey struct{}

// sockOf is the conn kept in the request context, or nil.
func sockOf(ctx context.Context) net.Conn {
	c, _ := ctx.Value(sockKey{}).(net.Conn)
	return c
}

package transport

// Congestion control choice. The client asks in Hello (off, bbr, fixed),
// the node answers in Status, and each side then switches the pacing of
// what it sends. Off is the system default and is what every session gets
// unless both the client asks and the node allows. On TCP fronts the node
// switches its own socket to BBR (Linux only); the client's upload keeps
// its kernel's default. Through a relay the node has no TCP socket of the
// client's, so the answer there is off.

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

// applyCC answers the Hello ask in st and, for BBR on TCP, switches sock.
// sock is the TCP connection under TLS, or nil when there is none (a relay
// trunk, QUIC). Call it before Seal of Status. If the socket refuses BBR
// (no kernel module, not Linux) the answer is off.
func applyCC(st *wire.Status, front string, hello []byte, sock net.Conn) {
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
	t := tcpOf(sock)
	o.TCP = t != nil
	id, up, down := wire.AgreeCC(front, h.CC, h.CCUp, h.CCDown, o)
	if id == wire.CCBBR && t != nil {
		if setTCPCongestion(t, "bbr") != nil {
			return
		}
	}
	st.CC, st.CCUp, st.CCDown = id, up, down
}

// tcpOf finds the *net.TCPConn under TLS and the node's own wrappers, or nil.
func tcpOf(c net.Conn) *net.TCPConn {
	for i := 0; i < 8 && c != nil; i++ {
		switch u := c.(type) {
		case *net.TCPConn:
			return u
		case *prefixConn:
			c = u.Conn
		case *maskConn:
			c = u.Conn
		case interface{ NetConn() net.Conn }:
			c = u.NetConn()
		default:
			return nil
		}
	}
	return nil
}

type sockKey struct{}

// SockContext is an http.Server ConnContext: it keeps the accepted conn in
// the request context so a front can reach its TCP socket.
func SockContext(ctx context.Context, c net.Conn) context.Context {
	return context.WithValue(ctx, sockKey{}, c)
}

// sockOf is the conn SockContext kept, or nil.
func sockOf(ctx context.Context) net.Conn {
	c, _ := ctx.Value(sockKey{}).(net.Conn)
	return c
}

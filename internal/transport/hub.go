package transport

// Controller hub: relay trunks Open hub:1 (HTTP/1.1) or hub:2 (HTTP/2);
// ctl:1 is fleet control. Member Open is overlay Handle or internet TCP.
//
// Every stream a client opens inside the mux names a target "host:port".
// On the node that target decides where the stream goes: to another member
// on the overlay, to the controller's own service addresses (only for a relay
// trunk), or out to the internet. hub and ctl are not real hosts; they are
// names reserved inside the mux between a relay and the controller.

import (
	"net"
	"net/http"
	"sync"
	"time"

	"golang.org/x/net/http2"

	"github.com/a3app/loft/internal/mux"
	"github.com/a3app/loft/internal/wire"
)

// Hub is the controller HTTP handler that serves member fronts over a relay trunk.
// A relay does not run a member's loft session itself: it removes the public
// TLS and forwards the plain HTTP bytes to the controller through a hub:*
// stream, and the controller serves them with this handler as if they came
// in directly.
var Hub http.Handler

// FleetCtl is the controller fleet (nil if this node is not a controller).
var FleetCtl *Fleet

// attachHello stamps hold and instance_id from Hello onto the mux.
// CapRelay is NOT applied here: a member Hello used to copy NodeCaps and
// become a trunk. Trunk role is applyRelayRole in finishStatus (token known).
// CapHold / instance_id stay so Park + 0x02 still reattach the mux.
func attachHello(ms *mux.Session, rest []byte) {
	if ms == nil || len(rest) == 0 {
		return
	}
	h, err := wire.DecodeHello(rest)
	if err != nil {
		return
	}
	if h.Caps.Has(wire.CapHold) {
		ms.SetHold(true)
	}
	if h.InstanceID != "" {
		ms.SetInstance(h.InstanceID)
	}
}

// proxyStream routes one Open: overlay Handle, ctl:1, hub:*, or internet TCP.
// The overlay gets the first look, so overlay addresses never leak to the
// internet path. ctl and hub are refused on a session that is not a relay
// trunk. Internet TCP follows the egress rules for the member behind the
// session and is then copied both ways until either side ends.
func proxyStream(st *mux.Stream) {
	defer st.Close()
	target := st.Target()
	host, port, err := net.SplitHostPort(target)
	if err != nil || host == "" {
		st.Reset(wire.StreamRefused)
		return
	}
	if o := NetSwitch(); o != nil && o.Handle(st) {
		return
	}
	if host == wire.CtlHost {
		if !st.SessRelay() || FleetCtl == nil {
			st.Reset(wire.StreamRefused)
			return
		}
		FleetCtl.ServeCtl(st)
		return
	}
	if host == wire.HubHost {
		if !st.SessRelay() {
			st.Reset(wire.StreamRefused)
			return
		}
		serveHub(st, port)
		return
	}
	conn, err := dialOut(whoOf(st.Sess()), target)
	if err != nil {
		st.Reset(wire.StreamRefused)
		return
	}
	tuneTCP(conn)
	defer conn.Close()
	go func() { standRelay(conn, st); conn.Close() }()
	standRelay(st, conn)
}

// serveHub serves Hub on hub:2 as HTTP/2 or otherwise HTTP/1.1 over the stream.
func serveHub(st *mux.Stream, port string) {
	h := Hub
	if h == nil {
		st.Reset(wire.StreamRefused)
		return
	}
	if port == wire.HubH2Port {
		H2HTTPServer().ServeConn(st, &http2.ServeConnOpts{Handler: h})
		return
	}
	srv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
	ln := &onceLn{c: st, ch: make(chan struct{})}
	_ = srv.Serve(ln)
}

// onceLn is a net.Listener that Accepts the hub stream once, then blocks the
// next Accept until that connection closes, so http.Server.Serve returns.
//
// Why the wait, and why it matters: http.Server.Serve hands the first conn to
// its own goroutine, then loops and Accepts again. If that second Accept blocked
// forever, Serve would park forever too — one leaked goroutine (plus the server
// and the stream it pins) for every HTTP/1.1 hub or masked connection, growing
// without bound as WebSocket-front clients reconnect. The wrapper below closes
// ch when http.Server finishes the connection (its own defer closes the conn),
// which unblocks the second Accept; Serve then returns and nothing is retained.
type onceLn struct {
	c     net.Conn
	mu    sync.Mutex
	taken bool
	ch    chan struct{}
}

// Accept returns the hub stream (wrapped so its Close unblocks the listener)
// on the first call; later calls wait for that connection to close.
func (o *onceLn) Accept() (net.Conn, error) {
	o.mu.Lock()
	if o.taken {
		o.mu.Unlock()
		<-o.ch
		return nil, net.ErrClosed
	}
	o.taken = true
	c := o.c
	o.mu.Unlock()
	return &onceConn{Conn: c, ln: o}, nil
}

// Close unblocks Accept. Guarded by mu so two Closes cannot both reach the
// default branch and close ch twice (which would panic).
func (o *onceLn) Close() error {
	o.mu.Lock()
	defer o.mu.Unlock()
	select {
	case <-o.ch:
	default:
		close(o.ch)
	}
	return nil
}

// onceConn is the single connection onceLn hands out. When http.Server is done
// with it (connection closed), its Close also closes the listener, so the
// parked second Accept returns and Serve exits.
type onceConn struct {
	net.Conn
	ln *onceLn
}

// Close closes the underlying stream, then unblocks the listener.
func (c *onceConn) Close() error {
	err := c.Conn.Close()
	_ = c.ln.Close()
	return err
}

// Addr is the hub stream's local address.
func (o *onceLn) Addr() net.Addr { return o.c.LocalAddr() }

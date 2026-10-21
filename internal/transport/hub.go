package transport

// Controller hub: relay trunks Open hub:1 (HTTP/1.1) or hub:2 (HTTP/2);
// ctl:1 is fleet control. Member Open is overlay Handle or internet TCP.

import (
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"golang.org/x/net/http2"

	"github.com/a3app/loft/internal/mux"
	"github.com/a3app/loft/internal/wire"
)

// Hub is the controller HTTP handler that serves member fronts over a relay trunk.
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
func proxyStream(st *mux.Stream) {
	defer st.Close()
	target := st.Target()
	host, port, err := net.SplitHostPort(target)
	if err != nil || host == "" {
		st.Reset(wire.StreamRefused)
		return
	}
	if NetSwitch != nil && NetSwitch.Handle(st) {
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
	go func() { _, _ = io.Copy(conn, st); conn.Close() }()
	_, _ = io.Copy(st, conn)
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

// onceLn is a net.Listener that Accepts the hub stream once, then blocks until Close.
type onceLn struct {
	c     net.Conn
	mu    sync.Mutex
	taken bool
	ch    chan struct{}
}

// Accept returns the hub stream on the first call; later calls wait for Close.
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
	return c, nil
}

// Close unblocks Accept.
func (o *onceLn) Close() error {
	select {
	case <-o.ch:
	default:
		close(o.ch)
	}
	return nil
}

// Addr is the hub stream's local address.
func (o *onceLn) Addr() net.Addr { return o.c.LocalAddr() }

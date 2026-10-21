package transport

// UDP dest splice: when decoy is on, UDP/443 is dest's datagrams, except
// marked loft QUIC (ListenUDPDecoy).

import (
	"crypto/tls"
	"errors"
	"net"
	"os"
	"sync"
	"syscall"
	"time"

	"github.com/quic-go/quic-go"
)

const udpFlowIdle = 60 * time.Second

type udpFlow struct {
	up   *net.UDPConn
	from net.Addr
}

// SpliceUDP relays every datagram on listen to dest and dest replies to the
// client. A probe that speaks QUIC to the node IP sees the origin, not loft.
func SpliceUDP(listen, dest string) error {
	if dest == "" {
		return nil
	}
	_, addr := splitDecoy(dest)
	pc, err := net.ListenPacket("udp", listen)
	if err != nil {
		return err
	}
	defer pc.Close()
	upAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return err
	}
	var mu sync.Mutex
	flows := map[string]*udpFlow{}
	buf := make([]byte, 64<<10)
	for {
		n, from, err := pc.ReadFrom(buf)
		if err != nil {
			return err
		}
		key := from.String()
		mu.Lock()
		f := flows[key]
		if f == nil {
			up, derr := net.DialUDP("udp", nil, upAddr)
			if derr != nil {
				mu.Unlock()
				continue
			}
			f = &udpFlow{up: up, from: from}
			flows[key] = f
			go relayUDPReply(pc, f, func() {
				mu.Lock()
				if cur := flows[key]; cur == f {
					delete(flows, key)
				}
				mu.Unlock()
				_ = up.Close()
			})
		}
		mu.Unlock()
		_, _ = f.up.Write(buf[:n])
	}
}

func relayUDPReply(pc net.PacketConn, f *udpFlow, done func()) {
	defer done()
	b := make([]byte, 64<<10)
	for {
		_ = f.up.SetReadDeadline(time.Now().Add(udpFlowIdle))
		n, err := f.up.Read(b)
		if err != nil {
			return
		}
		if _, err := pc.WriteTo(b[:n], f.from); err != nil {
			return
		}
	}
}

// ListenUDPDecoy shares UDP listen between loft QUIC and dest: a flow whose
// Initial carries the loft mark (quicmark.go), or whose short header names
// one of the node's connection IDs, goes to the quic/h3 fronts; every other
// datagram is dest's, as with SpliceUDP. A probe that speaks QUIC to the
// node IP still sees the origin, or nothing when the origin has no HTTP/3.
func ListenUDPDecoy(listen, certFile, keyFile string, info NodeInfo, fronts []string, dest string) error {
	if q, h := udpFronts(fronts); !q && !h {
		return SpliceUDP(listen, dest)
	}
	if dest == "" {
		return ListenUDP(listen, certFile, keyFile, info, fronts)
	}
	_, addr := splitDecoy(dest)
	upAddr, err := net.ResolveUDPAddr("udp", addr)
	if err != nil {
		return err
	}
	pc, err := net.ListenPacket("udp", listen)
	if err != nil {
		return err
	}
	d := newUDPDemux(pc, upAddr, info.Token)
	go d.run()
	defer d.close()
	tr := &quic.Transport{Conn: d.loft, ConnectionIDGenerator: d.cids}
	return serveUDP(certFile, keyFile, info, fronts, func(tc *tls.Config) (*quic.Listener, error) {
		return tr.Listen(tc, quicCfg())
	})
}

const (
	demuxLoftIdle = 3 * time.Minute  // loft QUIC keeps alive every 15 s
	demuxWait     = time.Second      // an Initial without the ClientHello start waits this long
	demuxSeen     = 10 * time.Minute // a client_random is single-use this long (as ecrypto.Nonces)
	demuxPend     = 8
)

type demuxFlow struct {
	loft bool
	up   *udpFlow // dest side of a spliced flow, nil until the first datagram
	pend [][]byte // datagrams held while the first Initial is undecided
	born time.Time
	last time.Time
}

type seenHello struct {
	from string
	at   time.Time
}

// udpDemux reads the shared socket and routes each client address to loft
// or dest. The verdict is taken once per address, on its first datagram.
type udpDemux struct {
	pc     net.PacketConn
	upAddr *net.UDPAddr
	token  []byte
	cids   *serverCIDs
	loft   *demuxConn

	mu    sync.Mutex
	flows map[string]*demuxFlow
	seen  map[string]seenHello
	done  chan struct{}
	once  sync.Once
}

func newUDPDemux(pc net.PacketConn, upAddr *net.UDPAddr, token []byte) *udpDemux {
	d := &udpDemux{pc: pc, upAddr: upAddr, token: token, cids: newServerCIDs(token),
		flows: map[string]*demuxFlow{}, seen: map[string]seenHello{}, done: make(chan struct{})}
	d.loft = newDemuxConn(pc)
	return d
}

func (d *udpDemux) close() {
	d.once.Do(func() {
		close(d.done)
		_ = d.pc.Close()
		_ = d.loft.Close()
	})
}

func (d *udpDemux) run() {
	defer d.close()
	go d.sweep()
	buf := make([]byte, 64<<10)
	for {
		n, from, err := d.pc.ReadFrom(buf)
		if err != nil {
			return
		}
		d.handle(append([]byte(nil), buf[:n]...), from)
	}
}

type verdict int

const (
	toDest verdict = iota
	toLoft
	toWait
)

// classify is the verdict on a datagram from an address with no decided flow.
func (d *udpDemux) classify(pkt []byte, key string, now time.Time) verdict {
	if d.cids.owns(pkt) {
		return toLoft
	}
	scid, random, err := initialHello(pkt)
	if errors.Is(err, errNoHello) && len(scid) == quicMarkLen {
		return toWait
	}
	if err != nil || !verifyQUICMark(d.token, random, scid) {
		return toDest
	}
	if s, ok := d.seen[string(random)]; ok && s.from != key {
		return toDest // a replayed Initial from elsewhere is a probe
	}
	d.seen[string(random)] = seenHello{from: key, at: now}
	return toLoft
}

func (d *udpDemux) handle(pkt []byte, from net.Addr) {
	key := from.String()
	now := time.Now()
	d.mu.Lock()
	f := d.flows[key]
	switch {
	case f == nil:
		f = &demuxFlow{born: now}
		switch d.classify(pkt, key, now) {
		case toLoft:
			f.loft = true
		case toWait:
			f.pend = [][]byte{pkt}
			f.last = now
			d.flows[key] = f
			d.mu.Unlock()
			return
		}
		d.flows[key] = f
	case f.pend != nil:
		switch v := d.classify(pkt, key, now); {
		case v == toWait && len(f.pend) < demuxPend:
			f.pend = append(f.pend, pkt)
			f.last = now
			d.mu.Unlock()
			return
		case v == toLoft:
			f.loft = true
		}
	case !f.loft && pkt[0]&0xf0 == 0xc0 && d.classify(pkt, key, now) == toLoft:
		// The port was dest's a moment ago; a loft client now has it.
		if f.up != nil {
			_ = f.up.up.Close()
			f.up = nil
		}
		f.loft = true
	}
	f.last = now
	pend, loft := append(f.pend, pkt), f.loft
	f.pend = nil
	d.mu.Unlock()
	for _, p := range pend {
		d.route(f, key, loft, p, from)
	}
}

// route sends one datagram of a decided flow; loft is its verdict, read under mu.
func (d *udpDemux) route(f *demuxFlow, key string, loft bool, pkt []byte, from net.Addr) {
	if loft {
		d.loft.push(pkt, from)
		return
	}
	d.mu.Lock()
	up := f.up
	if up == nil {
		c, err := net.DialUDP("udp", nil, d.upAddr)
		if err != nil {
			d.mu.Unlock()
			return
		}
		up = &udpFlow{up: c, from: from}
		f.up = up
		go relayUDPReply(d.pc, up, func() {
			d.mu.Lock()
			if cur := d.flows[key]; cur == f && f.up == up {
				delete(d.flows, key)
			}
			d.mu.Unlock()
			_ = c.Close()
		})
	}
	d.mu.Unlock()
	_, _ = up.up.Write(pkt)
}

// sweep sends undecided flows to dest, drops idle loft flows and old hellos.
func (d *udpDemux) sweep() {
	t := time.NewTicker(demuxWait / 2)
	defer t.Stop()
	for {
		select {
		case <-d.done:
			return
		case now := <-t.C:
			type held struct {
				f    *demuxFlow
				key  string
				pend [][]byte
			}
			var flush []held
			d.mu.Lock()
			for k, f := range d.flows {
				switch {
				case f.pend != nil && now.Sub(f.born) >= demuxWait:
					flush = append(flush, held{f, k, f.pend})
					f.pend = nil
				case f.loft && now.Sub(f.last) >= demuxLoftIdle:
					delete(d.flows, k)
				}
			}
			for r, s := range d.seen {
				if now.Sub(s.at) >= demuxSeen {
					delete(d.seen, r)
				}
			}
			d.mu.Unlock()
			for _, h := range flush {
				if from, err := net.ResolveUDPAddr("udp", h.key); err == nil {
					for _, p := range h.pend {
						d.route(h.f, h.key, false, p, from)
					}
				}
			}
		}
	}
}

type demuxPkt struct {
	b    []byte
	from net.Addr
}

// demuxConn is the loft side of the socket as quic-go sees it: datagrams
// the demux routed to loft in, replies straight out of the shared socket.
type demuxConn struct {
	pc     net.PacketConn
	in     chan demuxPkt
	closed chan struct{}
	once   sync.Once

	mu       sync.Mutex
	deadline time.Time
	wake     chan struct{}
}

func newDemuxConn(pc net.PacketConn) *demuxConn {
	return &demuxConn{pc: pc, in: make(chan demuxPkt, 1024), closed: make(chan struct{}), wake: make(chan struct{})}
}

// push hands a datagram to quic-go; a full queue drops it, as a socket would.
func (c *demuxConn) push(b []byte, from net.Addr) {
	select {
	case c.in <- demuxPkt{b, from}:
	default:
	}
}

func (c *demuxConn) ReadFrom(b []byte) (int, net.Addr, error) {
	for {
		c.mu.Lock()
		dl, wake := c.deadline, c.wake
		c.mu.Unlock()
		var expire <-chan time.Time
		if !dl.IsZero() {
			wait := time.Until(dl)
			if wait <= 0 {
				return 0, nil, os.ErrDeadlineExceeded
			}
			t := time.NewTimer(wait)
			expire = t.C
			defer t.Stop()
		}
		select {
		case p := <-c.in:
			return copy(b, p.b), p.from, nil
		case <-c.closed:
			return 0, nil, net.ErrClosed
		case <-wake:
			// the deadline moved
		case <-expire:
			return 0, nil, os.ErrDeadlineExceeded
		}
	}
}

func (c *demuxConn) WriteTo(b []byte, addr net.Addr) (int, error) { return c.pc.WriteTo(b, addr) }

func (c *demuxConn) Close() error {
	c.once.Do(func() { close(c.closed) })
	return nil
}

func (c *demuxConn) LocalAddr() net.Addr { return c.pc.LocalAddr() }

func (c *demuxConn) SetDeadline(t time.Time) error { return c.SetReadDeadline(t) }

func (c *demuxConn) SetReadDeadline(t time.Time) error {
	c.mu.Lock()
	c.deadline = t
	close(c.wake)
	c.wake = make(chan struct{})
	c.mu.Unlock()
	return nil
}

func (c *demuxConn) SetWriteDeadline(time.Time) error { return nil }

// SetReadBuffer, SetWriteBuffer and SyscallConn let quic-go size the shared
// socket's buffers as it would its own.
func (c *demuxConn) SetReadBuffer(n int) error {
	if u, ok := c.pc.(*net.UDPConn); ok {
		return u.SetReadBuffer(n)
	}
	return errors.New("not a UDP socket")
}

func (c *demuxConn) SetWriteBuffer(n int) error {
	if u, ok := c.pc.(*net.UDPConn); ok {
		return u.SetWriteBuffer(n)
	}
	return errors.New("not a UDP socket")
}

func (c *demuxConn) SyscallConn() (syscall.RawConn, error) {
	if u, ok := c.pc.(*net.UDPConn); ok {
		return u.SyscallConn()
	}
	return nil, errors.New("not a UDP socket")
}

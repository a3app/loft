package transport

// L3 front: HTTP/2 CONNECT (same path and 0x02 as h2), then AEAD IP datagrams.
// Corked 0x01 batches amortize Seal/Open; Rebind swaps CONNECT without dropping the IP pump.

import (
	"context"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/a3app/loft/internal/config"
	ecrypto "github.com/a3app/loft/internal/crypto"
	"github.com/a3app/loft/internal/mux"
	"github.com/a3app/loft/internal/wire"
)

const (
	l3InCap    = 256
	l3Cork     = 16 << 10
	l3CorkWait = 2 * time.Millisecond
	l3Rebind   = 15 * time.Second
)

// L3 is HTTP/2 CONNECT carrying IPv4/IPv6 datagrams. Opt-in via fronts: l3.
type L3 struct {
	cfg   config.Outbound
	token []byte
	offer wire.Cap
	inner *l3Sess
	conn  net.Conn
}

// NewL3 builds an L3 client that has not yet CONNECT'd.
func NewL3(token string, o config.Outbound, offer wire.Cap) *L3 {
	return &L3{cfg: o, token: []byte(token), offer: offer | wire.CapL3}
}

// Session is nil: L3 has no mux.
func (l *L3) Session() *mux.Session { return nil }

// Done is closed when the CONNECT stream dies and Rebind did not replace it.
func (l *L3) Done() <-chan struct{} {
	if l.inner == nil {
		ch := make(chan struct{})
		close(ch)
		return ch
	}
	return l.inner.done
}

// Open is mux-only. L3 carries IP; use WriteIP/ReadIP.
func (l *L3) Open(string) (net.Conn, error) {
	return nil, fmt.Errorf("l3: IP front")
}

// WriteIP seals one datagram onto the CONNECT body (corked unless urgent).
func (l *L3) WriteIP(p []byte) error {
	if l.inner == nil {
		return io.ErrClosedPipe
	}
	return l.inner.WriteIP(p)
}

// ReadIP blocks for one datagram from the node.
func (l *L3) ReadIP() ([]byte, error) {
	if l.inner == nil {
		return nil, io.EOF
	}
	return l.inner.ReadIP()
}

// Park drops CONNECT without a Leave frame.
func (l *L3) Park() error {
	if l.inner != nil {
		l.inner.die()
	}
	if l.conn != nil {
		return l.conn.Close()
	}
	return nil
}

// Close tears down the CONNECT stream.
func (l *L3) Close() error {
	if l.inner != nil {
		l.inner.die()
	}
	if l.conn != nil {
		return l.conn.Close()
	}
	return nil
}

// Start dials TLS and HTTP/2 CONNECT, then runs the L3 handshake.
func (l *L3) Start(ctx context.Context) error {
	c, stream, addr, sni, err := dialH2CONNECT(ctx, l.cfg, l.token)
	if err != nil {
		return err
	}
	inner, err := startL3(ctx, stream, l.token, l.offer, addr, sni)
	if err != nil {
		_ = stream.Close()
		_ = c.Close()
		return err
	}
	l.conn = c
	l.inner = inner
	return nil
}

// Rebind replaces the CONNECT 4-tuple after an egress change.
// Overlay IP pump, inbound queue, and Done stay; a fresh 0x02 mints a new AEAD key.
func (l *L3) Rebind() error {
	if l == nil || l.inner == nil {
		return fmt.Errorf("l3 rebind: not live")
	}
	select {
	case <-l.inner.done:
		return fmt.Errorf("l3 rebind: not live")
	default:
	}
	ctx, cancel := context.WithTimeout(context.Background(), l3Rebind)
	defer cancel()
	c, stream, addr, sni, err := dialH2CONNECT(ctx, l.cfg, l.token)
	if err != nil {
		return err
	}
	key, err := l3Handshake(stream, l.token, l.offer, addr, sni)
	if err != nil {
		_ = c.Close()
		return err
	}
	old := l.conn
	l.conn = c
	l.inner.adopt(stream, key)
	if old != nil && old != c {
		_ = old.Close()
	}
	return nil
}

// l3Sess is the authenticated IP pump on one CONNECT stream.
type l3Sess struct {
	mu   sync.Mutex
	conn net.Conn
	key  []byte
	in   chan []byte
	done chan struct{}
	dead sync.Once

	wmu   sync.Mutex
	cork  [][]byte
	corkN int
	timer *time.Timer
	werr  error
}

func (s *l3Sess) die() {
	s.wmu.Lock()
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	_ = s.flushLocked()
	s.wmu.Unlock()
	s.dead.Do(func() { close(s.done) })
}

// adopt swaps the live CONNECT and AEAD key. Old readLoop returns without die().
func (s *l3Sess) adopt(conn net.Conn, key []byte) {
	s.wmu.Lock()
	_ = s.flushLocked()
	s.mu.Lock()
	old := s.conn
	s.conn = conn
	s.key = key
	s.werr = nil
	s.mu.Unlock()
	s.wmu.Unlock()
	go s.readLoop(conn, key)
	if old != nil && old != conn {
		_ = old.Close()
	}
}

// WriteIP implements l3Sink.
func (s *l3Sess) WriteIP(p []byte) error {
	if s == nil || len(p) < 20 {
		return nil
	}
	pkt := append([]byte(nil), p...)
	s.wmu.Lock()
	defer s.wmu.Unlock()
	select {
	case <-s.done:
		return io.ErrClosedPipe
	default:
	}
	if s.werr != nil {
		return s.werr
	}
	urgent := l3Urgent(pkt)
	if s.corkN+len(pkt)+3 > l3Cork && len(s.cork) > 0 {
		if err := s.flushLocked(); err != nil {
			return err
		}
	}
	s.cork = append(s.cork, pkt)
	s.corkN += len(pkt) + 3
	if urgent || s.corkN >= l3Cork {
		return s.flushLocked()
	}
	if s.timer == nil {
		s.timer = time.AfterFunc(l3CorkWait, s.flushAsync)
	}
	return nil
}

func (s *l3Sess) flushAsync() {
	s.wmu.Lock()
	defer s.wmu.Unlock()
	s.timer = nil
	_ = s.flushLocked()
}

func (s *l3Sess) flushLocked() error {
	if s.timer != nil {
		s.timer.Stop()
		s.timer = nil
	}
	if len(s.cork) == 0 {
		return s.werr
	}
	pkts := s.cork
	s.cork = nil
	s.corkN = 0
	err := s.sendLocked(pkts)
	if err != nil {
		s.werr = err
	}
	return err
}

func (s *l3Sess) sendLocked(pkts [][]byte) error {
	var plain []byte
	if len(pkts) == 1 {
		plain = wire.EncodeL3(wire.L3IP, pkts[0])
	} else {
		plain = wire.EncodeL3Batch(pkts)
	}
	s.mu.Lock()
	conn, key := s.conn, s.key
	s.mu.Unlock()
	if conn == nil || len(key) == 0 {
		return io.ErrClosedPipe
	}
	sealed, err := ecrypto.Seal(key, plain, nil)
	if err != nil {
		return err
	}
	return writeMsg(conn, sealed)
}

// ReadIP waits for one inbound datagram.
func (s *l3Sess) ReadIP() ([]byte, error) {
	select {
	case <-s.done:
		return nil, io.EOF
	case p := <-s.in:
		return p, nil
	}
}

func l3Urgent(p []byte) bool {
	if len(p) < 20 {
		return false
	}
	switch p[9] {
	case 1, ipProtoUDP:
		return true
	case ipProtoTCP:
		ihl := int(p[0]&0x0f) * 4
		if ihl < 20 || len(p) < ihl+14 {
			return true
		}
		return p[ihl+13]&(tcpSYN|tcpFIN|tcpRST) != 0
	default:
		return false
	}
}

func l3Deliver(plain []byte, emit func([]byte)) {
	pkts, err := wire.SplitL3(plain)
	if err != nil || len(pkts) == 0 {
		return
	}
	for _, p := range pkts {
		if len(p) < 20 {
			continue
		}
		emit(append([]byte(nil), p...))
	}
}

// l3Handshake writes 0x02 and reads AEAD(Status). On error it closes conn.
func l3Handshake(conn net.Conn, token []byte, offer wire.Cap, addr, sni string) ([]byte, error) {
	hello := newClientHello(offer | wire.CapL3)
	proof, nonce, sk, err := ecrypto.MakeAuth(token)
	if err != nil {
		conn.Close()
		return nil, err
	}
	auth := append(proof, hello.Encode()...)
	if err := writeMsg(conn, auth); err != nil {
		conn.Close()
		return nil, err
	}
	raw, err := readMsg(conn)
	if err != nil {
		conn.Close()
		return nil, err
	}
	key, plain, err := ecrypto.OpenHello(sk, nonce, raw, nil)
	if err != nil {
		conn.Close()
		return nil, err
	}
	st, err := wire.DecodeStatus(plain)
	if err != nil {
		conn.Close()
		return nil, err
	}
	if !st.Code.OK() {
		conn.Close()
		return nil, wire.Fail(st.Code, st.Program)
	}
	// A node that did not enable l3 still answers 0x02 on the h2 path and
	// then speaks mux. CapL3 is the only signal that the body is IP frames.
	if !st.Caps.Has(wire.CapL3) {
		conn.Close()
		return nil, fmt.Errorf("l3: node has no l3")
	}
	RememberVis(st)
	if addr == "" {
		addr = conn.RemoteAddr().String()
	}
	if err := adoptDecoy(addr, sni, st, true); err != nil {
		conn.Close()
		return nil, err
	}
	return key, nil
}

// startL3 writes 0x02, reads AEAD(Status), and pumps IP frames.
func startL3(ctx context.Context, conn net.Conn, token []byte, offer wire.Cap, addr, sni string) (*l3Sess, error) {
	_ = ctx
	key, err := l3Handshake(conn, token, offer, addr, sni)
	if err != nil {
		return nil, err
	}
	s := &l3Sess{conn: conn, key: key, in: make(chan []byte, l3InCap), done: make(chan struct{})}
	go s.readLoop(conn, key)
	return s, nil
}

func (s *l3Sess) readLoop(conn net.Conn, key []byte) {
	defer func() {
		s.mu.Lock()
		replaced := s.conn != conn
		s.mu.Unlock()
		if !replaced {
			s.die()
		}
	}()
	for {
		raw, err := readMsg(conn)
		if err != nil {
			return
		}
		plain, err := ecrypto.Open(key, raw, nil)
		if err != nil {
			continue
		}
		l3Deliver(plain, func(pkt []byte) {
			select {
			case s.in <- pkt:
			case <-s.done:
			}
		})
		select {
		case <-s.done:
			return
		default:
		}
	}
}

// serveL3 authenticates (already done), writes AEAD(Status), and switches IP.
func serveL3(conn net.Conn, info NodeInfo, a ecrypto.Auth) {
	defer conn.Close()
	link := &l3Sess{conn: conn, key: a.Key, in: make(chan []byte, l3InCap), done: make(chan struct{})}
	st := info.Status("l3")
	finishStatusL3(&st, a.Token, a.Rest, link, conn.RemoteAddr())
	sealed, err := ecrypto.Seal(a.Key, st.Encode(), nil)
	if err != nil {
		return
	}
	if err := writeMsg(conn, ecrypto.WrapHello(a.EphN, sealed)); err != nil {
		return
	}
	defer func() {
		link.die()
		if o := NetSwitch(); o != nil {
			o.DetachL3(link)
		}
	}()
	for {
		raw, err := readMsg(conn)
		if err != nil {
			return
		}
		plain, err := ecrypto.Open(a.Key, raw, nil)
		if err != nil {
			continue
		}
		o := NetSwitch()
		l3Deliver(plain, func(pkt []byte) {
			if o != nil {
				o.DeliverL3(link, pkt)
			}
		})
	}
}

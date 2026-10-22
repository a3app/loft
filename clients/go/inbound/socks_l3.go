package inbound

// L3 SOCKS dialer: gVisor IPv4 over an IPPump (same shape as SSTPNet).

import (
	"context"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/a3app/loft/internal/mux"
	"github.com/a3app/loft/internal/transport"

	"gvisor.dev/gvisor/pkg/buffer"
	"gvisor.dev/gvisor/pkg/tcpip"
	"gvisor.dev/gvisor/pkg/tcpip/adapters/gonet"
	"gvisor.dev/gvisor/pkg/tcpip/header"
	"gvisor.dev/gvisor/pkg/tcpip/link/channel"
	"gvisor.dev/gvisor/pkg/tcpip/network/ipv4"
	"gvisor.dev/gvisor/pkg/tcpip/stack"
	"gvisor.dev/gvisor/pkg/tcpip/transport/tcp"
	"gvisor.dev/gvisor/pkg/tcpip/transport/udp"
)

const l3NIC = 1

// L3Net is a Dialer that opens TCP/UDP on the L3 overlay IP.
type L3Net struct {
	pumpMu sync.RWMutex
	pump   transport.IPPump
	d      transport.Dialer
	wait   chan struct{}

	ep  *channel.Endpoint
	stk *stack.Stack
	our tcpip.Address

	portMu sync.Mutex
	ports  map[uint16]struct{}
	next   uint16

	tunMu sync.Mutex
	tun   io.ReadWriteCloser

	done      chan struct{}
	cancel    context.CancelFunc
	closeOnce sync.Once
	up, down  atomic.Uint64
}

// WrapL3 returns n wrapping d when d carries IP. d is returned unchanged otherwise.
func WrapL3(d transport.Dialer) (transport.Dialer, error) {
	if d == nil {
		return d, nil
	}
	p, ok := d.(transport.IPPump)
	if !ok {
		return d, nil
	}
	n, err := NewL3Net(d, p)
	if err != nil {
		return d, err
	}
	return n, nil
}

// NewL3Net binds a gVisor IPv4 stack to pump and starts the packet pump.
func NewL3Net(d transport.Dialer, pump transport.IPPump) (*L3Net, error) {
	if pump == nil {
		return nil, fmt.Errorf("l3: no pump")
	}
	ip := net.ParseIP(transport.OverlayIP()).To4()
	if ip == nil {
		return nil, fmt.Errorf("l3: no overlay ip")
	}
	var a [4]byte
	copy(a[:], ip)
	ep := channel.New(2048, 1280, tcpip.LinkAddress("\x02\x00\x00\x00\x00\x03"))
	s := stack.New(stack.Options{
		NetworkProtocols:   []stack.NetworkProtocolFactory{ipv4.NewProtocol},
		TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol, udp.NewProtocol},
	})
	if err := s.CreateNIC(l3NIC, ep); err != nil {
		return nil, fmt.Errorf("l3 nic: %s", err)
	}
	if err := s.AddProtocolAddress(l3NIC, tcpip.ProtocolAddress{
		Protocol: ipv4.ProtocolNumber,
		AddressWithPrefix: tcpip.AddressWithPrefix{
			Address:   tcpip.AddrFrom4(a),
			PrefixLen: 32,
		},
	}, stack.AddressProperties{}); err != nil {
		return nil, fmt.Errorf("l3 addr: %s", err)
	}
	s.SetRouteTable([]tcpip.Route{{Destination: header.IPv4EmptySubnet, NIC: l3NIC}})
	_ = s.SetOption(tcpip.SendBufferSizeOption{Min: 4 << 10, Default: 4 << 20, Max: 16 << 20})
	_ = s.SetOption(tcpip.ReceiveBufferSizeOption{Min: 4 << 10, Default: 4 << 20, Max: 16 << 20})
	nagle := tcpip.TCPDelayEnabled(false)
	_ = s.SetTransportProtocolOption(tcp.ProtocolNumber, &nagle)
	ctx, cancel := context.WithCancel(context.Background())
	n := &L3Net{
		pump: pump, d: d, wait: make(chan struct{}),
		ep: ep, stk: s, our: tcpip.AddrFrom4(a),
		ports: make(map[uint16]struct{}), next: 49152,
		done: make(chan struct{}), cancel: cancel,
	}
	go n.stackOut(ctx)
	go n.fromL3()
	return n, nil
}

// Open dials TCP on the overlay toward target (host:port).
func (n *L3Net) Open(target string) (net.Conn, error) {
	host, portStr, err := net.SplitHostPort(target)
	if err != nil {
		return nil, err
	}
	p, err := strconv.Atoi(portStr)
	if err != nil || p <= 0 || p > 65535 {
		return nil, fmt.Errorf("l3 port")
	}
	ip := net.ParseIP(host)
	if ip == nil {
		ip, err = n.LookupIP4(host)
		if err != nil {
			return nil, err
		}
	}
	v4 := ip.To4()
	if v4 == nil {
		return nil, fmt.Errorf("l3: ipv6")
	}
	lp := n.allocPort()
	if lp == 0 {
		return nil, fmt.Errorf("l3 ports")
	}
	var dst [4]byte
	copy(dst[:], v4)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	c, err := gonet.DialTCPWithBind(ctx, n.stk,
		tcpip.FullAddress{NIC: l3NIC, Addr: n.our, Port: lp},
		tcpip.FullAddress{NIC: l3NIC, Addr: tcpip.AddrFrom4(dst), Port: uint16(p)},
		ipv4.ProtocolNumber)
	if err != nil {
		n.freePort(lp)
		return nil, err
	}
	return &l3Conn{Conn: c, n: n, port: lp}, nil
}

// ListenUDP binds one unconnected UDP socket on the overlay.
func (n *L3Net) ListenUDP() (net.PacketConn, error) {
	lp := n.allocPort()
	if lp == 0 {
		return nil, fmt.Errorf("l3 ports")
	}
	laddr := tcpip.FullAddress{NIC: l3NIC, Addr: n.our, Port: lp}
	c, err := gonet.DialUDP(n.stk, &laddr, nil, ipv4.ProtocolNumber)
	if err != nil {
		n.freePort(lp)
		return nil, err
	}
	return &l3Packet{PacketConn: c, n: n, port: lp}, nil
}

// LookupIP4 resolves host through the host resolver (debug path).
func (n *L3Net) LookupIP4(host string) (net.IP, error) {
	if ip := net.ParseIP(host); ip != nil {
		if v4 := ip.To4(); v4 != nil {
			return v4, nil
		}
		return nil, fmt.Errorf("l3: ipv6")
	}
	ips, err := net.DefaultResolver.LookupIP(context.Background(), "ip4", host)
	if err != nil {
		return nil, err
	}
	if len(ips) == 0 {
		return nil, fmt.Errorf("l3: no a")
	}
	return ips[0].To4(), nil
}

func (n *L3Net) Session() *mux.Session { return nil }
func (n *L3Net) Done() <-chan struct{} { return n.done }

func (n *L3Net) ConnBytes() (uint64, uint64) { return n.up.Load(), n.down.Load() }

func (n *L3Net) Close() error {
	n.closeOnce.Do(func() {
		n.cancel()
		n.ep.Close()
		n.stk.Close()
		n.tunMu.Lock()
		tun := n.tun
		n.tun = nil
		n.tunMu.Unlock()
		if tun != nil {
			_ = tun.Close()
		}
		n.pumpMu.Lock()
		d := n.d
		n.d = nil
		n.pump = nil
		wait := n.wait
		n.wait = make(chan struct{})
		n.pumpMu.Unlock()
		if wait != nil {
			close(wait)
		}
		if d != nil {
			_ = d.Close()
		}
		close(n.done)
	})
	return nil
}

// Swap replaces the CONNECT pump. gVisor, SOCKS, and TUN stay.
func (n *L3Net) Swap(d transport.Dialer, pump transport.IPPump) {
	if n == nil || pump == nil {
		return
	}
	n.pumpMu.Lock()
	old := n.d
	n.d = d
	n.pump = pump
	wait := n.wait
	n.wait = make(chan struct{})
	n.pumpMu.Unlock()
	if wait != nil {
		close(wait)
	}
	if old != nil && old != d {
		_ = old.Close()
	}
}

func (n *L3Net) livePump() (transport.IPPump, <-chan struct{}) {
	n.pumpMu.RLock()
	p, wait := n.pump, n.wait
	n.pumpMu.RUnlock()
	return p, wait
}

func (n *L3Net) writePump(ip []byte) error {
	for {
		p, wait := n.livePump()
		if p != nil {
			if err := p.WriteIP(ip); err == nil {
				return nil
			}
		}
		select {
		case <-n.done:
			return io.ErrClosedPipe
		case <-wait:
		}
	}
}

// AttachTUN splices kernel utun packets with the L3 pump.
func (n *L3Net) AttachTUN(tun io.ReadWriteCloser) {
	n.tunMu.Lock()
	same := n.tun == tun
	n.tun = tun
	n.tunMu.Unlock()
	if same {
		return
	}
	go n.fromTUN(tun)
}

func (n *L3Net) fromL3() {
	for {
		p, wait := n.livePump()
		if p == nil {
			select {
			case <-n.done:
				return
			case <-wait:
				continue
			}
		}
		ip, err := p.ReadIP()
		if err != nil {
			select {
			case <-n.done:
				return
			case <-wait:
				continue
			}
		}
		n.down.Add(uint64(len(ip)))
		if n.toStack(ip) {
			continue
		}
		n.tunMu.Lock()
		tun := n.tun
		n.tunMu.Unlock()
		if tun != nil {
			_, _ = tun.Write(ip)
		}
	}
}

func (n *L3Net) fromTUN(tun io.ReadWriteCloser) {
	buf := make([]byte, 2048)
	for {
		nr, err := tun.Read(buf)
		if err != nil {
			return
		}
		if nr == 0 {
			continue
		}
		pkt := append([]byte(nil), buf[:nr]...)
		n.up.Add(uint64(len(pkt)))
		if err := n.writePump(pkt); err != nil {
			return
		}
	}
}

func (n *L3Net) stackOut(ctx context.Context) {
	for {
		pkt := n.ep.ReadContext(ctx)
		if pkt == nil {
			return
		}
		v := pkt.ToView()
		b := make([]byte, v.Size())
		_, _ = v.Read(b)
		v.Release()
		pkt.DecRef()
		ip := asIPv4(b)
		if ip == nil {
			continue
		}
		n.up.Add(uint64(len(ip)))
		if err := n.writePump(ip); err != nil {
			return
		}
	}
}

func (n *L3Net) toStack(ip []byte) bool {
	if !n.stackOwns(ip) {
		return false
	}
	pkt := stack.NewPacketBuffer(stack.PacketBufferOptions{Payload: buffer.MakeWithData(append([]byte(nil), ip...))})
	pkt.NetworkProtocolNumber = header.IPv4ProtocolNumber
	pkt.RXChecksumValidated = true
	n.ep.InjectInbound(header.IPv4ProtocolNumber, pkt)
	pkt.DecRef()
	return true
}

func (n *L3Net) stackOwns(ip []byte) bool {
	port, proto, ok := ipv4DstPort(ip)
	if ok && (proto == 6 || proto == 17) && n.hasPort(port) {
		return true
	}
	if lp, ok := icmpInnerSrcPort(ip); ok && n.hasPort(lp) {
		return true
	}
	return false
}

func (n *L3Net) allocPort() uint16 {
	n.portMu.Lock()
	defer n.portMu.Unlock()
	for i := 0; i < 16384; i++ {
		p := n.next
		n.next++
		if n.next < 49152 {
			n.next = 49152
		}
		if _, used := n.ports[p]; !used {
			n.ports[p] = struct{}{}
			return p
		}
	}
	return 0
}

func (n *L3Net) freePort(p uint16) {
	n.portMu.Lock()
	delete(n.ports, p)
	n.portMu.Unlock()
}

func (n *L3Net) hasPort(p uint16) bool {
	n.portMu.Lock()
	_, ok := n.ports[p]
	n.portMu.Unlock()
	return ok
}

type l3Conn struct {
	net.Conn
	n    *L3Net
	port uint16
	once sync.Once
}

func (c *l3Conn) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() { c.n.freePort(c.port) })
	return err
}

type l3Packet struct {
	net.PacketConn
	n    *L3Net
	port uint16
	once sync.Once
}

func (c *l3Packet) Close() error {
	err := c.PacketConn.Close()
	c.once.Do(func() { c.n.freePort(c.port) })
	return err
}

var _ transport.Dialer = (*L3Net)(nil)

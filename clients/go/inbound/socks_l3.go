package inbound

// L3 SOCKS dialer: gVisor IPv4 over an IPPump (same shape as SSTPNet).
//
// An L3 front carries raw IP packets to the node (an IPPump) instead of mux
// streams. SOCKS still needs TCP connections, so L3Net runs a small TCP/IP
// stack in user space (gVisor) on the client's overlay address and sends
// its packets through the pump. Packets the stack does not own go to the
// attached TUN. The pump can be swapped on reconnect while the stack, the
// SOCKS listener and the TUN keep running.

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	mrand "math/rand/v2"
	"net"
	"os"
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

// l3NIC is the id of the single virtual network card in the gVisor stack.
const l3NIC = 1

// L3Net is a Dialer that opens TCP/UDP on the L3 overlay IP.
type L3Net struct {
	pumpMu sync.RWMutex
	pump   transport.IPPump
	d      transport.Dialer
	wait   chan struct{}

	ep    *channel.Endpoint
	stk   *stack.Stack
	ourMu sync.Mutex
	our   tcpip.Address // overlay address of the stack; Swap may change it

	portMu sync.Mutex
	ports  map[uint16]struct{}
	next   uint16

	tunMu sync.Mutex
	tun   io.ReadWriteCloser

	done      chan struct{}
	cancel    context.CancelFunc
	closeOnce sync.Once
	up, down  atomic.Uint64
	orphan    atomic.Uint64 // packets from the tunnel no socket owned (Stats)
	traceMu   sync.Mutex
	traceSeen map[string]int // LOFT_L3_TRACE: segments sent so far
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
	a, ok := overlayAddr()
	if !ok {
		return nil, fmt.Errorf("l3: no overlay ip")
	}
	// The link queue holds packets the stack sends until stackOut passes
	// them to the tunnel; when it is full, gVisor drops them. 8192 packets
	// (about 10 MB) keep a full TCP send window of a few flows in it.
	ep := channel.New(8192, 1280, tcpip.LinkAddress("\x02\x00\x00\x00\x00\x03"))
	s := stack.New(stack.Options{
		NetworkProtocols:   []stack.NetworkProtocolFactory{ipv4.NewProtocol},
		TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol, udp.NewProtocol},
	})
	if err := s.CreateNIC(l3NIC, ep); err != nil {
		return nil, fmt.Errorf("l3 nic: %s", err)
	}
	if err := addOverlay(s, a); err != nil {
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
		ep: ep, stk: s, our: a,
		// Start at a random port: every L3Net of this member shares one
		// overlay address, so a fixed start reused the last session's
		// ports while the node may still hold those flows.
		ports: make(map[uint16]struct{}), next: 49152 + uint16(mrand.IntN(16384)),
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
		tcpip.FullAddress{NIC: l3NIC, Addr: n.ourAddr(), Port: lp},
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
	laddr := tcpip.FullAddress{NIC: l3NIC, Addr: n.ourAddr(), Port: lp}
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

// Session is nil: an L3 front carries IP, not a mux session.
func (n *L3Net) Session() *mux.Session { return nil }

// Done is closed when L3Net is closed.
func (n *L3Net) Done() <-chan struct{} { return n.done }

// ConnBytes is the IP bytes sent (up) and received (down) through the pump.
func (n *L3Net) ConnBytes() (uint64, uint64) { return n.up.Load(), n.down.Load() }

// Close shuts L3Net down for good. Readers waiting for a new pump are woken
// so they see Done and exit.
//
// Steps:
//  1. Stop stackOut and the gVisor stack.
//  2. Close the attached TUN, which also ends fromTUN.
//  3. Drop the pump and wake everyone blocked on the old wait channel.
//  4. Close the front's Dialer, then close Done.
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
	n.followOverlay()
}

// overlayAddr reads the overlay IPv4 the node gave in its last Status.
func overlayAddr() (tcpip.Address, bool) {
	ip := net.ParseIP(transport.OverlayIP()).To4()
	if ip == nil {
		return tcpip.Address{}, false
	}
	return tcpip.AddrFrom4Slice(ip), true
}

// addOverlay gives the stack's network card the address a (/32).
func addOverlay(s *stack.Stack, a tcpip.Address) tcpip.Error {
	return s.AddProtocolAddress(l3NIC, tcpip.ProtocolAddress{
		Protocol:          ipv4.ProtocolNumber,
		AddressWithPrefix: tcpip.AddressWithPrefix{Address: a, PrefixLen: 32},
	}, stack.AddressProperties{})
}

func (n *L3Net) ourAddr() tcpip.Address {
	n.ourMu.Lock()
	defer n.ourMu.Unlock()
	return n.our
}

// followOverlay moves the stack to the overlay address of the new session.
// A node that restarted hands out addresses again, so after a reconnect the
// member can get another one. The stack would keep dropping replies sent to
// the new address, and new connections would leave from the old one.
// Connections bound to the old address are gone with the old session anyway.
func (n *L3Net) followOverlay() {
	a, ok := overlayAddr()
	if !ok {
		return
	}
	n.ourMu.Lock()
	defer n.ourMu.Unlock()
	if a == n.our {
		return
	}
	if err := addOverlay(n.stk, a); err != nil {
		return
	}
	_ = n.stk.RemoveAddress(l3NIC, n.our)
	n.our = a
}

// livePump returns the current pump and a channel that closes when the
// pump is swapped or L3Net is closed.
func (n *L3Net) livePump() (transport.IPPump, <-chan struct{}) {
	n.pumpMu.RLock()
	p, wait := n.pump, n.wait
	n.pumpMu.RUnlock()
	return p, wait
}

// writePump sends one packet through the current pump. If there is no pump
// or the write fails, it waits for the next Swap and tries again, so a
// reconnect does not drop the packet. It fails only once L3Net is closed.
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

// fromL3 reads packets from the pump for as long as L3Net lives.
//
// Steps:
//  1. Take the current pump. With no pump, or after a read error, wait for
//     a Swap (or Close) instead of giving up: the front is being replaced.
//  2. Packets for our own sockets go into the gVisor stack.
//  3. Everything else is written to the attached TUN, if any.
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
		} else {
			n.orphan.Add(1) // not for our sockets and no TUN: nowhere to go
		}
	}
}

// L3NetStats are drop and retransmission counters of an L3Net, for
// debugging lost packets on the l3 front: Dropped is packets the gVisor
// stack threw away (mostly a full receive queue of a socket), Orphan
// packets from the tunnel that no socket owned, and Retransmits, Timeouts
// and FastRetransmits what the stack had to send again toward the node.
type L3NetStats struct {
	Dropped, Orphan, Retransmits, Timeouts, FastRetransmits uint64
	// SendErrors are segments the stack could not hand to the link (a full
	// link queue); TLP and SpuriousRTO count tail-loss probes and timeouts
	// that turned out needless.
	SendErrors, TLP, SpuriousRTO uint64
}

// Stats returns the counters since this L3Net started.
func (n *L3Net) Stats() L3NetStats {
	st := n.stk.Stats()
	return L3NetStats{
		Dropped:         st.DroppedPackets.Value(),
		Orphan:          n.orphan.Load(),
		Retransmits:     st.TCP.Retransmits.Value(),
		Timeouts:        st.TCP.Timeouts.Value(),
		FastRetransmits: st.TCP.FastRetransmit.Value(),
		SendErrors:      st.TCP.SegmentSendErrors.Value(),
		TLP:             st.TCP.TLPRecovery.Value(),
		SpuriousRTO:     st.TCP.SpuriousRTORecovery.Value(),
	}
}

// fromTUN reads packets from the kernel TUN and sends them through the
// pump. It returns when the TUN read fails or L3Net is closed.
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

// stackOut takes packets the gVisor stack wants to send and writes them to
// the pump. Anything that is not IPv4 is skipped.
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
		if l3Trace {
			n.trace(ip)
		}
		if err := n.writePump(ip); err != nil {
			return
		}
	}
}

// l3Trace turns on a debug log of TCP segments this stack sends again
// (LOFT_L3_TRACE=1): the same sequence number and length seen before on
// the same connection. It shows what the stack is retransmitting.
// Debug only: it remembers every segment, so it grows for as long as it runs.
var l3Trace = os.Getenv("LOFT_L3_TRACE") == "1"

// trace logs ip if it repeats a TCP segment already sent (see l3Trace).
func (n *L3Net) trace(ip []byte) {
	if len(ip) < 40 || ip[9] != 6 {
		return
	}
	ihl := int(ip[0]&0x0f) * 4
	th := ip[ihl:]
	if len(th) < 20 {
		return
	}
	off := int(th[12]>>4) * 4
	key := fmt.Sprintf("%d>%s.%d:%d/%d", binary.BigEndian.Uint16(th[0:2]), net.IP(ip[16:20]), binary.BigEndian.Uint16(th[2:4]), binary.BigEndian.Uint32(th[4:8]), len(th)-off)
	n.traceMu.Lock()
	if n.traceSeen == nil {
		n.traceSeen = map[string]int{}
	}
	n.traceSeen[key]++
	again := n.traceSeen[key]
	n.traceMu.Unlock()
	if again > 1 && (len(th) > off || th[13]&0x03 != 0) {
		log.Printf("l3 trace: resend #%d %s flags=%#x", again-1, key, th[13])
	}
}

// toStack hands an incoming packet to the gVisor stack if one of our
// sockets owns it and reports whether it did.
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

// stackOwns reports whether ip is for one of our sockets: a TCP or UDP
// packet to a port we reserved, or an ICMP error about a packet we sent
// from such a port.
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

// allocPort reserves a free local port from 49152 up (the range meant for
// temporary ports), wrapping around at the top. It returns 0 if none of
// 16384 tries found a free port.
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

// freePort gives back a port taken by allocPort.
func (n *L3Net) freePort(p uint16) {
	n.portMu.Lock()
	delete(n.ports, p)
	n.portMu.Unlock()
}

// hasPort reports whether port p is reserved by one of our sockets.
func (n *L3Net) hasPort(p uint16) bool {
	n.portMu.Lock()
	_, ok := n.ports[p]
	n.portMu.Unlock()
	return ok
}

// l3Conn is a TCP socket on the L3 stack that frees its reserved local
// port when closed.
type l3Conn struct {
	net.Conn
	n    *L3Net
	port uint16
	once sync.Once
}

// Close closes the socket and frees its port once.
func (c *l3Conn) Close() error {
	err := c.Conn.Close()
	// Keep the port for a while: Close only starts TCP's closing (FIN,
	// the peer's ACK and FIN, TIME_WAIT), which the stack finishes with
	// packets that come later. Freed at once, the port stopped being ours,
	// those packets were dropped as nobody's (stackOwns), and the stack
	// resent its FIN for a minute on every connection. Holding it also
	// keeps a new socket off a port still in TIME_WAIT.
	c.once.Do(func() { time.AfterFunc(l3PortLinger, func() { c.n.freePort(c.port) }) })
	return err
}

// l3PortLinger is how long a closed TCP socket's port stays reserved:
// longer than gVisor's TIME_WAIT (60 s).
const l3PortLinger = 70 * time.Second

// l3Packet is a UDP socket on the L3 stack that frees its reserved local
// port when closed.
type l3Packet struct {
	net.PacketConn
	n    *L3Net
	port uint16
	once sync.Once
}

// Close closes the socket and frees its port once.
func (c *l3Packet) Close() error {
	err := c.PacketConn.Close()
	c.once.Do(func() { c.n.freePort(c.port) })
	return err
}

var _ transport.Dialer = (*L3Net)(nil)

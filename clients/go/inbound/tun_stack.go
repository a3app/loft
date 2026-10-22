package inbound

// tun_stack.go is the gVisor userspace stack: TCP splice, UDP/ICMP datagrams.

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"os"
	"strconv"
	"sync"
	"time"

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
	"gvisor.dev/gvisor/pkg/waiter"
)

const (
	nicID   = 1
	maxUDP  = 128
	udpIdle = 60 * time.Second
	// tunPktBuf is one IPv4 datagram at the NIC MTU (1280) plus headroom.
	tunPktBuf = 2048
	emitCap   = 64
)

// tunIO is a TUN device: IP packets in and out, plus an interface name.
type tunIO interface {
	io.ReadWriteCloser
	Name() string
}

// tunStack is the gVisor IPv4 stack bound to one TUN, with ICMP/UDP NAT state.
type tunStack struct {
	ep        *channel.Endpoint
	stk       *stack.Stack
	ifc       tunIO
	d         transport.Dialer
	our       net.IP
	net       *net.IPNet
	outCancel context.CancelFunc
	natMu     sync.Mutex
	nat       map[uint32][4]byte // ICMP id+seq → original IPv4 src (Darwin dummy)
	udpMu     sync.Mutex
	udp       map[string][]io.Writer // dest host:port → gVisor UDP conns
	udpNat    map[string][]udpFlow   // dest host:port → original src (Darwin dummy)
	emitCh    chan []byte            // TUN writes off the read/ingest path
}

// newStack builds an IPv4 gVisor NIC, TCP/UDP forwarders, and the TUN pump loops.
func newStack(ifc tunIO, d transport.Dialer, our net.IP, ipnet *net.IPNet) (*tunStack, error) {
	ep := channel.New(2048, 1280, tcpip.LinkAddress("\x02\x00\x00\x00\x00\x01"))
	s := stack.New(stack.Options{
		NetworkProtocols:   []stack.NetworkProtocolFactory{ipv4.NewProtocol},
		TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol, udp.NewProtocol},
	})
	if err := s.CreateNIC(nicID, ep); err != nil {
		return nil, errTCP(err)
	}
	s.SetPromiscuousMode(nicID, true)
	s.SetSpoofing(nicID, true)
	_ = s.SetForwardingDefaultAndAllNICs(ipv4.ProtocolNumber, true)
	s.SetRouteTable([]tcpip.Route{{Destination: header.IPv4EmptySubnet, NIC: nicID}})

	ts := &tunStack{ep: ep, stk: s, ifc: ifc, d: d, net: ipnet, nat: make(map[uint32][4]byte), udp: make(map[string][]io.Writer), udpNat: make(map[string][]udpFlow)}
	if our != nil {
		ts.our = our.To4()
	} else if ip := net.ParseIP(transport.OverlayIP()); ip != nil {
		ts.our = ip.To4()
	}
	if ts.our != nil {
		var a [4]byte
		copy(a[:], ts.our)
		if err := s.AddProtocolAddress(nicID, tcpip.ProtocolAddress{
			Protocol: ipv4.ProtocolNumber,
			AddressWithPrefix: tcpip.AddressWithPrefix{
				Address:   tcpip.AddrFrom4(a),
				PrefixLen: 32,
			},
		}, stack.AddressProperties{}); err != nil {
			return nil, errTCP(err)
		}
	}

	tcpFwd := tcp.NewForwarder(s, 0, 1024, func(r *tcp.ForwarderRequest) {
		id := r.ID()
		dest := net.JoinHostPort(id.LocalAddress.String(), strconv.Itoa(int(id.LocalPort)))
		go func() {
			wq := new(waiter.Queue)
			ep, terr := r.CreateEndpoint(wq)
			if terr != nil {
				if _, ok := terr.(*tcpip.ErrAborted); !ok {
					fmt.Fprintf(os.Stderr, "tun tcp %s: %s\n", dest, terr)
				}
				r.Complete(true)
				return
			}
			r.Complete(false)
			ep.SocketOptions().SetDelayOption(false)
			ep.SocketOptions().SetSendBufferSize(4<<20, true)
			ep.SocketOptions().SetReceiveBufferSize(4<<20, true)
			local := gonet.NewTCPConn(wq, ep)
			defer local.Close()
			ip := net.ParseIP(id.LocalAddress.String())
			if id.LocalPort == 53 && overlayDNS(ip) {
				serveLocalDNSTCP(local)
				return
			}
			var remote net.Conn
			var derr error
			switch transport.LookupAddr("", ip) {
			case transport.ActDrop:
				return
			case transport.ActDirect:
				remote, derr = net.DialTimeout("tcp", dest, 15*time.Second)
			default:
				if d == nil {
					return
				}
				remote, derr = d.Open(dest)
			}
			if derr != nil {
				fmt.Fprintf(os.Stderr, "tun tcp open %s: %v\n", dest, derr)
				return
			}
			defer remote.Close()
			pipe(local, remote)
		}()
	})
	s.SetTransportProtocolHandler(tcp.ProtocolNumber, tcpFwd.HandlePacket)

	udpFwd := udp.NewForwarder(s, func(r *udp.ForwarderRequest) {
		id := r.ID()
		dest := net.JoinHostPort(id.LocalAddress.String(), strconv.Itoa(int(id.LocalPort)))
		if transport.LookupAddr("", net.ParseIP(id.LocalAddress.String())) == transport.ActDrop {
			return
		}
		wq := new(waiter.Queue)
		ep, terr := r.CreateEndpoint(wq)
		if terr != nil {
			return
		}
		conn := gonet.NewUDPConn(wq, ep)
		go ts.relayUDP(conn, dest)
	})
	s.SetTransportProtocolHandler(udp.ProtocolNumber, udpFwd.HandlePacket)

	ts.Attach(d)
	ctx, cancel := context.WithCancel(context.Background())
	ts.outCancel = cancel
	ts.emitCh = make(chan []byte, emitCap)
	go ts.emitLoop(ctx)
	go ts.tunToStack()
	go ts.stackToTun(ctx)
	return ts, nil
}

// tunToStack reads TUN packets: ICMP/UDP first, else inject IPv4 into gVisor.
func (t *tunStack) tunToStack() {
	fmt.Fprintf(os.Stderr, "tun read-loop %s\n", t.ifc.Name())
	buf := make([]byte, tunPktBuf)
	var seen uint32
	for {
		n, err := t.ifc.Read(buf)
		if err != nil {
			fmt.Fprintf(os.Stderr, "tun read: %v\n", err)
			return
		}
		if n < 20 {
			continue
		}
		raw := stripUTUN(buf[:n])
		if raw = ipv4Datagram(raw); raw == nil {
			continue
		}
		switch classifyTUN(raw) {
		case tunIPv4:
		default:
			// No overlay IPv6 yet: drop instead of feeding the IPv4 stack.
			// Happy Eyeballs then uses IPv4, same as an IPv4-only tunnel.
			continue
		}
		if seen < 8 {
			seen++
			fmt.Fprintf(os.Stderr, "tun pkt n=%d proto=%d dst=%s\n", len(raw), raw[9], net.IP(raw[16:20]))
		}
		if t.handleICMP(raw) {
			continue
		}
		if t.handleUDP(raw) {
			continue
		}
		pkt := stack.NewPacketBuffer(stack.PacketBufferOptions{Payload: buffer.MakeWithData(append([]byte(nil), raw...))})
		pkt.NetworkProtocolNumber = header.IPv4ProtocolNumber
		pkt.RXChecksumValidated = true
		t.ep.InjectInbound(header.IPv4ProtocolNumber, pkt)
		pkt.DecRef()
	}
}

// stackToTun writes gVisor outbound packets to the TUN until ctx is cancelled.
func (t *tunStack) stackToTun(ctx context.Context) {
	for {
		pkt := t.ep.ReadContext(ctx)
		if pkt == nil {
			return
		}
		v := pkt.ToView()
		b := make([]byte, v.Size())
		_, _ = v.Read(b)
		v.Release()
		if len(b) > 0 {
			t.writeTUN(b)
		}
		pkt.DecRef()
	}
}

// ipv4Datagram returns a complete IPv4 header+payload, or nil if truncated.
func ipv4Datagram(raw []byte) []byte {
	if len(raw) < 20 || raw[0]>>4 != 4 {
		return raw
	}
	tot := int(binary.BigEndian.Uint16(raw[2:4]))
	if tot < 20 || tot > len(raw) {
		return nil
	}
	return raw[:tot]
}

// emit queues a packet for the TUN writer. Full queue drops (UDP/ICMP).
// Tests that never started emitLoop write through ifc directly.
func (t *tunStack) emit(pkt []byte) {
	if len(pkt) == 0 {
		return
	}
	if t.emitCh == nil {
		t.writeTUN(pkt)
		return
	}
	p := append([]byte(nil), pkt...)
	select {
	case t.emitCh <- p:
	default:
	}
}

func (t *tunStack) writeTUN(pkt []byte) {
	if t.ifc == nil || len(pkt) == 0 {
		return
	}
	_, _ = t.ifc.Write(pkt)
}

func (t *tunStack) emitLoop(ctx context.Context) {
	for {
		select {
		case <-ctx.Done():
			return
		case p := <-t.emitCh:
			t.writeTUN(p)
		}
	}
}

// Close unhooks datagrams, cancels the TUN pump, and closes the gVisor stack.
func (t *tunStack) Close() error {
	if t.d != nil {
		if sess := t.d.Session(); sess != nil {
			sess.FilterDgram(nil)
		}
	}
	if t.outCancel != nil {
		t.outCancel()
	}
	t.ep.Close()
	t.stk.Close()
	return nil
}

// addUDP registers a gVisor UDP conn for dest host:port replies.
// At cap, the oldest dest is evicted and closed so relay goroutines exit.
func (t *tunStack) addUDP(dest string, w io.Writer) {
	var evict io.Closer
	t.udpMu.Lock()
	if t.udp == nil {
		t.udp = make(map[string][]io.Writer)
	}
	n := 0
	for _, list := range t.udp {
		n += len(list)
	}
	if n >= maxUDP {
		for d, list := range t.udp {
			if len(list) == 0 {
				continue
			}
			if c, ok := list[0].(io.Closer); ok {
				evict = c
			}
			if len(list) == 1 {
				delete(t.udp, d)
			} else {
				t.udp[d] = list[1:]
			}
			break
		}
	}
	t.udp[dest] = append(t.udp[dest], w)
	t.udpMu.Unlock()
	if evict != nil {
		_ = evict.Close()
	}
}

// delUDP unregisters a gVisor UDP conn.
func (t *tunStack) delUDP(dest string, w io.Writer) {
	t.udpMu.Lock()
	defer t.udpMu.Unlock()
	list := t.udp[dest]
	out := list[:0]
	for _, c := range list {
		if c != w {
			out = append(out, c)
		}
	}
	if len(out) == 0 {
		delete(t.udp, dest)
		return
	}
	t.udp[dest] = out
}

// injectUDP writes a mux datagram into registered gVisor UDP conns for addr.
func (t *tunStack) injectUDP(addr string, data []byte) bool {
	t.udpMu.Lock()
	list := t.udp[addr]
	t.udpMu.Unlock()
	if len(list) == 0 {
		return false
	}
	for _, w := range list {
		if d, ok := w.(interface{ SetWriteDeadline(time.Time) error }); ok {
			_ = d.SetWriteDeadline(time.Now().Add(time.Millisecond))
		}
		_, _ = w.Write(data)
	}
	return true
}

// relayUDP copies gVisor UDP payloads to mux WriteDgram or ListenUDP until idle or error.
func (t *tunStack) relayUDP(conn *gonet.UDPConn, dest string) {
	defer conn.Close()
	if t.d == nil {
		return
	}
	if sess := t.d.Session(); sess != nil {
		t.addUDP(dest, conn)
		defer t.delUDP(dest, conn)
		buf := make([]byte, 65535)
		for {
			_ = conn.SetReadDeadline(time.Now().Add(udpIdle))
			n, _, err := conn.ReadFrom(buf)
			if err != nil {
				return
			}
			if err := sess.WriteDgram(dest, buf[:n]); err != nil {
				return
			}
		}
	}
	ud, ok := t.d.(interface {
		ListenUDP() (net.PacketConn, error)
	})
	if !ok {
		return
	}
	via, err := ud.ListenUDP()
	if err != nil {
		return
	}
	defer via.Close()
	raddr, err := net.ResolveUDPAddr("udp", dest)
	if err != nil {
		return
	}
	t.addUDP(dest, conn)
	defer t.delUDP(dest, conn)
	go func() {
		buf := make([]byte, 65535)
		for {
			n, _, err := via.ReadFrom(buf)
			if err != nil {
				_ = conn.Close()
				return
			}
			_, _ = conn.Write(buf[:n])
		}
	}()
	buf := make([]byte, 65535)
	for {
		_ = conn.SetReadDeadline(time.Now().Add(udpIdle))
		n, _, err := conn.ReadFrom(buf)
		if err != nil {
			return
		}
		if _, err := via.WriteTo(buf[:n], raddr); err != nil {
			return
		}
	}
}

// pipe copies both ways and half-closes, matching SOCKS relay.
func pipe(a, b io.ReadWriteCloser) {
	done := make(chan struct{}, 2)
	cp := func(dst, src io.ReadWriteCloser) {
		buf := make([]byte, 128<<10)
		_, _ = io.CopyBuffer(dst, src, buf)
		if cw, ok := dst.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite()
		}
		done <- struct{}{}
	}
	go cp(a, b)
	go cp(b, a)
	<-done
	<-done
}

// errTCP wraps a gVisor tcpip.Error.
func errTCP(e tcpip.Error) error {
	if e == nil {
		return nil
	}
	return fmt.Errorf("gvisor: %s", e)
}

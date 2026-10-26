package transport

// UDP replies use the destination address the datagram arrived on.
// A wildcard listen on a host with a secondary IPv4 (A-record on eth0
// next to the primary) otherwise sends from the primary; NAT that
// dialed the secondary drops the reply. TCP does not: the stack copies
// the SYN dest.
//
// The fix: read each datagram with its destination address (IP_PKTINFO /
// IPV6_PKTINFO control message), remember it per remote address, and set it
// as the source when replying to that remote. Used by the UDP listeners
// (QUIC/HTTP/3 front and the decoy UDP splice).

import (
	"net"
	"sync"
	"syscall"
	"time"

	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

// listenUDPSticky listens on addr and replies from the address each peer
// wrote to.
func listenUDPSticky(addr string) (net.PacketConn, error) {
	pc, err := net.ListenPacket("udp", addr)
	if err != nil {
		return nil, err
	}
	return newStickyUDP(pc), nil
}

// stickyUDP is a PacketConn that remembers, per remote address, which local
// IP the remote sent to (src), and uses that IP as the reply source.
type stickyUDP struct {
	uc  *net.UDPConn
	p4  *ipv4.PacketConn
	oob []byte
	mu  sync.Mutex
	src map[string]net.IP
}

// newStickyUDP turns on destination-address control messages; pc is
// returned as is when it is not a UDPConn.
func newStickyUDP(pc net.PacketConn) net.PacketConn {
	uc, ok := pc.(*net.UDPConn)
	if !ok {
		return pc
	}
	p4 := ipv4.NewPacketConn(uc)
	if err := p4.SetControlMessage(ipv4.FlagDst, true); err != nil {
		_ = ipv6.NewPacketConn(uc).SetControlMessage(ipv6.FlagDst, true)
	}
	return &stickyUDP{uc: uc, p4: p4, oob: make([]byte, 128), src: map[string]net.IP{}}
}

// udpConnOf is the UDPConn under pc (plain or sticky), or nil.
func udpConnOf(pc net.PacketConn) *net.UDPConn {
	switch c := pc.(type) {
	case *net.UDPConn:
		return c
	case *stickyUDP:
		return c.uc
	default:
		return nil
	}
}

// ReadFrom reads one datagram and records its destination IP for addr.
func (s *stickyUDP) ReadFrom(p []byte) (int, net.Addr, error) {
	n, oobn, _, addr, err := s.uc.ReadMsgUDP(p, s.oob)
	if err != nil {
		return n, addr, err
	}
	if ip := dstFromOOB(s.oob[:oobn]); ip != nil && addr != nil {
		s.mu.Lock()
		s.src[addr.String()] = ip
		s.mu.Unlock()
	}
	return n, addr, nil
}

// WriteTo replies from the recorded IP for addr, or as a plain write when
// none is known.
func (s *stickyUDP) WriteTo(p []byte, addr net.Addr) (int, error) {
	ip := s.lookup(addr)
	ua, _ := addr.(*net.UDPAddr)
	if ip == nil || ua == nil {
		return s.uc.WriteTo(p, addr)
	}
	oob := srcOOB(ip)
	if len(oob) == 0 {
		return s.uc.WriteTo(p, addr)
	}
	n, _, err := s.uc.WriteMsgUDP(p, oob, ua)
	return n, err
}

func (s *stickyUDP) lookup(addr net.Addr) net.IP {
	if addr == nil {
		return nil
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.src[addr.String()]
}

func (s *stickyUDP) Close() error                       { return s.uc.Close() }
func (s *stickyUDP) LocalAddr() net.Addr                { return s.uc.LocalAddr() }
func (s *stickyUDP) SetDeadline(t time.Time) error      { return s.uc.SetDeadline(t) }
func (s *stickyUDP) SetReadDeadline(t time.Time) error  { return s.uc.SetReadDeadline(t) }
func (s *stickyUDP) SetWriteDeadline(t time.Time) error { return s.uc.SetWriteDeadline(t) }
func (s *stickyUDP) SetReadBuffer(n int) error          { return s.uc.SetReadBuffer(n) }
func (s *stickyUDP) SetWriteBuffer(n int) error         { return s.uc.SetWriteBuffer(n) }
func (s *stickyUDP) SyscallConn() (syscall.RawConn, error) {
	return s.uc.SyscallConn()
}

// dstFromOOB extracts the destination IP from a control message.
func dstFromOOB(oob []byte) net.IP {
	if len(oob) == 0 {
		return nil
	}
	cm4 := &ipv4.ControlMessage{}
	if cm4.Parse(oob) == nil && cm4.Dst != nil {
		return append(net.IP(nil), cm4.Dst...)
	}
	cm6 := &ipv6.ControlMessage{}
	if cm6.Parse(oob) == nil && cm6.Dst != nil {
		return append(net.IP(nil), cm6.Dst...)
	}
	return nil
}

// srcOOB builds a control message that sets the source IP of a send.
func srcOOB(ip net.IP) []byte {
	if ip == nil {
		return nil
	}
	if v4 := ip.To4(); v4 != nil {
		return (&ipv4.ControlMessage{Src: v4}).Marshal()
	}
	return (&ipv6.ControlMessage{Src: ip}).Marshal()
}

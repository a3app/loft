package inbound

// SSTP SOCKS dialer: userspace IPv4 (gVisor) over the PPP session.
// kernelDial used the host stack, so SOCKS left via en0.
//
// SSTP is Microsoft's VPN protocol: PPP (a point-to-point link that carries
// IP packets) inside TLS. loft can use an SSTP server as its way out. The
// server hands out one IPv4 address; SSTPNet runs a small TCP/IP stack in
// user space (gVisor) on that address, so SOCKS connections leave through
// the SSTP session instead of the host's normal network card. Packets that
// the stack does not own go to an attached TUN, if any.

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/a3app/loft/internal/mux"
	"github.com/a3app/loft/internal/sstp"
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

// sstpNIC is the id of the single virtual network card in the gVisor stack.
const sstpNIC = 1

// SSTPNet is a transport.Dialer that opens TCP/UDP on the SSTP overlay IP.
type SSTPNet struct {
	sess *sstp.Session
	ep   *channel.Endpoint
	stk  *stack.Stack
	our  tcpip.Address
	dns  []net.IP

	portMu sync.Mutex
	ports  map[uint16]struct{}
	next   uint16

	tunMu sync.Mutex
	tun   io.ReadWriteCloser

	done      chan struct{}
	cancel    context.CancelFunc
	closeOnce sync.Once

	logf func(string, ...any)
	tap  func([]byte)
	seen int

	up, down atomic.Uint64
}

// NewSSTPNet binds a gVisor IPv4 stack to sess and starts the packet pump.
func NewSSTPNet(sess *sstp.Session) (*SSTPNet, error) {
	ip := sess.IP.To4()
	if ip == nil {
		return nil, fmt.Errorf("sstp: no ipv4")
	}
	var a [4]byte
	copy(a[:], ip)
	mtu := uint32(sess.MTU())
	if mtu < 1280 {
		mtu = 1280
	}
	ep := channel.New(2048, mtu, tcpip.LinkAddress("\x02\x00\x00\x00\x00\x02"))
	s := stack.New(stack.Options{
		NetworkProtocols:   []stack.NetworkProtocolFactory{ipv4.NewProtocol},
		TransportProtocols: []stack.TransportProtocolFactory{tcp.NewProtocol, udp.NewProtocol},
	})
	if err := s.CreateNIC(sstpNIC, ep); err != nil {
		return nil, fmt.Errorf("sstp nic: %s", err)
	}
	if err := s.AddProtocolAddress(sstpNIC, tcpip.ProtocolAddress{
		Protocol: ipv4.ProtocolNumber,
		AddressWithPrefix: tcpip.AddressWithPrefix{
			Address:   tcpip.AddrFrom4(a),
			PrefixLen: 32,
		},
	}, stack.AddressProperties{}); err != nil {
		return nil, fmt.Errorf("sstp addr: %s", err)
	}
	s.SetRouteTable([]tcpip.Route{{Destination: header.IPv4EmptySubnet, NIC: sstpNIC}})
	ctx, cancel := context.WithCancel(context.Background())
	n := &SSTPNet{
		sess: sess, ep: ep, stk: s, our: tcpip.AddrFrom4(a),
		dns:   ResolveDNS(sess.DNS, sess.Peer, nil, false),
		ports: make(map[uint16]struct{}), next: 49152, done: make(chan struct{}), cancel: cancel,
		logf: func(string, ...any) {},
	}
	go n.stackOut(ctx)
	go n.fromSSTP()
	return n, nil
}

// SetLog prints stack/DNS/TCP traces (loft-front -v, loft-probe -v).
func (n *SSTPNet) SetLog(fn func(string, ...any)) {
	if fn == nil {
		fn = func(string, ...any) {}
	}
	n.logf = fn
}

// SetTap sees every inbound IP before demux (ICMP probes).
func (n *SSTPNet) SetTap(fn func([]byte)) { n.tap = fn }

// OverlayIP is the SSTP-assigned IPv4.
func (n *SSTPNet) OverlayIP() net.IP {
	return n.sess.IP
}

// ApplyDNS sets resolvers: IPCP, else the PPP peer, plus extra.
// replace with a non-empty extra list skips IPCP and the gateway.
func (n *SSTPNet) ApplyDNS(extra []net.IP, replace bool) {
	n.dns = ResolveDNS(n.sess.DNS, n.sess.Peer, extra, replace)
}

// DNS is the resolver list SOCKS/TUN name lookup will use.
func (n *SSTPNet) DNS() []net.IP {
	out := make([]net.IP, len(n.dns))
	copy(out, n.dns)
	return out
}

// Bytes is overlay IP octets written (up) and read (down) on the PPP session.
func (n *SSTPNet) Bytes() (up, down uint64) {
	return n.up.Load(), n.down.Load()
}

// AttachTUN splices kernel utun packets with the SSTP session (L3).
func (n *SSTPNet) AttachTUN(tun io.ReadWriteCloser) {
	n.tunMu.Lock()
	n.tun = tun
	n.tunMu.Unlock()
	go n.fromTUN(tun)
}

// Open makes a TCP connection to target ("host:port") from the SSTP address.
// A host name is resolved through the SSTP DNS servers first. Only IPv4 is
// supported. Each connection reserves a local port so incoming packets for
// it can be told apart from TUN traffic (see stackOwns). The dial gives up
// after 20 seconds.
func (n *SSTPNet) Open(target string) (net.Conn, error) {
	host, portStr, err := net.SplitHostPort(target)
	if err != nil {
		return nil, err
	}
	p, err := strconv.Atoi(portStr)
	if err != nil || p <= 0 || p > 65535 {
		return nil, fmt.Errorf("sstp port")
	}
	ip := net.ParseIP(host)
	if ip == nil {
		ip, err = n.lookup4(host)
		if err != nil {
			return nil, err
		}
	}
	v4 := ip.To4()
	if v4 == nil {
		return nil, fmt.Errorf("sstp: ipv6")
	}
	lp := n.allocPort()
	if lp == 0 {
		return nil, fmt.Errorf("sstp ports")
	}
	var dst [4]byte
	copy(dst[:], v4)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	n.logf("tcp %s local=:%d", target, lp)
	c, err := gonet.DialTCPWithBind(ctx, n.stk,
		tcpip.FullAddress{NIC: sstpNIC, Addr: n.our, Port: lp},
		tcpip.FullAddress{NIC: sstpNIC, Addr: tcpip.AddrFrom4(dst), Port: uint16(p)},
		ipv4.ProtocolNumber)
	if err != nil {
		n.freePort(lp)
		n.logf("tcp %s: %v", target, err)
		return nil, err
	}
	n.logf("tcp %s ok local=%s remote=%s", target, c.LocalAddr(), c.RemoteAddr())
	return &sstpConn{Conn: c, n: n, port: lp}, nil
}

// DialUDP opens one connected UDP socket on the SSTP overlay (SOCKS UDP ASSOCIATE).
func (n *SSTPNet) DialUDP(addr string) (net.Conn, error) {
	host, portStr, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}
	p, err := strconv.Atoi(portStr)
	if err != nil || p <= 0 || p > 65535 {
		return nil, fmt.Errorf("sstp port")
	}
	ip := net.ParseIP(host)
	if ip == nil {
		ip, err = n.lookup4(host)
		if err != nil {
			return nil, err
		}
	}
	v4 := ip.To4()
	if v4 == nil {
		return nil, fmt.Errorf("sstp: ipv6")
	}
	lp := n.allocPort()
	if lp == 0 {
		return nil, fmt.Errorf("sstp ports")
	}
	var dst [4]byte
	copy(dst[:], v4)
	laddr := tcpip.FullAddress{NIC: sstpNIC, Addr: n.our, Port: lp}
	raddr := tcpip.FullAddress{NIC: sstpNIC, Addr: tcpip.AddrFrom4(dst), Port: uint16(p)}
	c, err := gonet.DialUDP(n.stk, &laddr, &raddr, ipv4.ProtocolNumber)
	if err != nil {
		n.freePort(lp)
		return nil, err
	}
	return &sstpConn{Conn: c, n: n, port: lp}, nil
}

// ListenUDP binds one unconnected UDP socket on the overlay (SOCKS UDP ASSOCIATE).
func (n *SSTPNet) ListenUDP() (net.PacketConn, error) {
	lp := n.allocPort()
	if lp == 0 {
		return nil, fmt.Errorf("sstp ports")
	}
	laddr := tcpip.FullAddress{NIC: sstpNIC, Addr: n.our, Port: lp}
	c, err := gonet.DialUDP(n.stk, &laddr, nil, ipv4.ProtocolNumber)
	if err != nil {
		n.freePort(lp)
		return nil, err
	}
	return &sstpPacket{PacketConn: c, n: n, port: lp}, nil
}

// LookupIP4 resolves host through the SSTP DNS servers.
func (n *SSTPNet) LookupIP4(host string) (net.IP, error) {
	return n.lookup4(host)
}

// Session is nil: SSTP carries plain IP, not a loft mux session.
func (n *SSTPNet) Session() *mux.Session { return nil }

// Done is closed when the SSTP session ends and the Dialer must be replaced.
func (n *SSTPNet) Done() <-chan struct{} { return n.done }

// Close stops the packet goroutines and the stack, closes the attached TUN,
// and closes Done. It is safe to call more than once.
func (n *SSTPNet) Close() error {
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
		close(n.done)
	})
	return nil
}

// fromSSTP reads every IP packet that arrives over SSTP. Packets for our
// own sockets go into the gVisor stack; the rest go to the attached TUN.
// When the session read fails, the whole SSTPNet is closed.
func (n *SSTPNet) fromSSTP() {
	defer n.Close()
	for {
		ip, err := n.sess.ReadIP()
		if err != nil {
			n.logf("read ip: %v", err)
			return
		}
		n.logPkt("in", ip)
		n.down.Add(uint64(len(ip)))
		if tap := n.tap; tap != nil {
			tap(append([]byte(nil), ip...))
		}
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

// fromTUN reads packets from the kernel TUN and sends them over SSTP. Pings
// to our own overlay address are answered locally, because the SSTP server
// would not answer them.
func (n *SSTPNet) fromTUN(tun io.ReadWriteCloser) {
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
		// A --> A sends ping of our overlay IP into utun. Answer locally
		// (loft tun_stack does the same); the PPP peer will not.
		if reply, ok := icmpEchoReply(pkt, n.sess.IP); ok {
			_, _ = tun.Write(reply)
			continue
		}
		if err := n.sess.WriteIP(pkt); err != nil {
			return
		}
	}
}

// stackOut takes packets the gVisor stack wants to send and writes them to
// the SSTP session. A frame with an Ethernet header is trimmed to its IPv4
// payload; anything that is not IPv4 is skipped.
func (n *SSTPNet) stackOut(ctx context.Context) {
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
			n.logf("out skip n=%d head=%x", len(b), headN(b, 16))
			continue
		}
		n.logPkt("out", ip)
		n.up.Add(uint64(len(ip)))
		if err := n.sess.WriteIP(ip); err != nil {
			n.logf("write ip: %v", err)
			return
		}
	}
}

// toStack hands an incoming packet to the gVisor stack if one of our
// sockets owns it and reports whether it did. An ICMP "packet too big"
// message lowers the MTU first, whoever owns it.
func (n *SSTPNet) toStack(ip []byte) bool {
	if v := sstp.ICMPNextHopMTU(ip); v > 0 {
		n.applyPMTU(v)
	}
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
func (n *SSTPNet) stackOwns(ip []byte) bool {
	port, proto, ok := ipv4DstPort(ip)
	if ok && (proto == 6 || proto == 17) && n.hasPort(port) {
		return true
	}
	if lp, ok := icmpInnerSrcPort(ip); ok && n.hasPort(lp) {
		return true
	}
	return false
}

// applyPMTU lowers the stack and session MTU (the largest packet size) to
// v after the path reported it, but never below 1280, the IPv6 minimum
// that the rest of loft also uses. A larger value is ignored.
func (n *SSTPNet) applyPMTU(v int) {
	if v < 1280 {
		v = 1280
	}
	cur := int(n.ep.MTU())
	if v >= cur {
		return
	}
	n.ep.SetMTU(uint32(v))
	if n.sess != nil {
		n.sess.SetMTU(v)
	}
	n.logf("pmtu %d", v)
}

// icmpInnerSrcPort is the original datagram's source port inside ICMP errors.
func icmpInnerSrcPort(ip []byte) (uint16, bool) {
	if len(ip) < 28 || ip[0]>>4 != 4 || ip[9] != 1 {
		return 0, false
	}
	ihl := int(ip[0]&0xf) * 4
	if ihl < 20 || len(ip) < ihl+8 {
		return 0, false
	}
	if ip[ihl] != 3 && ip[ihl] != 11 {
		return 0, false
	}
	inner := ip[ihl+8:]
	if len(inner) < 24 || inner[0]>>4 != 4 {
		return 0, false
	}
	iihl := int(inner[0]&0xf) * 4
	if iihl < 20 || len(inner) < iihl+2 {
		return 0, false
	}
	return binary.BigEndian.Uint16(inner[iihl : iihl+2]), true
}

// lookup4 resolves host to an IPv4 address through the SSTP DNS servers.
// Each server is asked over UDP first and then over TCP, since some
// networks drop one of the two. The last error is returned if all fail.
func (n *SSTPNet) lookup4(host string) (net.IP, error) {
	n.logf("dns %s", host)
	var last error
	tried := 0
	for _, s := range n.dns {
		v4 := s.To4()
		if v4 == nil {
			continue
		}
		tried++
		dns := v4.String()
		if ip, err := n.lookupDNS(dns, host, false); err == nil {
			return ip, nil
		} else {
			last = err
			n.logf("dns udp %s: %v", dns, err)
		}
		if ip, err := n.lookupDNS(dns, host, true); err == nil {
			return ip, nil
		} else {
			last = err
			n.logf("dns tcp %s: %v", dns, err)
		}
	}
	if tried == 0 {
		return nil, fmt.Errorf("sstp: no dns")
	}
	if last == nil {
		last = fmt.Errorf("sstp: no dns")
	}
	return nil, last
}

// ParseDNSList reads IPv4 addresses separated by comma, space, or semicolon.
func ParseDNSList(s string) []net.IP {
	split := func(r rune) bool {
		return r == ',' || r == ';' || r == ' ' || r == '\t' || r == '\n' || r == '\r'
	}
	var out []net.IP
	seen := map[string]struct{}{}
	for _, p := range strings.FieldsFunc(s, split) {
		ip := net.ParseIP(p)
		if ip == nil {
			continue
		}
		v4 := ip.To4()
		if v4 == nil || v4.IsUnspecified() {
			continue
		}
		k := v4.String()
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, append(net.IP(nil), v4...))
	}
	return out
}

// JoinDNS is comma-separated IPv4 for STATUS and logs.
func JoinDNS(ips []net.IP) string {
	var b []string
	for _, ip := range ips {
		if v4 := ip.To4(); v4 != nil {
			b = append(b, v4.String())
		}
	}
	return strings.Join(b, ",")
}

// ResolveDNS picks SSTP resolvers the way Windows RAS does:
// IPCP DNS if the server sent any, else the PPP gateway.
// extra appends to that list; extra with replace uses only extra.
func ResolveDNS(ipcp []net.IP, peer net.IP, extra []net.IP, replace bool) []net.IP {
	extra = v4DNS(extra)
	if replace && len(extra) > 0 {
		return extra
	}
	auto := v4DNS(ipcp)
	if len(auto) == 0 {
		if v4 := peer.To4(); v4 != nil && !v4.IsUnspecified() {
			auto = []net.IP{append(net.IP(nil), v4...)}
		}
	}
	if len(extra) == 0 {
		return auto
	}
	return v4DNS(append(auto, extra...))
}

// v4DNS keeps only usable IPv4 addresses from in, without duplicates.
// Each address is copied so later changes to in do not affect the result.
func v4DNS(in []net.IP) []net.IP {
	var out []net.IP
	seen := map[string]struct{}{}
	for _, ip := range in {
		v4 := ip.To4()
		if v4 == nil || v4.IsUnspecified() {
			continue
		}
		k := v4.String()
		if _, ok := seen[k]; ok {
			continue
		}
		seen[k] = struct{}{}
		out = append(out, append(net.IP(nil), v4...))
	}
	return out
}

// lookupDNS asks one DNS server for the A record (IPv4 address) of name,
// with the query travelling through the SSTP session itself.
//
// Steps:
//  1. Build an A query with a time-based id.
//  2. Connect to server:53 over TCP (Open) or UDP (DialUDP), with an
//     8-second deadline for the whole exchange.
//  3. Over TCP, DNS messages carry a 2-byte length prefix: write it with
//     the query, read the reply length (12..4096 bytes, 12 being the DNS
//     header), then the reply. Over UDP one datagram goes each way.
//  4. Pick the first IPv4 address out of the reply with dnsA.
func (n *SSTPNet) lookupDNS(server, name string, tcpMode bool) (net.IP, error) {
	id := uint16(time.Now().UnixNano())
	q := dnsQueryA(name, id)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	addr := net.JoinHostPort(server, "53")
	var (
		c   net.Conn
		err error
	)
	if tcpMode {
		c, err = n.Open(addr)
	} else {
		c, err = n.DialUDP(addr)
	}
	if err != nil {
		return nil, err
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(8 * time.Second))
	_ = ctx
	if tcpMode {
		var ln [2]byte
		binary.BigEndian.PutUint16(ln[:], uint16(len(q)))
		if _, err := c.Write(append(ln[:], q...)); err != nil {
			return nil, err
		}
		if _, err := io.ReadFull(c, ln[:]); err != nil {
			return nil, err
		}
		n := int(binary.BigEndian.Uint16(ln[:]))
		if n < 12 || n > 4096 {
			return nil, fmt.Errorf("dns len %d", n)
		}
		msg := make([]byte, n)
		if _, err := io.ReadFull(c, msg); err != nil {
			return nil, err
		}
		return dnsA(msg)
	}
	if _, err := c.Write(q); err != nil {
		return nil, err
	}
	buf := make([]byte, 2048)
	nr, err := c.Read(buf)
	if err != nil {
		return nil, err
	}
	return dnsA(buf[:nr])
}

// allocPort reserves a free local port from 49152 up (the range meant for
// temporary ports), wrapping around at the top. It returns 0 if none of
// 16384 tries found a free port.
func (n *SSTPNet) allocPort() uint16 {
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
func (n *SSTPNet) freePort(p uint16) {
	n.portMu.Lock()
	delete(n.ports, p)
	n.portMu.Unlock()
}

// hasPort reports whether port p is reserved by one of our sockets.
func (n *SSTPNet) hasPort(p uint16) bool {
	n.portMu.Lock()
	_, ok := n.ports[p]
	n.portMu.Unlock()
	return ok
}

// sstpConn is a TCP or connected UDP socket on the SSTP stack that frees
// its reserved local port when closed.
type sstpConn struct {
	net.Conn
	n    *SSTPNet
	port uint16
	once sync.Once
}

// Close closes the socket and frees its port once.
func (c *sstpConn) Close() error {
	err := c.Conn.Close()
	c.once.Do(func() { c.n.freePort(c.port) })
	return err
}

// sstpPacket is an unconnected UDP socket on the SSTP stack that frees its
// reserved local port when closed.
type sstpPacket struct {
	net.PacketConn
	n    *SSTPNet
	port uint16
	once sync.Once
}

// Close closes the socket and frees its port once.
func (c *sstpPacket) Close() error {
	err := c.PacketConn.Close()
	c.once.Do(func() { c.n.freePort(c.port) })
	return err
}

// logPkt logs the addresses of the first 12 packets in either direction.
// That is enough to see whether traffic flows without flooding the log.
func (n *SSTPNet) logPkt(dir string, ip []byte) {
	n.seen++
	if n.seen > 12 {
		return
	}
	src, dst, proto, ok := ipv4Ends(ip)
	if !ok {
		n.logf("%s n=%d head=%x", dir, len(ip), headN(ip, 16))
		return
	}
	n.logf("%s %s → %s proto=%d n=%d", dir, src, dst, proto, len(ip))
}

// asIPv4 returns b as an IPv4 packet: as is, or with a 14-byte Ethernet
// header removed. It returns nil for anything else.
func asIPv4(b []byte) []byte {
	if len(b) >= 20 && b[0]>>4 == 4 {
		return b
	}
	if len(b) >= 34 && binary.BigEndian.Uint16(b[12:14]) == 0x0800 && b[14]>>4 == 4 {
		return b[14:]
	}
	return nil
}

// headN is the first n bytes of b (or all of b), for hex dumps in logs.
func headN(b []byte, n int) []byte {
	if len(b) < n {
		return b
	}
	return b[:n]
}

// ipv4Ends reads the source, destination and protocol number of an IPv4
// packet for logging.
func ipv4Ends(ip []byte) (src, dst net.IP, proto byte, ok bool) {
	if len(ip) < 20 || ip[0]>>4 != 4 {
		return nil, nil, 0, false
	}
	return net.IP(ip[12:16]), net.IP(ip[16:20]), ip[9], true
}

// dnsQueryA builds a DNS query for the A record of name: a 12-byte header
// asking for recursion, then the name as length-prefixed labels, then type
// A, class IN. Empty or too long labels (over 63 bytes) are skipped.
func dnsQueryA(name string, id uint16) []byte {
	q := make([]byte, 12)
	binary.BigEndian.PutUint16(q[0:2], id)
	q[2] = 0x01
	binary.BigEndian.PutUint16(q[4:6], 1)
	name = strings.TrimSuffix(strings.TrimSpace(name), ".")
	for _, lab := range strings.Split(name, ".") {
		if lab == "" || len(lab) > 63 {
			continue
		}
		q = append(q, byte(len(lab)))
		q = append(q, lab...)
	}
	q = append(q, 0, 0, 1, 0, 1)
	return q
}

// dnsA returns the first IPv4 address in a DNS reply.
//
// Steps:
//  1. Check the 12-byte header and fail on any error code (rcode).
//  2. Skip the question section. Names are skipped label by label; a
//     compression pointer (two top bits set) ends a name in 2 bytes. At
//     most 16 labels are followed, so a malformed reply cannot loop.
//  3. Walk the answers. Each record has a name, then 10 fixed bytes (type,
//     class, TTL, data length), then its data. The first record of type A
//     with 4 bytes of data is the answer; others (such as CNAME) are passed.
func dnsA(msg []byte) (net.IP, error) {
	if len(msg) < 12 {
		return nil, fmt.Errorf("short dns")
	}
	if msg[3]&0x0f != 0 {
		return nil, fmt.Errorf("dns rcode %d", msg[3]&0x0f)
	}
	qd := int(binary.BigEndian.Uint16(msg[4:6]))
	an := int(binary.BigEndian.Uint16(msg[6:8]))
	off := 12
	skip := func() error {
		for hops := 0; hops < 16; hops++ {
			if off >= len(msg) {
				return fmt.Errorf("dns name")
			}
			l := int(msg[off])
			if l == 0 {
				off++
				return nil
			}
			if l&0xc0 == 0xc0 {
				off += 2
				return nil
			}
			off += 1 + l
		}
		return fmt.Errorf("dns name")
	}
	for i := 0; i < qd; i++ {
		if err := skip(); err != nil {
			return nil, err
		}
		off += 4
	}
	for i := 0; i < an; i++ {
		if err := skip(); err != nil {
			return nil, err
		}
		if off+10 > len(msg) {
			return nil, fmt.Errorf("short rr")
		}
		typ := binary.BigEndian.Uint16(msg[off : off+2])
		rdlen := int(binary.BigEndian.Uint16(msg[off+8 : off+10]))
		off += 10
		if off+rdlen > len(msg) {
			return nil, fmt.Errorf("short rdata")
		}
		if typ == 1 && rdlen == 4 {
			return net.IP(append([]byte(nil), msg[off:off+4]...)), nil
		}
		off += rdlen
	}
	return nil, fmt.Errorf("no a")
}

// ipv4DstPort reads TCP/UDP destination port from a raw IPv4 packet.
func ipv4DstPort(ip []byte) (uint16, byte, bool) {
	if len(ip) < 20 || ip[0]>>4 != 4 {
		return 0, 0, false
	}
	ihl := int(ip[0]&0xf) * 4
	if ihl < 20 || len(ip) < ihl+4 {
		return 0, 0, false
	}
	proto := ip[9]
	return binary.BigEndian.Uint16(ip[ihl+2 : ihl+4]), proto, true
}

var _ transport.Dialer = (*SSTPNet)(nil)

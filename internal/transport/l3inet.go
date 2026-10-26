package transport

// Userspace IPv4 NAT for L3 internet destinations: ICMP echo, UDP, TCP.
//
// An L3 client sends raw IP packets, but the node does not forward them as
// packets. Instead it plays the far end itself: for each TCP or UDP flow it opens an ordinary
// socket to the destination, and builds the reply packets by hand. The TCP
// part is a minimal stack: handshake, ACKs, window-limited sending and
// FIN/RST. It has no retransmission or reordering; the link between client
// and node is a reliable HTTP/2 stream.

import (
	"crypto/rand"
	"encoding/binary"
	"net"
	"strconv"
	"sync"
	"time"

	"github.com/a3app/loft/internal/egress"
)

// IP protocol numbers, TCP flag bits, and NAT limits: at most l3FlowCap
// flows of each kind (TCP, UDP) node-wide, UDP flows close after l3UDPIdle
// without a reply, and the SYN-ACK offers MSS l3MSS and window scale
// l3WScale.
const (
	ipProtoTCP = 6
	ipProtoUDP = 17
	tcpSYN     = 0x02
	tcpACK     = 0x10
	tcpPSH     = 0x08
	tcpRST     = 0x04
	tcpFIN     = 0x01
	l3FlowCap  = 128
	l3UDPIdle  = 60 * time.Second
	l3MSS      = 1240
	l3WScale   = 7
	l3TCPChunk = 1240
)

// l3Tuple identifies one NAT flow: member overlay address and port, and the
// internet address and port.
type l3Tuple struct {
	src, dst     [4]byte
	sport, dport uint16
}

// l3TCPFlow is one proxied TCP connection. iss is the node's initial
// sequence number, snd the next sequence number to send, rcv the next one
// expected from the client, una the last byte the client acknowledged, and
// cwin the client's advertised window (scaled by wshift). pend holds data
// the client sent before the outbound connection was up.
type l3TCPFlow struct {
	src, dst      net.IP
	sport, dport  uint16
	iss, snd, rcv uint32
	una, cwin     uint32
	wshift        uint8
	conn          net.Conn
	reply         func([]byte) error
	once          sync.Once
	mu            sync.Mutex
	up            bool
	pend          []byte
	ack           chan struct{}
}

// l3UDPFlow is one proxied UDP flow with its own node socket.
type l3UDPFlow struct {
	src, dst     net.IP
	sport, dport uint16
	pc           net.PacketConn
	reply        func([]byte) error
}

var (
	l3TCPMu sync.Mutex
	l3TCP   = map[l3Tuple]*l3TCPFlow{}
	l3UDPMu sync.Mutex
	l3UDP   = map[l3Tuple]*l3UDPFlow{}
)

// l3FlowKey builds the flow key; ok is false for non-IPv4 addresses.
func l3FlowKey(src, dst net.IP, sport, dport uint16) (k l3Tuple, ok bool) {
	s, d := src.To4(), dst.To4()
	if s == nil || d == nil {
		return k, false
	}
	copy(k.src[:], s)
	copy(k.dst[:], d)
	k.sport, k.dport = sport, dport
	return k, true
}

// l3Inet emits pkt from overlay src toward an internet dest, replies via reply.
func l3Inet(reply func([]byte) error, src net.IP, pkt []byte) {
	if reply == nil || src == nil || len(pkt) < 20 {
		return
	}
	ihl := int(pkt[0]&0x0f) * 4
	if ihl < 20 || ihl > len(pkt) {
		return
	}
	dst := net.IP(pkt[16:20]).To4()
	if dst == nil {
		return
	}
	switch pkt[9] {
	case ipProtoICMP:
		l3InetICMP(reply, src, dst, pkt, ihl)
	case ipProtoUDP:
		l3InetUDP(reply, src, dst, pkt, ihl)
	case ipProtoTCP:
		l3InetTCP(reply, src, dst, pkt, ihl)
	}
}

// l3InetICMP sends a real echo from the node and returns the reply as an
// IPv4 packet to src; it shares the 16-echo limit with DeliverInternetICMP.
func l3InetICMP(reply func([]byte) error, src, dst net.IP, pkt []byte, ihl int) {
	if len(pkt) < ihl+8 || pkt[ihl] != icmpEcho {
		return
	}
	echo := append([]byte(nil), pkt[ihl:]...)
	go func() {
		select {
		case internetICMPSem <- struct{}{}:
			defer func() { <-internetICMPSem }()
		default:
			return
		}
		body, from, err := internetICMPPing(dst, echo)
		body = restoreICMPIdent(stripIPv4(body), echo)
		if err != nil || len(body) < 8 || body[0] != icmpReply {
			return
		}
		if from == nil {
			from = dst
		}
		out := encapsulateIPv4(from, src, ipProtoICMP, body)
		if len(out) > 0 {
			_ = reply(out)
		}
	}()
}

// l3InetUDP sends the UDP payload from the flow's node socket, opening the
// socket (and its reader) on the first packet of a flow.
func l3InetUDP(reply func([]byte) error, src, dst net.IP, pkt []byte, ihl int) {
	if len(pkt) < ihl+8 {
		return
	}
	sport := binary.BigEndian.Uint16(pkt[ihl : ihl+2])
	dport := binary.BigEndian.Uint16(pkt[ihl+2 : ihl+4])
	payload := pkt[ihl+8:]
	if spliceIPDenied(dst) { // same drop as a mux Open
		return
	}
	key, ok := l3FlowKey(src, dst, sport, dport)
	if !ok {
		return
	}
	l3UDPMu.Lock()
	f := l3UDP[key]
	if f == nil {
		if len(l3UDP) >= l3FlowCap {
			l3UDPMu.Unlock()
			return
		}
		pc, err := net.ListenPacket("udp4", "0.0.0.0:0")
		if err != nil {
			l3UDPMu.Unlock()
			return
		}
		f = &l3UDPFlow{src: src, dst: dst, sport: sport, dport: dport, pc: pc, reply: reply}
		l3UDP[key] = f
		go l3UDPRead(key, f)
	}
	l3UDPMu.Unlock()
	_ = f.pc.SetWriteDeadline(time.Now().Add(3 * time.Second))
	_, _ = f.pc.WriteTo(payload, &net.UDPAddr{IP: dst, Port: int(dport)})
}

// l3UDPRead wraps each reply from the internet into a UDP/IPv4 packet back
// to the member, and removes the flow after l3UDPIdle of silence.
func l3UDPRead(key l3Tuple, f *l3UDPFlow) {
	defer func() {
		_ = f.pc.Close()
		l3UDPMu.Lock()
		delete(l3UDP, key)
		l3UDPMu.Unlock()
	}()
	buf := make([]byte, 2048)
	for {
		_ = f.pc.SetReadDeadline(time.Now().Add(l3UDPIdle))
		n, _, err := f.pc.ReadFrom(buf)
		if err != nil {
			return
		}
		out := encapsulateIPv4UDP(f.dst, f.src, f.dport, f.sport, buf[:n])
		if len(out) > 0 {
			_ = f.reply(out)
		}
	}
}

// encapsulateIPv4UDP builds an IPv4 UDP packet with a valid checksum.
func encapsulateIPv4UDP(src, dst net.IP, sport, dport uint16, payload []byte) []byte {
	udp := make([]byte, 8+len(payload))
	binary.BigEndian.PutUint16(udp[0:2], sport)
	binary.BigEndian.PutUint16(udp[2:4], dport)
	binary.BigEndian.PutUint16(udp[4:6], uint16(len(udp)))
	copy(udp[8:], payload)
	pkt := encapsulateIPv4(src, dst, ipProtoUDP, udp)
	if len(pkt) < 28 {
		return pkt
	}
	ihl := 20
	sum := transportChecksum(src, dst, ipProtoUDP, pkt[ihl:])
	binary.BigEndian.PutUint16(pkt[ihl+6:ihl+8], sum)
	return pkt
}

// l3InetTCP handles one TCP segment from the member. A bare SYN starts a
// flow and an outbound dial; RST ends the flow; ACK updates the send window;
// FIN half-closes the outbound connection; payload is written to it (or
// buffered until the dial completes) and acknowledged at once. Segments are
// taken in arrival order; sequence numbers are not checked.
func l3InetTCP(reply func([]byte) error, src, dst net.IP, pkt []byte, ihl int) {
	if len(pkt) < ihl+20 {
		return
	}
	th := pkt[ihl:]
	sport := binary.BigEndian.Uint16(th[0:2])
	dport := binary.BigEndian.Uint16(th[2:4])
	seq := binary.BigEndian.Uint32(th[4:8])
	dataOff := int(th[12]>>4) * 4
	if dataOff < 20 || dataOff > len(th) {
		return
	}
	flags := th[13]
	payload := th[dataOff:]
	key, ok := l3FlowKey(src, dst, sport, dport)
	if !ok {
		return
	}
	l3TCPMu.Lock()
	f := l3TCP[key]
	if flags&tcpRST != 0 {
		if f != nil {
			f.close()
			delete(l3TCP, key)
		}
		l3TCPMu.Unlock()
		return
	}
	if f == nil {
		if flags&tcpSYN == 0 || flags&tcpACK != 0 || len(l3TCP) >= l3FlowCap {
			l3TCPMu.Unlock()
			return
		}
		var iss [4]byte
		_, _ = rand.Read(iss[:])
		win := binary.BigEndian.Uint16(th[14:16])
		shift := tcpWScale(th, dataOff)
		cwin := uint32(win) << shift
		if cwin == 0 {
			cwin = 65535
		}
		f = &l3TCPFlow{
			src: src, dst: dst, sport: sport, dport: dport,
			iss: binary.BigEndian.Uint32(iss[:]), rcv: seq + 1, reply: reply,
			wshift: shift, cwin: cwin, ack: make(chan struct{}, 1),
		}
		f.snd = f.iss
		f.una = f.iss
		l3TCP[key] = f
		l3TCPMu.Unlock()
		go l3TCPDial(key, f)
		return
	}
	l3TCPMu.Unlock()
	if flags&tcpACK != 0 {
		ackn := binary.BigEndian.Uint32(th[8:12])
		win := binary.BigEndian.Uint16(th[14:16])
		f.mu.Lock()
		f.una = ackn
		f.cwin = uint32(win) << f.wshift
		select {
		case f.ack <- struct{}{}:
		default:
		}
		f.mu.Unlock()
	}
	if flags&tcpFIN != 0 {
		f.mu.Lock()
		f.rcv++
		c := f.conn
		rep := f.reply
		f.mu.Unlock()
		if cw, ok := c.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite()
		}
		_ = rep(l3TCPPacket(f, tcpACK, nil))
		return
	}
	f.mu.Lock()
	if !f.up && flags&tcpACK != 0 {
		f.up = true
	}
	if len(payload) > 0 {
		f.rcv += uint32(len(payload))
		if f.conn != nil {
			_, _ = f.conn.Write(payload)
		} else {
			f.pend = append(f.pend, payload...)
		}
		rep := f.reply
		f.mu.Unlock()
		_ = rep(l3TCPPacket(f, tcpACK, nil))
		return
	}
	f.mu.Unlock()
}

// l3TCPDial connects to the destination and only then answers the SYN, so
// a refused dial reaches the member as RST, like a real host would. It then
// copies data from the internet into TCP segments, never sending more than
// the member's window allows; when the window is full it waits for an ACK
// (or 50 ms) and tries again. The outbound dial uses the default exit rule
// (no member identity).
func l3TCPDial(key l3Tuple, f *l3TCPFlow) {
	c, err := dialOut(egress.Who{}, net.JoinHostPort(f.dst.String(), strconv.Itoa(int(f.dport))))
	if err != nil {
		_ = f.reply(l3TCPPacket(f, tcpRST|tcpACK, nil))
		l3TCPMu.Lock()
		delete(l3TCP, key)
		l3TCPMu.Unlock()
		return
	}
	f.mu.Lock()
	f.conn = c
	pend := f.pend
	f.pend = nil
	f.mu.Unlock()
	_ = f.reply(l3TCPPacket(f, tcpSYN|tcpACK, nil))
	f.mu.Lock()
	f.snd = f.iss + 1
	if len(pend) > 0 {
		_, _ = c.Write(pend)
	}
	f.mu.Unlock()
	buf := make([]byte, l3TCPChunk)
	for {
		n, err := c.Read(buf)
		if n > 0 {
			off := 0
			for off < n {
				f.mu.Lock()
				inFlight := f.snd - f.una
				room := int(f.cwin)
				if room > int(inFlight) {
					room -= int(inFlight)
				} else {
					room = 0
				}
				ack := f.ack
				f.mu.Unlock()
				if room < 1 {
					select {
					case <-ack:
					case <-time.After(50 * time.Millisecond):
					}
					continue
				}
				if room > n-off {
					room = n - off
				}
				chunk := append([]byte(nil), buf[off:off+room]...)
				f.mu.Lock()
				pkt := l3TCPPacket(f, tcpPSH|tcpACK, chunk)
				f.snd += uint32(len(chunk))
				rep := f.reply
				f.mu.Unlock()
				_ = rep(pkt)
				off += len(chunk)
			}
		}
		if err != nil {
			_ = f.reply(l3TCPPacket(f, tcpFIN|tcpACK, nil))
			f.close()
			l3TCPMu.Lock()
			delete(l3TCP, key)
			l3TCPMu.Unlock()
			return
		}
	}
}

// close closes the outbound connection once.
func (f *l3TCPFlow) close() {
	f.once.Do(func() {
		if f.conn != nil {
			_ = f.conn.Close()
		}
	})
}

// l3TCPPacket builds one TCP/IPv4 segment from the destination to the
// member, with the flow's current sequence and ack numbers. A SYN carries
// the MSS and window scale options.
func l3TCPPacket(f *l3TCPFlow, flags byte, payload []byte) []byte {
	hdr := 20
	var opts []byte
	if flags&tcpSYN != 0 {
		opts = []byte{2, 4, byte(l3MSS >> 8), byte(l3MSS & 0xff), 1, 3, 3, l3WScale}
		hdr = 20 + len(opts)
	}
	th := make([]byte, hdr+len(payload))
	binary.BigEndian.PutUint16(th[0:2], f.dport)
	binary.BigEndian.PutUint16(th[2:4], f.sport)
	seq := f.snd
	if flags&tcpSYN != 0 {
		seq = f.iss
	}
	binary.BigEndian.PutUint32(th[4:8], seq)
	binary.BigEndian.PutUint32(th[8:12], f.rcv)
	th[12] = byte(hdr/4) << 4
	th[13] = flags
	binary.BigEndian.PutUint16(th[14:16], 65535)
	copy(th[20:], opts)
	copy(th[hdr:], payload)
	pkt := encapsulateIPv4(f.dst, f.src, ipProtoTCP, th)
	if len(pkt) < 20+hdr {
		return pkt
	}
	sum := transportChecksum(f.dst, f.src, ipProtoTCP, pkt[20:])
	binary.BigEndian.PutUint16(pkt[20+16:20+18], sum)
	return pkt
}

// tcpWScale is RFC 7323 shift from SYN options (0 if absent). Cap 14.
func tcpWScale(th []byte, dataOff int) uint8 {
	if dataOff < 20 || dataOff > len(th) {
		return 0
	}
	opts := th[20:dataOff]
	for i := 0; i < len(opts); {
		switch opts[i] {
		case 0:
			return 0
		case 1:
			i++
		default:
			if i+1 >= len(opts) {
				return 0
			}
			n := int(opts[i+1])
			if n < 2 || i+n > len(opts) {
				return 0
			}
			if opts[i] == 3 && n == 3 {
				sh := opts[i+2]
				if sh > 14 {
					return 14
				}
				return sh
			}
			i += n
		}
	}
	return 0
}

// transportChecksum is the TCP/UDP checksum over the IPv4 pseudo-header
// (addresses, protocol, length) and the segment.
func transportChecksum(src, dst net.IP, proto byte, payload []byte) uint16 {
	s4, d4 := src.To4(), dst.To4()
	if s4 == nil || d4 == nil {
		return 0
	}
	var sum uint32
	sum += uint32(s4[0])<<8 | uint32(s4[1])
	sum += uint32(s4[2])<<8 | uint32(s4[3])
	sum += uint32(d4[0])<<8 | uint32(d4[1])
	sum += uint32(d4[2])<<8 | uint32(d4[3])
	sum += uint32(proto)
	sum += uint32(len(payload))
	for i := 0; i+1 < len(payload); i += 2 {
		sum += uint32(payload[i])<<8 | uint32(payload[i+1])
	}
	if len(payload)%2 == 1 {
		sum += uint32(payload[len(payload)-1]) << 8
	}
	for sum > 0xffff {
		sum = (sum >> 16) + (sum & 0xffff)
	}
	return ^uint16(sum)
}

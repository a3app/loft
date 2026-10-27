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
	"sync/atomic"
	"time"

	"github.com/a3app/loft/internal/egress"
)

// IP protocol numbers, TCP flag bits, and NAT limits: at most l3FlowCap
// flows of each kind (TCP, UDP) node-wide, UDP flows close after l3UDPIdle
// without a reply, and the SYN-ACK offers MSS l3MSS and window scale
// l3WScale. l3RcvWin is the receive window every segment to the member
// advertises, and l3OOOMax caps the bytes kept per flow that arrived after
// a missing segment (see l3InetTCP).
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
	// l3RcvWin is 1 MiB: about 40 Mbit/s per flow at 200 ms. A larger
	// window let the member's TCP put megabytes in flight at once, more
	// than the tunnel drains, so its local queue (the gVisor link queue in
	// L3Net, the TUN queue in the OS) dropped packets.
	l3RcvWin = 1 << 20
	l3OOOMax = 1 << 20
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
// the client sent before the outbound connection was up. ooo holds segments
// that arrived after a missing one, by sequence number, oooN their bytes.
type l3TCPFlow struct {
	who           egress.Who // the member, so its exit rules apply
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
	ooo           map[uint32][]byte
	oooN          int
}

// l3UDPFlow is one proxied UDP flow. With exit rules configured it goes out
// through egress (eu) like a mux datagram of the same member; without them
// it has its own node socket (pc).
type l3UDPFlow struct {
	src, dst     net.IP
	sport, dport uint16
	pc           net.PacketConn
	eu           *egress.UDP
	last         atomic.Int64 // unix nanoseconds of the last packet either way
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
// who is the member behind src: TCP and UDP leave by that member's exit
// rules, the same as its mux streams and datagrams.
func l3Inet(reply func([]byte) error, who egress.Who, src net.IP, pkt []byte) {
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
		if icmpOut(who, dst) {
			l3InetICMP(reply, src, dst, pkt, ihl)
		}
	case ipProtoUDP:
		l3InetUDP(reply, who, src, dst, pkt, ihl)
	case ipProtoTCP:
		l3InetTCP(reply, who, src, dst, pkt, ihl)
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

// l3InetUDP sends the UDP payload of one flow, opening the flow on its
// first packet: through egress when exit rules are configured, else from a
// node socket with its own reader.
func l3InetUDP(reply func([]byte) error, who egress.Who, src, dst net.IP, pkt []byte, ihl int) {
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
		f = &l3UDPFlow{src: src, dst: dst, sport: sport, dport: dport, reply: reply}
		f.last.Store(time.Now().UnixNano())
		if r := Egress.Load(); r != nil {
			f.eu = r.NewUDP(who, func(_ string, b []byte) {
				f.last.Store(time.Now().UnixNano())
				if out := encapsulateIPv4UDP(f.dst, f.src, f.dport, f.sport, b); len(out) > 0 {
					_ = f.reply(out)
				}
			})
			l3UDP[key] = f
			go l3UDPIdleClose(key, f)
		} else {
			pc, err := net.ListenPacket("udp4", "0.0.0.0:0")
			if err != nil {
				l3UDPMu.Unlock()
				return
			}
			f.pc = pc
			l3UDP[key] = f
			go l3UDPRead(key, f)
		}
	}
	l3UDPMu.Unlock()
	f.last.Store(time.Now().UnixNano())
	if f.eu != nil {
		f.eu.Send(net.JoinHostPort(dst.String(), strconv.Itoa(int(dport))), payload)
		return
	}
	_ = f.pc.SetWriteDeadline(time.Now().Add(3 * time.Second))
	_, _ = f.pc.WriteTo(payload, &net.UDPAddr{IP: dst, Port: int(dport)})
}

// l3UDPIdleClose ends an egress UDP flow after l3UDPIdle with no packet in
// either direction.
func l3UDPIdleClose(key l3Tuple, f *l3UDPFlow) {
	t := time.NewTicker(l3UDPIdle / 4)
	defer t.Stop()
	for range t.C {
		if time.Since(time.Unix(0, f.last.Load())) > l3UDPIdle {
			break
		}
	}
	f.eu.Close()
	l3UDPMu.Lock()
	delete(l3UDP, key)
	l3UDPMu.Unlock()
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
// payload is written to the outbound connection (or buffered until the dial
// completes) and acknowledged at once; FIN, after the payload of the same
// segment, half-closes the outbound connection.
//
// Bytes are written to the outbound connection strictly in order. The link
// to the member is a reliable stream, but packets can still go missing
// before it (a full queue on the member's side), and the member's TCP may
// resend a segment whose ACK was slow (common on mobile links). Writing a
// resent byte again would duplicate it in the outbound stream (inside TLS
// that is a fatal "bad record MAC"). So a resent segment is only
// acknowledged again, and a segment from beyond a gap is kept (up to
// l3OOOMax per flow) until the missing one arrives; then everything that
// now lines up is written and acknowledged at once. Without that keeping,
// one lost packet cost the member a resend of its whole window, which
// stalled uploads. A new SYN for a flow that is still known starts a fresh
// flow: the member reused the port for a new connection.
func l3InetTCP(reply func([]byte) error, who egress.Who, src, dst net.IP, pkt []byte, ihl int) {
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
	if f != nil && flags&tcpSYN != 0 && flags&tcpACK == 0 && seq+1 != f.rcv {
		// Not a resend of this flow's SYN (that has the same seq): a new
		// connection on the same addresses and ports.
		f.close()
		delete(l3TCP, key)
		f = nil
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
			who: who, src: src, dst: dst, sport: sport, dport: dport,
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
	f.mu.Lock()
	if !f.up && flags&tcpACK != 0 {
		f.up = true
	}
	if ahead := int32(seq - f.rcv); ahead > 0 && len(payload) > 0 {
		// Beyond a gap: keep it for later and repeat the ACK of what we
		// have, which tells the member which segment is missing.
		if _, dup := f.ooo[seq]; !dup && f.oooN+len(payload) <= l3OOOMax {
			if f.ooo == nil {
				f.ooo = map[uint32][]byte{}
			}
			f.ooo[seq] = append([]byte(nil), payload...)
			f.oooN += len(payload)
		}
		rep := f.reply
		f.mu.Unlock()
		_ = rep(f.segment(tcpACK, nil))
		return
	}
	payload, inOrder := l3TCPTrim(seq, f.rcv, payload)
	fin := flags&tcpFIN != 0 && inOrder
	if len(payload) > 0 && len(f.ooo) > 0 && !fin {
		payload = f.drainOOO(append([]byte(nil), payload...))
	}
	if len(payload) == 0 && !fin {
		resend := len(th[dataOff:]) > 0 || flags&tcpFIN != 0
		rep := f.reply
		f.mu.Unlock()
		if resend {
			_ = rep(f.segment(tcpACK, nil)) // duplicate or beyond a gap: ACK what we have
		}
		return
	}
	if len(payload) > 0 {
		f.rcv += uint32(len(payload))
		if f.conn != nil {
			_, _ = f.conn.Write(payload)
		} else {
			f.pend = append(f.pend, payload...)
		}
	}
	var c net.Conn
	if fin {
		f.rcv++
		c = f.conn
		f.ooo, f.oooN = nil, 0 // nothing may follow a FIN
	}
	rep := f.reply
	f.mu.Unlock()
	if c != nil {
		if cw, ok := c.(interface{ CloseWrite() error }); ok {
			_ = cw.CloseWrite()
		}
	}
	_ = rep(f.segment(tcpACK, nil))
}

// drainOOO appends to in (the bytes that arrive in order at f.rcv) the kept
// segments that now line up behind them, and forgets those and any that
// lie wholly before the new end. f.mu must be held.
func (f *l3TCPFlow) drainOOO(in []byte) []byte {
	end := f.rcv + uint32(len(in))
	for {
		moved := false
		for seq, b := range f.ooo {
			if int32(seq-end) > 0 {
				continue // still beyond a gap
			}
			delete(f.ooo, seq)
			f.oooN -= len(b)
			if rest, _ := l3TCPTrim(seq, end, b); len(rest) > 0 {
				in = append(in, rest...)
				end += uint32(len(rest))
			}
			moved = true
		}
		if !moved {
			return in
		}
	}
}

// l3TCPTrim returns the part of payload (sent at seq) that starts at the
// expected sequence number rcv, and whether the segment's end lines up with
// rcv after that (so a FIN in it is in order). Bytes before rcv were
// received already; a segment starting after rcv is beyond a gap and gives
// nothing. Sequence numbers wrap, so the comparison is on the difference.
func l3TCPTrim(seq, rcv uint32, payload []byte) ([]byte, bool) {
	ahead := int32(seq - rcv)
	if ahead > 0 {
		return nil, false
	}
	skip := uint32(-ahead)
	if skip >= uint32(len(payload)) {
		return nil, skip == uint32(len(payload))
	}
	return payload[skip:], true
}

// l3TCPDial connects to the destination and only then answers the SYN, so
// a refused dial reaches the member as RST, like a real host would. It then
// copies data from the internet into TCP segments, never sending more than
// the member's window allows; when the window is full it waits for an ACK
// (or 50 ms) and tries again. The outbound dial follows the member's exit
// rules, like its mux streams.
func l3TCPDial(key l3Tuple, f *l3TCPFlow) {
	c, err := dialOut(f.who, net.JoinHostPort(f.dst.String(), strconv.Itoa(int(f.dport))))
	if err != nil {
		_ = f.reply(f.segment(tcpRST|tcpACK, nil))
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
	_ = f.reply(f.segment(tcpSYN|tcpACK, nil))
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
			_ = f.reply(f.segment(tcpFIN|tcpACK, nil))
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

// segment copies snd/rcv under f.mu. reply stays outside the lock:
// it can re-enter l3InetTCP on this flow.
func (f *l3TCPFlow) segment(flags byte, payload []byte) []byte {
	f.mu.Lock()
	defer f.mu.Unlock()
	return l3TCPPacket(f, flags, payload)
}

// l3TCPPacket builds one TCP/IPv4 segment from the destination to the
// member, with the flow's current sequence and ack numbers. A SYN carries
// the MSS and window scale options. It reads sport, dport, iss, snd, rcv,
// src, dst; the caller holds f.mu.
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
	binary.BigEndian.PutUint16(th[14:16], l3RcvWin>>l3WScale)
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

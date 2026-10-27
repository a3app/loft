package transport

// L3 shaping (experimental, branch experimentos): make the l3 front harder
// to spot with "TLS inside TLS" detection.
//
// The problem. On the l3 front the client's own TCP packets ride inside the
// tunnel almost one by one. When an app opens an HTTPS site, its TLS
// handshake (a ClientHello of a typical size, a wait of one round trip, a
// server answer of a typical size, another wait) shows through as the sizes
// and timing of the tunnel's encrypted messages. Xue et al. (USENIX
// Security 2024) built a classifier on exactly that shape and ran it at an
// ISP; padding and multiplexing alone did not stop it.
//
// What shaping does, on both sides of an l3 session once CapL3Shape is
// agreed:
//
//  1. Cork the start of each TCP flow. The SYN and the first l3ShapeFirst
//     data segments of a flow (in the direction being sent) no longer flush
//     at once: they wait up to l3ShapeWait so they share one message with
//     whatever else is going, and that message is padded up to a size
//     bucket (l3ShapeBucket). The ClientHello then no longer travels alone
//     in a message of its own typical size.
//  2. Answer at once. When the first data segment of a flow arrives from
//     the peer, this side immediately sends a padding-only message back.
//     The observer sees an answer one tunnel round trip after the
//     ClientHello, not one round trip to the far site, so the wait that
//     marks a nested handshake is blurred.
//
// Costs: up to l3ShapeWait more on each new connection, and some padding
// bytes on the first packets of each flow. Bulk transfer after the start
// is not padded. Whether this is enough against the published classifier
// is not measured yet; it is an experiment to measure.

import (
	"crypto/rand"
	"encoding/binary"
	"sync"
	"sync/atomic"
	"time"
)

// Shaping limits: the first data segments of a flow that count as its
// start, how long they may wait in the cork, the smallest padded size, the
// size range of padding-only answers, and the flow table bounds.
const (
	l3ShapeFirst    = 4
	l3ShapeWait     = 12 * time.Millisecond
	l3ShapeMin      = 512
	l3ChaffMin      = 1024
	l3ChaffMax      = 4096
	l3FlowMax       = 4096
	l3FlowIdle      = 2 * time.Minute
	l3FlowSweepEach = 256
)

// l3ShapeAsk is the client's wish, set from client.yaml (l3_shape: true) or
// the loft-front -l3-shape flag. The node shapes when the Hello asks.
var l3ShapeAsk atomic.Bool

// SetL3Shape turns the l3 shaping ask on or off for new l3 sessions.
func SetL3Shape(on bool) { l3ShapeAsk.Store(on) }

// Shaping counters for this process, over all l3 sessions: IP packet bytes
// sent, padding bytes added to them, padding-only answers sent and their
// bytes, and messages that went out padded. loft-bench reads them to show
// what shaping costs.
var (
	l3StatPayload atomic.Uint64
	l3StatPad     atomic.Uint64
	l3StatChaffN  atomic.Uint64
	l3StatChaff   atomic.Uint64
	l3StatPaddedN atomic.Uint64
)

// L3ShapeStats is a snapshot of the shaping counters.
type L3ShapeStats struct {
	Payload, Pad, ChaffN, Chaff, PaddedN uint64
}

// L3ShapeCounters returns the shaping counters so far.
func L3ShapeCounters() L3ShapeStats {
	return L3ShapeStats{
		Payload: l3StatPayload.Load(),
		Pad:     l3StatPad.Load(),
		ChaffN:  l3StatChaffN.Load(),
		Chaff:   l3StatChaff.Load(),
		PaddedN: l3StatPaddedN.Load(),
	}
}

// Sub is the change from an earlier snapshot b to s.
func (s L3ShapeStats) Sub(b L3ShapeStats) L3ShapeStats {
	return L3ShapeStats{s.Payload - b.Payload, s.Pad - b.Pad, s.ChaffN - b.ChaffN, s.Chaff - b.Chaff, s.PaddedN - b.PaddedN}
}

// l3ShapeBucket is the padded plaintext size for a message of n bytes: the
// next power of two from l3ShapeMin, so sizes fall into a few steps instead
// of revealing the packets inside. Above l3Cork nothing is added.
func l3ShapeBucket(n int) int {
	if n >= l3Cork {
		return n
	}
	b := l3ShapeMin
	for b < n {
		b <<= 1
	}
	if b > l3Cork {
		b = l3Cork
	}
	return b
}

// l3ChaffSize picks a random size for a padding-only answer.
func l3ChaffSize() int {
	var r [2]byte
	_, _ = rand.Read(r[:])
	return l3ChaffMin + int(binary.BigEndian.Uint16(r[:]))%(l3ChaffMax-l3ChaffMin+1)
}

// l3ShapeKey names one TCP flow regardless of direction: the two endpoints
// (address and port) in a fixed order, so a packet and its reply map to the
// same key. IPv4 addresses are stored in the IPv6 form.
type l3ShapeKey struct {
	a, b [18]byte
}

// l3ShapeFlow counts the data segments seen each way, so shaping knows when a
// flow's start is over, and when it was last seen, for cleanup.
type l3ShapeFlow struct {
	sent, recv int
	seen       time.Time
}

// l3ShapeFlows is the per-session flow table. It only tracks TCP; UDP and ICMP
// are not shaped.
type l3ShapeFlows struct {
	mu    sync.Mutex
	m     map[l3ShapeKey]*l3ShapeFlow
	count int
}

// newL3ShapeFlows returns an empty flow table.
func newL3ShapeFlows() *l3ShapeFlows { return &l3ShapeFlows{m: map[l3ShapeKey]*l3ShapeFlow{}} }

// l3TCPInfo pulls the flow key, the TCP flags and whether the segment
// carries data out of one IPv4 or IPv6 packet. ok is false for anything
// that is not TCP (or is cut short).
func l3TCPInfo(p []byte) (key l3ShapeKey, flags byte, data bool, ok bool) {
	if len(p) < 20 {
		return key, 0, false, false
	}
	var src, dst []byte
	var tcp []byte
	switch p[0] >> 4 {
	case 4:
		ihl := int(p[0]&0x0f) * 4
		if ihl < 20 || len(p) < ihl+20 || p[9] != ipProtoTCP {
			return key, 0, false, false
		}
		total := int(binary.BigEndian.Uint16(p[2:4]))
		if total > len(p) || total < ihl {
			total = len(p)
		}
		src, dst, tcp = p[12:16], p[16:20], p[ihl:total]
	case 6:
		// Extension headers are rare on this path; such packets are just
		// not shaped.
		if len(p) < 60 || p[6] != ipProtoTCP {
			return key, 0, false, false
		}
		src, dst, tcp = p[8:24], p[24:40], p[40:]
	default:
		return key, 0, false, false
	}
	if len(tcp) < 20 {
		return key, 0, false, false
	}
	off := int(tcp[12]>>4) * 4
	if off < 20 || off > len(tcp) {
		return key, 0, false, false
	}
	var ea, eb [18]byte
	putEnd(&ea, src, tcp[0:2])
	putEnd(&eb, dst, tcp[2:4])
	if string(ea[:]) > string(eb[:]) {
		ea, eb = eb, ea
	}
	return l3ShapeKey{a: ea, b: eb}, tcp[13], len(tcp) > off, true
}

// putEnd writes an address (4 or 16 bytes, IPv4 mapped into IPv6) and a
// port into e.
func putEnd(e *[18]byte, ip, port []byte) {
	if len(ip) == 4 {
		e[10], e[11] = 0xff, 0xff
		copy(e[12:16], ip)
	} else {
		copy(e[:16], ip)
	}
	copy(e[16:18], port)
}

// noteSend records a packet this side is about to send and reports whether
// it belongs to the start of its flow (a SYN, or one of the first
// l3ShapeFirst data segments sent on the flow).
func (f *l3ShapeFlows) noteSend(p []byte) bool {
	key, flags, data, ok := l3TCPInfo(p)
	if !ok {
		return false
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	fl := f.getLocked(key, flags)
	if fl == nil {
		return false
	}
	if flags&tcpSYN != 0 {
		return true
	}
	if !data {
		return false
	}
	fl.sent++
	return fl.sent <= l3ShapeFirst
}

// noteRecv records a packet that came from the peer and reports whether it
// is the first data segment of its flow in that direction: the moment to
// send a padding-only answer.
func (f *l3ShapeFlows) noteRecv(p []byte) bool {
	key, flags, data, ok := l3TCPInfo(p)
	if !ok {
		return false
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	// A SYN from the peer starts tracking too (on the node, the client's
	// SYN arrives this way).
	fl := f.getLocked(key, flags)
	if fl == nil || !data {
		return false
	}
	fl.recv++
	return fl.recv == 1
}

// getLocked returns the flow for key, creating it on a SYN, and drops it on
// FIN or RST (the caller still gets this last view). Flows first seen in
// the middle (no SYN) are not tracked: their start is already over. Every
// l3FlowSweepEach calls, idle flows are cleared; a full table takes no new
// flows. f.mu must be held.
func (f *l3ShapeFlows) getLocked(key l3ShapeKey, flags byte) *l3ShapeFlow {
	now := time.Now()
	f.count++
	if f.count%l3FlowSweepEach == 0 {
		for k, v := range f.m {
			if now.Sub(v.seen) > l3FlowIdle {
				delete(f.m, k)
			}
		}
	}
	fl := f.m[key]
	if fl == nil {
		if flags&tcpSYN == 0 || len(f.m) >= l3FlowMax {
			return nil
		}
		fl = &l3ShapeFlow{}
		f.m[key] = fl
	}
	fl.seen = now
	if flags&(tcpFIN|tcpRST) != 0 {
		delete(f.m, key)
	}
	return fl
}

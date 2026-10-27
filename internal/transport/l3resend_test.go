package transport

import (
	"encoding/binary"
	"net"
	"sync"
	"testing"
	"time"

	"github.com/a3app/loft/internal/egress"
)

// lossyLink is the member side of one l3 flow in a test: it records every
// segment the node sends and can drop chosen ones, as a full queue on the
// member's side would.
type lossyLink struct {
	mu   sync.Mutex
	got  chan []byte
	drop func(flags byte, seq uint32, payload []byte) bool
}

// reply is the node's way to send to the member.
func (l *lossyLink) reply(p []byte) error {
	ihl := int(p[0]&0x0f) * 4
	th := p[ihl:]
	off := int(th[12]>>4) * 4
	l.mu.Lock()
	drop := l.drop != nil && l.drop(th[13], binary.BigEndian.Uint32(th[4:8]), th[off:])
	l.mu.Unlock()
	if !drop {
		l.got <- append([]byte(nil), p...)
	}
	return nil
}

// next waits up to d for a segment matching want and returns its flags, seq
// and payload; ok is false on timeout.
func (l *lossyLink) next(d time.Duration, want func(flags byte, payload []byte) bool) (byte, uint32, []byte, bool) {
	end := time.After(d)
	for {
		select {
		case p := <-l.got:
			th := p[int(p[0]&0x0f)*4:]
			off := int(th[12]>>4) * 4
			if want(th[13], th[off:]) {
				return th[13], binary.BigEndian.Uint32(th[4:8]), th[off:], true
			}
		case <-end:
			return 0, 0, nil, false
		}
	}
}

// dropFirst returns a drop rule that loses the first segment matching
// match, once. It is set before the flow opens: the node sends data right
// after its SYN-ACK, so a rule set later could miss it.
func dropFirst(match func(flags byte, payload []byte) bool) func(byte, uint32, []byte) bool {
	var once sync.Once
	return func(f byte, _ uint32, p []byte) bool {
		hit := false
		if match(f, p) {
			once.Do(func() { hit = true })
		}
		return hit
	}
}

// openLossy opens an l3 flow from src:sport to a test listener that sends
// body and then closes; drop (may be nil) decides which node segments get
// lost. It returns the link, the member's next sequence number, and the
// node's first data sequence number (what the member acknowledges after
// the handshake).
func openLossy(t *testing.T, src net.IP, sport uint16, body []byte, drop func(byte, uint32, []byte) bool) (*lossyLink, *net.TCPAddr, uint32, uint32) {
	t.Helper()
	ln, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		_, _ = c.Write(body)
		time.Sleep(100 * time.Millisecond)
		c.Close()
	}()
	addr := ln.Addr().(*net.TCPAddr)
	l := &lossyLink{got: make(chan []byte, 256), drop: drop}
	l3Inet(l.reply, egress.Who{}, src, l3TCPSyn(src, addr.IP, sport, uint16(addr.Port), 1000))
	_, iss, _, ok := l.next(2*time.Second, func(f byte, _ []byte) bool { return f&tcpSYN != 0 })
	if !ok {
		t.Fatal("no SYN-ACK")
	}
	return l, addr, 1001, iss + 1
}

// ack sends the member's pure ACK of everything before ackn.
func (l *lossyLink) ack(src net.IP, addr *net.TCPAddr, sport uint16, seq, ackn uint32) {
	l3Inet(l.reply, egress.Who{}, src, l3TCPAck(src, addr.IP, sport, uint16(addr.Port), seq, ackn))
}

// TestL3ResendOnTimeout: the first data segment is lost; with no ACK for
// it, the node sends it again after the retransmission timeout.
func TestL3ResendOnTimeout(t *testing.T) {
	src := net.IPv4(10, 8, 1, 1)
	t0 := time.Now()
	l, addr, seq, start := openLossy(t, src, 41000, []byte("lost-then-resent"),
		dropFirst(func(f byte, p []byte) bool { return len(p) > 0 }))
	l.ack(src, addr, 41000, seq, start) // finish the handshake, ack no data
	_, s, p, ok := l.next(3*time.Second, func(f byte, p []byte) bool { return len(p) > 0 })
	if !ok {
		t.Fatal("no resend")
	}
	if s != start || string(p) != "lost-then-resent" {
		t.Fatalf("resent seq %d %q", s-start, p)
	}
	if took := time.Since(t0); took < l3MinRTO/2 {
		t.Fatalf("resent after %v, before the timeout", took)
	}
}

// TestL3FastRetransmit: three duplicate ACKs make the node resend the
// missing segment at once, well before the timeout.
func TestL3FastRetransmit(t *testing.T) {
	src := net.IPv4(10, 8, 1, 2)
	body := make([]byte, 3*l3TCPChunk)
	for i := range body {
		body[i] = byte('a' + i%26)
	}
	l, addr, seq, start := openLossy(t, src, 41001, body,
		dropFirst(func(f byte, p []byte) bool { return len(p) > 0 }))
	l.ack(src, addr, 41001, seq, start)
	// The two later segments arrive; the member keeps acking start.
	for i := 0; i < 2; i++ {
		if _, _, _, ok := l.next(2*time.Second, func(f byte, p []byte) bool { return len(p) > 0 }); !ok {
			t.Fatal("later segments missing")
		}
	}
	t0 := time.Now()
	for i := 0; i < l3DupAcks; i++ {
		l.ack(src, addr, 41001, seq, start)
	}
	_, s, p, ok := l.next(l3MinRTO/2, func(f byte, p []byte) bool { return len(p) > 0 })
	if !ok || s != start || len(p) != l3TCPChunk {
		t.Fatalf("no fast retransmit (ok=%v seq=%d len=%d)", ok, s-start, len(p))
	}
	if time.Since(t0) >= l3MinRTO/2 {
		t.Fatal("resent by the timer, not by duplicate ACKs")
	}
}

// TestL3FinResentUntilAcked: a lost FIN is sent again, and the flow stays
// in the table until the member acknowledges it.
func TestL3FinResentUntilAcked(t *testing.T) {
	src := net.IPv4(10, 8, 1, 3)
	l, addr, seq, start := openLossy(t, src, 41002, []byte("x"),
		dropFirst(func(f byte, _ []byte) bool { return f&tcpFIN != 0 }))
	l.ack(src, addr, 41002, seq, start)
	if _, _, p, ok := l.next(2*time.Second, func(f byte, p []byte) bool { return len(p) > 0 }); !ok || string(p) != "x" {
		t.Fatal("no data")
	}
	l.ack(src, addr, 41002, seq, start+1) // data acknowledged, FIN was dropped
	_, finSeq, _, ok := l.next(3*time.Second, func(f byte, _ []byte) bool { return f&tcpFIN != 0 })
	if !ok || finSeq != start+1 {
		t.Fatalf("FIN not resent (ok=%v seq=%d)", ok, finSeq-start)
	}
	key, _ := l3FlowKey(src, addr.IP, 41002, uint16(addr.Port))
	l3TCPMu.Lock()
	kept := l3TCP[key] != nil
	l3TCPMu.Unlock()
	if !kept {
		t.Fatal("flow dropped before its FIN was acknowledged")
	}
	l.ack(src, addr, 41002, seq, start+2) // acknowledge the FIN
	time.Sleep(50 * time.Millisecond)
	l3TCPMu.Lock()
	kept = l3TCP[key] != nil
	l3TCPMu.Unlock()
	if !kept {
		t.Fatal("flow dropped before the member closed its side")
	}
	// The member closes its side: its FIN must be acknowledged, and only
	// then is the flow gone. (Dropping the flow earlier left the member's
	// FIN unanswered, and its stack resent it on every timeout.)
	l3Inet(l.reply, egress.Who{}, src, l3TCPDataFIN(src, addr.IP, 41002, uint16(addr.Port), seq, start+2, nil))
	_, _, _, ok = l.next(time.Second, func(f byte, _ []byte) bool { return f&tcpACK != 0 && f&tcpFIN == 0 })
	if !ok {
		t.Fatal("member's FIN not acknowledged")
	}
	time.Sleep(50 * time.Millisecond)
	l3TCPMu.Lock()
	kept = l3TCP[key] != nil
	l3TCPMu.Unlock()
	if kept {
		t.Fatal("flow kept after both sides closed")
	}
}

// TestL3DuplicateSynGetsSynAck: when the member sends its SYN again (our
// SYN-ACK was lost), the node answers it again.
func TestL3DuplicateSynGetsSynAck(t *testing.T) {
	src := net.IPv4(10, 8, 1, 4)
	l, addr, _, _ := openLossy(t, src, 41003, nil, nil)
	l3Inet(l.reply, egress.Who{}, src, l3TCPSyn(src, addr.IP, 41003, uint16(addr.Port), 1000))
	if _, _, _, ok := l.next(2*time.Second, func(f byte, _ []byte) bool { return f&tcpSYN != 0 }); !ok {
		t.Fatal("repeated SYN got no SYN-ACK")
	}
}

// TestL3RTO: the timeout follows the measured round trip, never under
// l3MinRTO and never over l3MaxRTO.
func TestL3RTO(t *testing.T) {
	f := &l3TCPFlow{}
	f.sampleRTTLocked(200 * time.Millisecond) // 200 + 4*100 ms is under the floor
	if f.rs.rto != l3MinRTO {
		t.Fatalf("rto %v, want the floor", f.rs.rto)
	}
	f = &l3TCPFlow{}
	f.sampleRTTLocked(2 * time.Second) // 2 s + 4*1 s
	if f.rs.rto != 6*time.Second {
		t.Fatalf("rto %v, want 6s", f.rs.rto)
	}
	f.sampleRTTLocked(time.Minute)
	if f.rs.rto != l3MaxRTO {
		t.Fatalf("rto %v, want the ceiling", f.rs.rto)
	}
}

package transport

// Resending what the member did not get (downlink retransmission on l3).
//
// On the l3 front the node is the far end of the member's TCP connections
// (l3inet.go): it reads a site's bytes and sends them to the member as TCP
// segments. TCP needs every byte in order, so a segment that never reaches
// the member's stack stops the whole download until the sender sends it
// again. Segments do go missing even though the tunnel itself is reliable:
// the packets that were in the old CONNECT when the member switches
// networks, a full queue in the member's stack (gVisor in L3Net, the OS TUN
// queue), or a slow phone.
//
// So the node keeps a copy of what it sent until the member acknowledges
// it, and sends it again in two cases:
//
//  1. Timeout. Nothing new was acknowledged for one retransmission timeout
//     (rto). The rto follows the measured round trip (RFC 6298) and is at
//     least l3MinRTO. That floor matters: the tunnel under l3 is itself TCP
//     (HTTP/2 or HTTP/3) and can hold ACKs back for a while; resending too
//     early only adds traffic to a link that is already slow, which slows
//     it further. Each timeout in a row doubles the rto, and after
//     l3MaxResend of them the flow is given up with a RST.
//  2. Fast retransmit. Three duplicate ACKs (the same number, no data, the
//     same window) mean one segment is missing while later ones arrived;
//     its copy goes out at once instead of after the timeout. While
//     recovering, an ACK that covers only part of what was sent points at
//     the next hole, and that one goes out at once too. (The node offers no
//     SACK, so this is how several holes get filled, one per round trip.)
//
// A round trip is measured on one segment at a time and never on a segment
// that was sent twice (Karn's rule): its ACK could answer either copy.

import (
	"sync/atomic"
	"time"
)

const (
	l3MinRTO    = time.Second      // floor of the retransmission timeout
	l3MaxRTO    = 60 * time.Second // ceiling after doubling
	l3MaxResend = 8                // timeouts in a row before the flow is given up
	l3DupAcks   = 3                // duplicate ACKs that trigger a fast retransmit
)

// l3Resends counts segments sent a second (or later) time, node-wide.
var l3Resends atomic.Uint64

// L3Resends is the number of l3 segments the node sent again so far.
func L3Resends() uint64 { return l3Resends.Load() }

// l3Resend is the retransmission state of one flow. All fields are guarded
// by the flow's mu.
type l3Resend struct {
	buf        []byte      // bytes sent but not acknowledged yet
	bufSeq     uint32      // sequence number of buf[0] (not una: the SYN may still be unacknowledged)
	fin        bool        // a FIN was sent; it takes the sequence number right after buf
	gone       bool        // the flow is closed: a late timer must do nothing
	timer      *time.Timer // fires onTimeout
	rto        time.Duration
	srtt       time.Duration // smoothed round trip, 0 until the first sample
	rttvar     time.Duration // its variation
	rttOn      bool          // a round trip is being measured
	rttSeq     uint32        // the measurement ends when this sequence is acknowledged
	rttAt      time.Time     // when that segment was sent
	tries      int           // timeouts in a row
	dups       int           // duplicate ACKs in a row
	lastWin    uint32        // window of the previous ACK, to tell a duplicate from a window update
	recovering bool          // a hole is being filled
	recoverTo  uint32        // recovery ends when everything up to here is acknowledged
}

// sentLocked records chunk, just sent at sequence seq, until it is
// acknowledged. f.mu must be held.
func (f *l3TCPFlow) sentLocked(seq uint32, chunk []byte) {
	wasIdle := len(f.rs.buf) == 0 && !f.rs.fin
	if len(f.rs.buf) == 0 {
		f.rs.bufSeq = seq
	}
	f.rs.buf = append(f.rs.buf, chunk...)
	// Time this segment if nothing else is being timed.
	if !f.rs.rttOn {
		f.rs.rttOn, f.rs.rttSeq, f.rs.rttAt = true, seq+uint32(len(chunk)), time.Now()
	}
	// The timer belongs to the oldest unacknowledged byte: start it only
	// when nothing was waiting before; a running one keeps its deadline.
	if wasIdle {
		f.armLocked()
	}
}

// finLocked builds the FIN that ends the node's side of the flow and keeps
// it for resending like data. f.mu must be held.
func (f *l3TCPFlow) finLocked() []byte {
	pkt := l3TCPPacketAt(f, tcpFIN|tcpACK, f.snd, nil)
	f.rs.fin = true
	f.snd++ // a FIN takes one sequence number
	f.armLocked()
	return pkt
}

// outstandingLocked reports whether anything sent is still unacknowledged.
func (f *l3TCPFlow) outstandingLocked() bool {
	return int32(f.snd-f.una) > 0
}

// onAckLocked handles the acknowledgement number and window of a segment
// from the member. pure is true for an ACK that carries nothing else (no
// data, no SYN, FIN or RST). It returns a segment to send again, if any,
// and whether the flow is finished (its FIN was acknowledged). f.mu must be
// held.
func (f *l3TCPFlow) onAckLocked(ackn, win uint32, pure bool) (resend []byte, finished bool) {
	defer func() { f.rs.lastWin = win }()
	f.cwin = win << f.wshift
	adv := int32(ackn - f.una)
	switch {
	case adv > 0 && int32(f.snd-ackn) >= 0:
		// New bytes acknowledged (and not more than was sent).
		// 1. Forget the kept bytes below ackn. Count from bufSeq, not una:
		//    the SYN and the FIN each take a sequence number that is not
		//    in buf.
		if n := int32(ackn - f.rs.bufSeq); n > 0 {
			k := min(int(n), len(f.rs.buf))
			f.rs.buf = f.rs.buf[k:]
			f.rs.bufSeq += uint32(k)
			if len(f.rs.buf) == 0 {
				f.rs.buf = nil
			}
		}
		f.una = ackn
		// 2. Take a round-trip sample if the timed segment is covered.
		if f.rs.rttOn && int32(ackn-f.rs.rttSeq) >= 0 {
			f.sampleRTTLocked(time.Since(f.rs.rttAt))
			f.rs.rttOn = false
		}
		// 3. Progress: no more doubling, no duplicates counted.
		f.rs.tries, f.rs.dups = 0, 0
		// 4. In recovery, an ACK below the recovery point means the next
		//    segment is missing too: send it now. At or past it, recovery
		//    is over.
		if f.rs.recovering {
			if int32(ackn-f.rs.recoverTo) < 0 {
				resend = f.resendLocked()
			} else {
				f.rs.recovering = false
			}
		}
		// 5. Restart the timer for what is still out, or stop it.
		if f.outstandingLocked() {
			f.armLocked()
		} else {
			f.stopTimerLocked()
		}
		return resend, f.rs.fin && ackn == f.snd
	case adv == 0 && pure && win == f.rs.lastWin && f.outstandingLocked():
		// The same ACK again with nothing new: the member got a later
		// segment but is still missing the one at una.
		f.rs.dups++
		if f.rs.dups == l3DupAcks && !f.rs.recovering {
			f.rs.recovering, f.rs.recoverTo = true, f.snd
			f.rs.rttOn = false // the resent segment must not be timed (Karn)
			resend = f.resendLocked()
		}
	}
	return resend, false
}

// onTimeout runs when nothing was acknowledged for one rto: it sends the
// oldest unacknowledged segment again with a doubled rto, or gives the flow
// up after l3MaxResend timeouts in a row.
func (f *l3TCPFlow) onTimeout() {
	f.mu.Lock()
	if f.rs.gone || !f.outstandingLocked() {
		f.mu.Unlock()
		return
	}
	f.rs.tries++
	if f.rs.tries > l3MaxResend {
		// The member is not answering: reset it and drop the flow.
		rst := l3TCPPacketAt(f, tcpRST|tcpACK, f.snd, nil)
		rep := f.reply
		f.mu.Unlock()
		_ = rep(rst)
		f.forget()
		return
	}
	// Back off, stop timing (Karn), and resend what is at una.
	f.rs.rto = min(f.rs.rto*2, l3MaxRTO)
	f.rs.rttOn, f.rs.dups = false, 0
	f.rs.recovering, f.rs.recoverTo = true, f.snd
	pkt := f.resendLocked()
	f.armLocked()
	rep := f.reply
	f.mu.Unlock()
	if pkt != nil {
		_ = rep(pkt)
	}
}

// resendLocked builds the oldest unacknowledged segment again: up to one
// MSS of kept data, or the FIN when only the FIN is left (it is the last
// sequence number sent). f.mu must be held.
func (f *l3TCPFlow) resendLocked() []byte {
	var pkt []byte
	switch {
	case len(f.rs.buf) > 0:
		n := min(len(f.rs.buf), l3MSS)
		pkt = l3TCPPacketAt(f, tcpPSH|tcpACK, f.rs.bufSeq, f.rs.buf[:n])
	case f.rs.fin:
		pkt = l3TCPPacketAt(f, tcpFIN|tcpACK, f.snd-1, nil)
	default:
		return nil
	}
	l3Resends.Add(1)
	return pkt
}

// sampleRTTLocked folds one round-trip measurement r into srtt and rttvar
// and sets the rto from them (RFC 6298, section 2), within the bounds.
func (f *l3TCPFlow) sampleRTTLocked(r time.Duration) {
	if f.rs.srtt == 0 {
		f.rs.srtt, f.rs.rttvar = r, r/2
	} else {
		d := f.rs.srtt - r
		if d < 0 {
			d = -d
		}
		f.rs.rttvar = (3*f.rs.rttvar + d) / 4
		f.rs.srtt = (7*f.rs.srtt + r) / 8
	}
	f.rs.rto = min(max(f.rs.srtt+4*f.rs.rttvar, l3MinRTO), l3MaxRTO)
}

// armLocked (re)starts the retransmission timer for one rto from now.
func (f *l3TCPFlow) armLocked() {
	if f.rs.gone {
		return
	}
	if f.rs.rto == 0 {
		f.rs.rto = l3MinRTO
	}
	if f.rs.timer == nil {
		f.rs.timer = time.AfterFunc(f.rs.rto, f.onTimeout)
		return
	}
	f.rs.timer.Reset(f.rs.rto)
}

// stopTimerLocked stops the retransmission timer.
func (f *l3TCPFlow) stopTimerLocked() {
	if f.rs.timer != nil {
		f.rs.timer.Stop()
	}
}

// forget ends the flow for good: no more timers, the outbound connection
// closed, and the flow removed from the table (if it is still the one
// there).
func (f *l3TCPFlow) forget() {
	f.mu.Lock()
	f.rs.gone = true
	f.stopTimerLocked()
	f.mu.Unlock()
	f.close()
	l3TCPMu.Lock()
	if l3TCP[f.key] == f {
		delete(l3TCP, f.key)
	}
	l3TCPMu.Unlock()
}

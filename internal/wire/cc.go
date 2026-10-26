package wire

import (
	"fmt"
	"strings"
)

// Congestion control ids on the optional Hello/Status tail.
//
// Congestion control decides how fast a sender pushes bytes when the line
// loses packets. It only changes the sender's own pacing, never the bytes on
// the wire, so each side applies it to what it sends. The client asks in
// Hello and the node answers in Status; 0 on either side means the system
// default (the kernel's on TCP, the QUIC library's on QUIC). Some networks
// may single out unusual pacing, so the client can always leave it off and a
// node can refuse it.
const (
	CCDefault byte = 0
	CCBBR     byte = 1 // BBR: paces by measured bandwidth and RTT
	CCFixed   byte = 2 // sends at an agreed rate and ignores loss (QUIC only)
)

// ParseCCName reads off, bbr, or fixed.
func ParseCCName(s string) (byte, error) {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "", "off", "default", "none":
		return CCDefault, nil
	case "bbr":
		return CCBBR, nil
	case "fixed", "brutal":
		return CCFixed, nil
	default:
		return 0, fmt.Errorf("cc %q: off, bbr, or fixed", s)
	}
}

// CCName is the config spelling of a congestion control id.
func CCName(id byte) string {
	switch id {
	case CCBBR:
		return "bbr"
	case CCFixed:
		return "fixed"
	default:
		return "off"
	}
}

// CCOffer is the node's side of the agreement.
type CCOffer struct {
	Allow   []byte // ids the node permits (bbr, fixed). Empty means off
	TCP     bool   // the node can switch this TCP socket to BBR
	QUIC    bool   // the node can switch this QUIC connection
	MaxUp   uint32 // fixed: cap on the client's sending rate, kbit/s; 0 = none
	MaxDown uint32 // fixed: cap on the node's sending rate, kbit/s; 0 = none
}

// AgreeCC is the node's answer to a Hello ask: the id both sides use and,
// for fixed, the two rates in kbit/s. Up is client to node, down is node to
// client. Anything the node cannot or will not do is 0 (system default).
// GET is always 0: it spreads over several connections, usually via a CDN.
// Fixed is QUIC only; on TCP a fixed ask is answered with BBR when allowed.
// A fixed ask without rates is answered with BBR as well. Under fixed a
// rate of 0 leaves that direction on the system default.
func AgreeCC(front string, ask byte, up, down uint32, o CCOffer) (id byte, upOut, downOut uint32) {
	if ask == CCDefault || front == "" || front == FrontGET {
		return CCDefault, 0, 0
	}
	quic := front == FrontQUIC || front == FrontH3
	if quic && !o.QUIC || !quic && !o.TCP {
		return CCDefault, 0, 0
	}
	if ask == CCFixed && quic && (up > 0 || down > 0) && allowsCC(o.Allow, CCFixed) {
		return CCFixed, capRate(up, o.MaxUp), capRate(down, o.MaxDown)
	}
	if (ask == CCBBR || ask == CCFixed) && allowsCC(o.Allow, CCBBR) {
		return CCBBR, 0, 0
	}
	return CCDefault, 0, 0
}

func allowsCC(allow []byte, id byte) bool {
	for _, a := range allow {
		if a == id {
			return true
		}
	}
	return false
}

func capRate(v, max uint32) uint32 {
	if max > 0 && v > max {
		return max
	}
	return v
}

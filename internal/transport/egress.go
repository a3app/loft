package transport

import (
	"context"
	"net"
	"sync/atomic"
	"time"

	"github.com/a3app/loft/internal/egress"
	"github.com/a3app/loft/internal/mux"
	"github.com/a3app/loft/internal/wire"
)

// Egress is the node's exit rules (egress.conf); nil sends everything out
// directly. Swapped on reload.
var Egress atomic.Pointer[egress.Router]

// whoOf is the member behind a session, for the exit rules.
func whoOf(sess *mux.Session) egress.Who {
	o := NetSwitch()
	if o == nil || sess == nil {
		return egress.Who{}
	}
	return o.Who(sess)
}

// dialOut opens a member's TCP connection to the internet.
// Loopback, link-local, and metadata names are refused, same as CONNECT.
func dialOut(who egress.Who, target string) (net.Conn, error) {
	host, _, err := net.SplitHostPort(target)
	if err != nil || spliceDenied(host) {
		return nil, errDenied
	}
	if r := Egress.Load(); r != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		c, _, err := r.DialTCP(ctx, who, target)
		return c, err
	}
	return (&net.Dialer{Timeout: 10 * time.Second, Control: dialGuard}).Dial("tcp4", target)
}

// Who returns the member attached on sess, its tags and overlay VLAN.
func (o *Overlay) Who(sess *mux.Session) egress.Who {
	if o == nil {
		return egress.Who{}
	}
	o.mu.Lock()
	p := o.byS[sess]
	member := ""
	if p != nil {
		member = p.member
	}
	o.mu.Unlock()
	w := egress.Who{Member: member}
	if n := o.N(); member != "" && n != nil {
		if m := n.MemberByID(member); m != nil {
			w.Tags = m.Tags
			w.VLAN = m.VLAN
		}
	}
	return w
}

// Peer is one attached member session, for the status file.
type Peer struct {
	Member string    `json:"member"`
	IP     string    `json:"ip"`
	Inst   string    `json:"inst,omitempty"`
	From   string    `json:"from,omitempty"`
	Front  string    `json:"front,omitempty"` // live bulk mux front (get/ws/h2/quic/h3/l3)
	Since  time.Time `json:"since"`
}

// Peers lists the attached sessions.
func (o *Overlay) Peers() []Peer {
	if o == nil {
		return nil
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	out := make([]Peer, 0, len(o.tab))
	for _, p := range o.tab {
		front := ""
		if p.l3 != nil {
			front = wire.FrontL3
		} else {
			front = sessionBulkFront(p.sess)
		}
		out = append(out, Peer{Member: p.member, IP: p.ip, Inst: p.inst, From: p.seen, Front: front, Since: p.at})
	}
	return out
}

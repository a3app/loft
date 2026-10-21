package egress

import (
	"context"
	"net"
	"sync"
	"time"
)

// UDP carries one session's datagrams: each destination goes the way the
// rules say, through one socket per way, and replies come back through out.
type UDP struct {
	r     *Router
	who   Who
	out   func(from string, data []byte)
	mu    sync.Mutex
	ways  map[string]udpWay
	close bool
}

type udpWay interface {
	send(target string, data []byte) error
	Close() error
}

// NewUDP starts the datagram side of a session.
func (r *Router) NewUDP(who Who, out func(from string, data []byte)) *UDP {
	return &UDP{r: r, who: who, out: out, ways: map[string]udpWay{}}
}

// Close drops every socket.
func (u *UDP) Close() {
	u.mu.Lock()
	defer u.mu.Unlock()
	u.close = true
	for _, w := range u.ways {
		w.Close()
	}
	u.ways = map[string]udpWay{}
}

// Send sends one datagram to target ("host:port"); a blocked or unreachable
// destination drops it, as UDP would.
func (u *UDP) Send(target string, data []byte) {
	host, _, err := net.SplitHostPort(target)
	if err != nil {
		return
	}
	// No spreading: a member's datagrams to one host keep one way out.
	for _, s := range u.r.steps(u.r.Route(u.who, host), false) {
		if s == Block {
			return
		}
		if s != Direct {
			cs := u.r.ch[s]
			if cs == nil || !u.r.up(cs) {
				continue
			}
			switch cs.ch.UDP {
			case Block:
				return
			case "", Direct:
				if cs.ch.Kind == KindSOCKS5 || cs.ch.Kind == KindHTTP {
					s = Direct // the proxy carries TCP only; UDP goes out as set
				}
			}
		}
		w, err := u.way(s)
		if err != nil {
			continue
		}
		if err := w.send(target, data); err == nil {
			if cs := u.r.ch[s]; cs != nil {
				cs.tx.Add(uint64(len(data)))
			}
			return
		}
	}
}

func (u *UDP) way(s string) (udpWay, error) {
	u.mu.Lock()
	defer u.mu.Unlock()
	if u.close {
		return nil, net.ErrClosed
	}
	if w := u.ways[s]; w != nil {
		return w, nil
	}
	cs := u.r.ch[s]
	var w udpWay
	switch {
	case s == Direct:
		pc, err := net.ListenPacket("udp4", ":0")
		if err != nil {
			return nil, err
		}
		w = &pcWay{pc: pc}
	case cs.ch.Kind == KindDev:
		pc, err := (&net.ListenConfig{Control: bindDev(cs.ch.Dev)}).ListenPacket(context.Background(), "udp4", ":0")
		if err != nil {
			return nil, err
		}
		w = &pcWay{pc: pc}
	case cs.ch.Kind == KindSOCKS5:
		ctx, cancel := context.WithTimeout(u.r.ctx, dialTimeout)
		su, err := socksAssociate(ctx, u.r.under(cs.ch.Via), &cs.ch)
		cancel()
		if err != nil {
			u.r.fail(cs, err)
			return nil, err
		}
		w = &socksWay{u: su}
	case cs.ch.Kind == KindSSTP:
		cs.mu.Lock()
		ss := cs.sstp
		cs.mu.Unlock()
		if ss == nil {
			return nil, net.ErrClosed
		}
		pc, err := ss.ListenUDP()
		if err != nil {
			return nil, err
		}
		w = &pcWay{pc: pc}
	default:
		return nil, net.ErrClosed
	}
	u.ways[s] = w
	go u.read(s, w, cs)
	return w, nil
}

func (u *UDP) read(s string, w udpWay, cs *chanState) {
	buf := make([]byte, 65535)
	for {
		var n int
		var from string
		var err error
		switch x := w.(type) {
		case *pcWay:
			var a net.Addr
			x.pc.SetReadDeadline(time.Now().Add(2 * time.Minute))
			n, a, err = x.pc.ReadFrom(buf)
			if err == nil {
				from = a.String()
				if ua, ok := a.(*net.UDPAddr); ok {
					if v4 := ua.IP.To4(); v4 != nil {
						from = (&net.UDPAddr{IP: v4, Port: ua.Port}).String()
					}
				}
			}
		case *socksWay:
			n, from, err = x.u.ReadFrom(buf)
		}
		if err != nil {
			// Idle or closed: the next datagram opens a fresh socket.
			u.mu.Lock()
			if u.ways[s] == w {
				delete(u.ways, s)
			}
			u.mu.Unlock()
			w.Close()
			return
		}
		if cs != nil {
			cs.rx.Add(uint64(n))
		}
		u.out(from, buf[:n])
	}
}

type pcWay struct{ pc net.PacketConn }

func (p *pcWay) send(target string, data []byte) error {
	a, err := net.ResolveUDPAddr("udp4", target)
	if err != nil {
		return err
	}
	_, err = p.pc.WriteTo(data, a)
	return err
}
func (p *pcWay) Close() error { return p.pc.Close() }

type socksWay struct{ u *socksUDP }

func (s *socksWay) send(target string, data []byte) error { return s.u.WriteTo(data, target) }
func (s *socksWay) Close() error                          { return s.u.Close() }

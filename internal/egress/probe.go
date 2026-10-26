package egress

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"os"
	"sync/atomic"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
)

// autoProbes are HTTPS hosts Automatic mode tries; any one success is enough.
// The probe goroutine reads the list while a test (or a later reload) may replace it.
var autoProbes atomic.Value // []string

func init() {
	autoProbes.Store([]string{"ya.ru:443", "nic.ru:443", "google.com:443"})
}

func autoProbeList() []string {
	v, _ := autoProbes.Load().([]string)
	return v
}

// probe runs a channel's link check until the router closes. Fails probes
// in a row mark the channel "no link"; one success clears it. Defaults are
// every 10 s and 3 fails; one probe gets at most 5 s (or the interval, if
// shorter). The first probe runs at once. Only changes of state are logged.
func (r *Router) probe(cs *chanState) {
	k := cs.ch.Check
	every := time.Duration(k.Every) * time.Second
	if every <= 0 {
		every = 10 * time.Second
	}
	fails := k.Fails
	if fails <= 0 {
		fails = 3
	}
	wait := min(every, 5*time.Second)
	bad := 0
	t := time.NewTimer(0)
	defer t.Stop()
	for {
		select {
		case <-r.ctx.Done():
			return
		case <-t.C:
		}
		ctx, cancel := context.WithTimeout(r.ctx, wait)
		err := r.probeOnce(ctx, cs)
		cancel()
		if r.ctx.Err() != nil {
			return
		}
		cs.mu.Lock()
		was := cs.noLink
		if err == nil {
			bad, cs.noLink = 0, false
		} else if bad++; bad >= fails {
			cs.noLink = true
			cs.lastErr = "link check: " + err.Error()
		}
		now := cs.noLink
		cs.mu.Unlock()
		if now != was {
			if now {
				r.opt.Log("egress: %s: no link (%v)", cs.ch.Name, err)
			} else {
				r.opt.Log("egress: %s: link is back", cs.ch.Name)
			}
		}
		t.Reset(every)
	}
}

// probeOnce is one check. TCP and TLS go the way traffic would (through the
// proxy, SSTP or interface, or the host default route for direct); ping is
// ICMP out of the channel's interface (or the default route when Dev is empty).
func (r *Router) probeOnce(ctx context.Context, cs *chanState) error {
	k := cs.ch.Check
	switch k.Kind {
	case "tcp":
		c, err := r.probeDial(ctx, cs, k.Probe)
		if err != nil {
			return err
		}
		return c.Close()
	case "tls":
		return r.probeTLS(ctx, cs, k.Probe)
	case "auto":
		var last error
		for _, addr := range autoProbeList() {
			if err := r.probeTLS(ctx, cs, addr); err == nil {
				return nil
			} else {
				last = err
			}
		}
		if last == nil {
			return errors.New("no auto probes")
		}
		return last
	case "ping":
		return pingDev(ctx, cs.ch.Dev, k.Probe)
	}
	return fmt.Errorf("unknown check %q", k.Kind)
}

// probeDial connects to addr the way the channel would carry traffic. The
// direct pseudo-channel uses a plain dialer: the probe tests the host route,
// not the Deny rules.
func (r *Router) probeDial(ctx context.Context, cs *chanState, addr string) (net.Conn, error) {
	if cs == r.direct {
		return (&net.Dialer{}).DialContext(ctx, "tcp4", addr)
	}
	return r.dialOn(ctx, cs, addr)
}

// probeTLS connects and completes a TLS handshake with certificate checks.
// It proves more than a TCP connect: a broken tunnel or a middlebox often
// still accepts TCP but cannot finish a valid handshake.
func (r *Router) probeTLS(ctx context.Context, cs *chanState, addr string) error {
	c, err := r.probeDial(ctx, cs, addr)
	if err != nil {
		return err
	}
	defer c.Close()
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	cfg := &tls.Config{ServerName: host}
	if r.opt.TLS != nil {
		cfg = r.opt.TLS.Clone()
		if cfg.ServerName == "" {
			cfg.ServerName = host
		}
	}
	if d, ok := ctx.Deadline(); ok {
		_ = c.SetDeadline(d)
	}
	return tls.Client(c, cfg).HandshakeContext(ctx)
}

// pingDev sends one ICMP echo out of dev and waits for the reply. It opens
// a raw ICMP socket, so the node needs root or CAP_NET_RAW. Replies from
// other hosts or with another echo ID are ignored.
func pingDev(ctx context.Context, dev, host string) error {
	ips, err := net.DefaultResolver.LookupIP(ctx, "ip4", host)
	if err != nil || len(ips) == 0 {
		return fmt.Errorf("resolve %s: %v", host, err)
	}
	dst := &net.IPAddr{IP: ips[0]}
	lc := net.ListenConfig{}
	if dev != "" {
		lc.Control = bindDev(dev)
	}
	pc, err := lc.ListenPacket(ctx, "ip4:icmp", "0.0.0.0")
	if err != nil {
		return err
	}
	defer pc.Close()
	if d, ok := ctx.Deadline(); ok {
		pc.SetDeadline(d)
	}
	id := os.Getpid() & 0xffff
	msg := icmp.Message{Type: ipv4.ICMPTypeEcho, Body: &icmp.Echo{ID: id, Seq: 1, Data: []byte("loft")}}
	b, err := msg.Marshal(nil)
	if err != nil {
		return err
	}
	if _, err := pc.WriteTo(b, dst); err != nil {
		return err
	}
	buf := make([]byte, 1500)
	for {
		n, from, err := pc.ReadFrom(buf)
		if err != nil {
			return errors.New("no reply")
		}
		if a, ok := from.(*net.IPAddr); !ok || !a.IP.Equal(dst.IP) {
			continue
		}
		m, err := icmp.ParseMessage(1, buf[:n])
		if err != nil || m.Type != ipv4.ICMPTypeEchoReply {
			continue
		}
		if e, ok := m.Body.(*icmp.Echo); ok && e.ID == id {
			return nil
		}
	}
}

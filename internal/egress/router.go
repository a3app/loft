package egress

import (
	"context"
	"crypto/tls"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"golang.org/x/time/rate"
)

// SSTPConn is a held SSTP session that can open TCP and UDP through it
// (the node wires clients/go/inbound.SSTPNet in; this package does not
// import it, to keep transport free of client code).
type SSTPConn interface {
	Open(target string) (net.Conn, error)
	ListenUDP() (net.PacketConn, error)
	Done() <-chan struct{}
	Close() error
	OverlayIP() net.IP
}

// SSTPFunc connects one SSTP channel.
type SSTPFunc func(ctx context.Context, ch Channel) (SSTPConn, error)

// Options are what the router needs besides egress.conf.
type Options struct {
	Lists  map[string][]string // node.conf lists: domain, .suffix, CIDR
	SSTP   SSTPFunc
	SysNet string // /sys/class/net (tests)
	Log    func(string, ...any)
	TLS    *tls.Config // nil = system roots; tests pass InsecureSkipVerify
}

// Who is the member a connection belongs to; empty for traffic without one.
type Who struct {
	Member string
	Tags   []string
	VLAN   int // overlay VLAN; 0 is the network's untagged segment
}

type chanState struct {
	ch        Channel
	tx, rx    atomic.Uint64
	conns     atomic.Int64
	mu        sync.Mutex
	downUntil time.Time
	lastErr   string
	sstp      SSTPConn
	sstpState string
	noLink    bool // the link check failed Fails times in a row
}

// prioState is a priority list at run time: where multipath starts next,
// and the shared speed limits of its channels (in, out).
type prioState struct {
	next atomic.Uint64
	lim  map[string][2]*rate.Limiter
}

// Router applies one egress.conf. Make a new one on reload and Close the old.
type Router struct {
	cfg    *Config
	opt    Options
	ch     map[string]*chanState
	prio   map[string]*prioState
	direct *chanState // host default route when DirectCheck is set
	ctx    context.Context
	cancel context.CancelFunc
}

// dialTimeout bounds one connect through any channel.
const dialTimeout = 10 * time.Second

// downFor is how long a channel that failed a connect is skipped.
const downFor = 20 * time.Second

// New starts a router for cfg; SSTP channels connect in the background.
func New(cfg *Config, opt Options) *Router {
	if opt.SysNet == "" {
		opt.SysNet = "/sys/class/net"
	}
	if opt.Log == nil {
		opt.Log = func(string, ...any) {}
	}
	r := &Router{cfg: cfg, opt: opt, ch: map[string]*chanState{}, prio: map[string]*prioState{}}
	for _, p := range cfg.Prios {
		ps := &prioState{lim: map[string][2]*rate.Limiter{}}
		for ch, l := range p.Limits {
			ps.lim[ch] = [2]*rate.Limiter{limiter(l.In), limiter(l.Out)}
		}
		r.prio[p.Name] = ps
	}
	r.ctx, r.cancel = context.WithCancel(context.Background())
	for _, c := range cfg.Channels {
		cs := &chanState{ch: c}
		if c.Kind == KindSSTP && !c.Off {
			cs.sstpState = "connecting"
		}
		r.ch[c.Name] = cs
	}
	// Start sessions and probes only once r.ch is whole: a probe through a
	// proxy reads it for the via channel.
	for _, c := range cfg.Channels {
		cs := r.ch[c.Name]
		if c.Kind == KindSSTP && !c.Off {
			go r.holdSSTP(cs)
		}
		if c.Check != nil && !c.Off {
			go r.probe(cs)
		}
	}
	if k := cfg.DirectCheck; k != nil {
		r.direct = &chanState{ch: Channel{Name: Direct, Kind: Direct, Check: k}}
		go r.probe(r.direct)
	}
	return r
}

// Close stops SSTP sessions; open connections keep running.
func (r *Router) Close() {
	r.cancel()
	for _, cs := range r.ch {
		cs.mu.Lock()
		if cs.sstp != nil {
			cs.sstp.Close()
		}
		cs.mu.Unlock()
	}
}

// Config is what the router runs.
func (r *Router) Config() *Config { return r.cfg }

func (r *Router) holdSSTP(cs *chanState) {
	wait := 5 * time.Second
	for r.ctx.Err() == nil {
		cs.mu.Lock()
		cs.sstpState = "connecting"
		cs.mu.Unlock()
		if r.opt.SSTP == nil {
			r.fail(cs, errors.New("this node has no SSTP client"))
			return
		}
		ctx, cancel := context.WithTimeout(r.ctx, 30*time.Second)
		s, err := r.opt.SSTP(ctx, cs.ch)
		cancel()
		if err != nil {
			r.fail(cs, err)
			r.opt.Log("egress: %s: %v", cs.ch.Name, err)
			select {
			case <-r.ctx.Done():
				return
			case <-time.After(wait):
			}
			if wait < time.Minute {
				wait *= 2
			}
			continue
		}
		wait = 5 * time.Second
		cs.mu.Lock()
		cs.sstp, cs.sstpState, cs.lastErr = s, "up", ""
		cs.mu.Unlock()
		r.opt.Log("egress: %s: SSTP up, %s", cs.ch.Name, s.OverlayIP())
		select {
		case <-r.ctx.Done():
			s.Close()
			return
		case <-s.Done():
		}
		cs.mu.Lock()
		cs.sstp = nil
		cs.mu.Unlock()
		r.opt.Log("egress: %s: SSTP session ended", cs.ch.Name)
	}
}

func (r *Router) fail(cs *chanState, err error) {
	cs.mu.Lock()
	cs.lastErr = err.Error()
	cs.downUntil = time.Now().Add(downFor)
	cs.mu.Unlock()
}

// up reports whether a channel may be tried now.
func (r *Router) up(cs *chanState) bool {
	cs.mu.Lock()
	down := time.Now().Before(cs.downUntil)
	sstp := cs.sstp
	noLink := cs.noLink
	cs.mu.Unlock()
	if cs.ch.Off || noLink {
		return false
	}
	switch cs.ch.Kind {
	case KindDev:
		b, err := os.ReadFile(filepath.Join(r.opt.SysNet, cs.ch.Dev, "operstate"))
		if err != nil || strings.TrimSpace(string(b)) == "down" {
			return false
		}
	case KindSSTP:
		if sstp == nil {
			return false
		}
	}
	return !down
}

// Route is where traffic of who to host goes first: a channel, direct or block.
func (r *Router) Route(who Who, host string) string {
	for _, ru := range r.cfg.Rules {
		ok := false
		switch ru.Match {
		case "member":
			ok = who.Member != "" && ru.Value == who.Member
		case "tag":
			for _, t := range who.Tags {
				ok = ok || strings.EqualFold(t, ru.Value)
			}
		case "vlan":
			ok = who.Member != "" && ru.Value == strconv.Itoa(who.VLAN)
		case "list":
			ok = inList(r.opt.Lists[ru.Value], host)
		}
		if ok {
			return ru.Via
		}
	}
	if r.cfg.Default == "" {
		return Direct
	}
	return r.cfg.Default
}

func inList(entries []string, host string) bool {
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	ip := net.ParseIP(host)
	for _, e := range entries {
		e = strings.ToLower(strings.TrimSpace(e))
		switch {
		case strings.Contains(e, "/"):
			if _, n, err := net.ParseCIDR(e); err == nil && ip != nil && n.Contains(ip) {
				return true
			}
		case strings.HasPrefix(e, "."):
			if host == e[1:] || strings.HasSuffix(host, e) {
				return true
			}
		default:
			if host == e || (ip != nil && ip.Equal(net.ParseIP(e))) {
				return true
			}
		}
	}
	return false
}

var errBlocked = errors.New("blocked by the exit rules")

// steps is every hop a route may try in order: a priority's channels and
// its last step, or a channel followed through its fail links. spread
// turns a multipath list one channel further for each new connection.
func (r *Router) steps(first string, spread bool) []string {
	if p := r.cfg.Prio(first); p != nil {
		last := p.Last
		if last == "" {
			last = Direct
		}
		use := p.Use
		if p.Multi && spread && len(use) > 1 {
			k := int(r.prio[p.Name].next.Add(1) % uint64(len(use)))
			use = append(append([]string{}, use[k:]...), use[:k]...)
		}
		return append(append([]string{}, use...), last)
	}
	var out []string
	seen := map[string]bool{}
	for s := first; s != "" && !seen[s]; {
		seen[s] = true
		out = append(out, s)
		if s == Direct || s == Block {
			break
		}
		cs := r.ch[s]
		if cs == nil {
			break
		}
		s = cs.ch.Fail
		if s == "" {
			s = Direct
		}
	}
	return out
}

// DialTCP opens a TCP connection for who to target along the rules; it
// returns the step that carried it (a channel name or direct).
func (r *Router) DialTCP(ctx context.Context, who Who, target string) (net.Conn, string, error) {
	host, _, err := net.SplitHostPort(target)
	if err != nil {
		return nil, "", err
	}
	var last error = errBlocked
	route := r.Route(who, host)
	steps := r.steps(route, true)
	for i, s := range steps {
		switch s {
		case Direct:
			if !r.directUp() {
				last = errors.New("direct: no link")
				if i == len(steps)-1 {
					return nil, Direct, last
				}
				continue
			}
			c, err := (&net.Dialer{Timeout: dialTimeout}).DialContext(ctx, "tcp4", target)
			if err == nil {
				return c, Direct, nil
			}
			last = err
			if i == len(steps)-1 {
				return nil, Direct, err
			}
			continue
		case Block:
			return nil, Block, last
		}
		cs := r.ch[s]
		if cs == nil || !r.up(cs) {
			continue
		}
		cctx, cancel := context.WithTimeout(ctx, dialTimeout)
		c, err := r.dialOn(cctx, cs, target)
		cancel()
		if err == nil {
			c = r.count(cs, c)
			if ps := r.prio[route]; ps != nil {
				if l, ok := ps.lim[s]; ok {
					c = &limited{Conn: c, in: l[0], out: l[1]}
				}
			}
			return c, s, nil
		}
		last = fmt.Errorf("%s: %v", s, err)
		r.fail(cs, err)
	}
	return nil, "", last
}

func (r *Router) dialOn(ctx context.Context, cs *chanState, target string) (net.Conn, error) {
	switch cs.ch.Kind {
	case KindDev:
		return (&net.Dialer{Control: bindDev(cs.ch.Dev)}).DialContext(ctx, "tcp4", target)
	case KindSOCKS5:
		return socksDial(ctx, r.under(cs.ch.Via), &cs.ch, target)
	case KindHTTP:
		return httpDial(ctx, r.under(cs.ch.Via), &cs.ch, target)
	case KindSSTP:
		cs.mu.Lock()
		s := cs.sstp
		cs.mu.Unlock()
		if s == nil {
			return nil, errors.New("SSTP is not connected")
		}
		return s.Open(target)
	}
	return nil, fmt.Errorf("unknown channel kind %q", cs.ch.Kind)
}

// under is how a proxy itself is reached: directly or through via.
func (r *Router) under(via string) dialFn {
	if cs := r.ch[via]; via != "" && cs != nil {
		return func(ctx context.Context, _, addr string) (net.Conn, error) { return r.dialOn(ctx, cs, addr) }
	}
	return func(ctx context.Context, network, addr string) (net.Conn, error) {
		return (&net.Dialer{Timeout: dialTimeout}).DialContext(ctx, network, addr)
	}
}

type counted struct {
	net.Conn
	cs   *chanState
	once sync.Once
}

func (c *counted) Read(p []byte) (int, error) {
	n, err := c.Conn.Read(p)
	c.cs.rx.Add(uint64(n))
	return n, err
}

func (c *counted) Write(p []byte) (int, error) {
	n, err := c.Conn.Write(p)
	c.cs.tx.Add(uint64(n))
	return n, err
}

func (c *counted) Close() error {
	c.once.Do(func() { c.cs.conns.Add(-1) })
	return c.Conn.Close()
}

func (r *Router) count(cs *chanState, c net.Conn) net.Conn {
	cs.conns.Add(1)
	return &counted{Conn: c, cs: cs}
}

// Status is one channel as the node sees it.
type Status struct {
	Name  string `json:"name"`
	Kind  string `json:"kind"`
	State string `json:"state"` // up | down | connecting | nolink | off
	Error string `json:"error,omitempty"`
	TX    uint64 `json:"tx"`
	RX    uint64 `json:"rx"`
	Conns int64  `json:"conns"`
	IP    string `json:"ip,omitempty"` // SSTP: the address the server gave
}

// Status lists every channel.
func (r *Router) Status() []Status {
	out := make([]Status, 0, len(r.cfg.Channels))
	for _, c := range r.cfg.Channels {
		cs := r.ch[c.Name]
		st := Status{Name: c.Name, Kind: c.Kind, State: "up", TX: cs.tx.Load(), RX: cs.rx.Load(), Conns: cs.conns.Load()}
		if !r.up(cs) {
			st.State = "down"
		}
		cs.mu.Lock()
		if cs.noLink {
			st.State = "nolink"
		}
		cs.mu.Unlock()
		if c.Off {
			st.State = "off"
		}
		cs.mu.Lock()
		st.Error = cs.lastErr
		if c.Kind == KindSSTP {
			if cs.sstp != nil {
				st.IP = cs.sstp.OverlayIP().String()
			} else if cs.sstpState == "connecting" {
				st.State = "connecting"
			}
		}
		cs.mu.Unlock()
		out = append(out, st)
	}
	if cs := r.direct; cs != nil {
		st := Status{Name: Direct, Kind: Direct, State: "up"}
		cs.mu.Lock()
		if cs.noLink {
			st.State = "nolink"
		}
		st.Error = cs.lastErr
		cs.mu.Unlock()
		out = append([]Status{st}, out...)
	}
	return out
}

func (r *Router) directUp() bool {
	if r.direct == nil {
		return true
	}
	return !r.direct.noLinkNow()
}

func (cs *chanState) noLinkNow() bool {
	cs.mu.Lock()
	defer cs.mu.Unlock()
	return cs.noLink
}

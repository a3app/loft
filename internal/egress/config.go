// Package egress decides how a member's traffic leaves the node: directly
// (the server's own route), through a kernel interface (a WireGuard,
// OpenVPN or ZeroTier channel), through a SOCKS5 or HTTP(S) proxy, or
// through an SSTP session the node holds itself. Rules pick a channel by
// member, member tag, overlay VLAN (segment) or named address list and send it to a priority list:
// channels in order, the first one that works carries the traffic, then a
// last step (direct or block). A channel can check its link; while the
// check fails, priorities skip it. A rule may still name a channel; then its
// fail chain (another channel, direct, block) is the order, as before.
//
// egress.conf, next to network.conf:
//
//	channel wg-nl dev wg-nl check ping probe 1.1.1.1 every 10 fails 3
//	channel wg-fi dev wg-fi
//	channel px-office socks5 192.0.2.40:1080 user loft pass "s3cret" udp proxy via wg-nl check tcp probe example.com:443
//	channel web http 192.0.2.41:3128 tls
//	channel office sstp vpn.example.org:443 user office pass x auth mschapv2
//	prio default direct wg-nl then block
//	prio main wg-nl wg-fi then direct limit wg-fi 50/10
//	prio de px-office web then block multi
//	rule member bob via de
//	rule tag office via main
//	rule vlan 10 via main
//	rule list blocked-ru via main
//	default via default
package egress

import (
	"errors"
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"
)

// Kinds of channel.
const (
	KindDev    = "dev"
	KindSOCKS5 = "socks5"
	KindHTTP   = "http"
	KindSSTP   = "sstp"
)

// Terminal outcomes in a fail chain or a rule.
const (
	Direct = "direct"
	Block  = "block"
)

// Channel is one way out.
type Channel struct {
	Name string
	Kind string
	Dev  string // dev: the interface
	Addr string // socks5, http, sstp: host:port
	User string
	Pass string
	TLS  bool   // http: TLS to the proxy (an HTTPS proxy)
	UDP  string // socks5: proxy | direct | block; http: direct | block. Empty = direct.
	Via  string // socks5, http: reach the proxy through this channel
	Auth string // sstp: auto | mschapv2 | pap
	// Insecure skips the SSTP server certificate check.
	Insecure bool
	// Off: the channel is switched off; traffic for it takes Fail.
	Off bool
	// Fail is the next step when this channel is down: a channel, direct or block.
	// Only rules that name the channel itself follow it; priorities do not.
	Fail string
	// Check probes the link through the channel; nil = no check.
	Check *Check
}

// Check is a link probe: ping (kernel-interface channels only) or a TCP connect.
type Check struct {
	Kind  string // ping | tcp
	Probe string // ping: an IP or host; tcp: host:port
	Every int    // seconds between probes; 0 = 10
	Fails int    // failures in a row before "no link"; 0 = 3
}

// Prio is a priority list: channels in order, then Last (direct or block).
// Use may include Direct for the host's own default route (the WAN
// adapter). Multi spreads new connections over the working channels in turn
// instead of sending them all to the first one. Limits cap a channel's
// speed for everyone who leaves by this list.
type Prio struct {
	Name   string
	Use    []string
	Last   string // direct | block; empty = direct
	Multi  bool
	Limits map[string]Limit
}

// Limit is a speed cap in Mbit/s; 0 = none. In is towards members (what
// they download), Out is from them.
type Limit struct {
	In, Out int
}

// Rule sends matching traffic to Via (a priority, a channel, direct or block).
type Rule struct {
	Match string // member | tag | vlan | list
	Value string
	Via   string
}

// Config is a parsed egress.conf.
type Config struct {
	Channels []Channel
	Prios    []Prio
	Rules    []Rule
	Default  string // a priority, a channel, direct or block; empty = direct
}

// Prio returns the priority list called name.
func (c *Config) Prio(name string) *Prio {
	for i := range c.Prios {
		if c.Prios[i].Name == name {
			return &c.Prios[i]
		}
	}
	return nil
}

// Channel returns the channel called name.
func (c *Config) Channel(name string) *Channel {
	for i := range c.Channels {
		if c.Channels[i].Name == name {
			return &c.Channels[i]
		}
	}
	return nil
}

func validName(s string) bool {
	if s == "" || len(s) > 32 || s == Direct || s == Block {
		return false
	}
	for _, r := range s {
		if !(r >= 'a' && r <= 'z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.') {
			return false
		}
	}
	return true
}

// tokens splits a line into words; "double quoted" words use Go escapes.
func tokens(line string) ([]string, error) {
	var out []string
	s := strings.TrimSpace(line)
	for s != "" {
		if s[0] == '"' {
			q, err := strconv.QuotedPrefix(s)
			if err != nil {
				return nil, fmt.Errorf("bad quoted word: %v", err)
			}
			w, _ := strconv.Unquote(q)
			out = append(out, w)
			s = strings.TrimSpace(s[len(q):])
			continue
		}
		i := strings.IndexAny(s, " \t")
		if i < 0 {
			out = append(out, s)
			break
		}
		out = append(out, s[:i])
		s = strings.TrimSpace(s[i:])
	}
	return out, nil
}

// Quote writes a word so tokens reads it back.
func Quote(s string) string {
	if s != "" && !strings.ContainsAny(s, " \t\"\\#") {
		return s
	}
	return strconv.Quote(s)
}

// Parse reads egress.conf.
func Parse(text string) (*Config, error) {
	c := &Config{}
	for n, raw := range strings.Split(text, "\n") {
		line := strings.TrimSpace(raw)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		t, err := tokens(line)
		if err != nil {
			return nil, fmt.Errorf("line %d: %v", n+1, err)
		}
		if err := c.line(t); err != nil {
			return nil, fmt.Errorf("line %d: %v", n+1, err)
		}
	}
	return c, c.check()
}

func (c *Config) line(t []string) error {
	switch t[0] {
	case "channel":
		if len(t) < 3 {
			return errors.New("channel NAME KIND …")
		}
		ch := Channel{Name: t[1], Kind: t[2]}
		if !validName(ch.Name) {
			return fmt.Errorf("channel name %q: lowercase letters, digits, - _ .", ch.Name)
		}
		if c.Channel(ch.Name) != nil {
			return fmt.Errorf("channel %s twice", ch.Name)
		}
		rest := t[3:]
		switch ch.Kind {
		case KindDev:
			if len(rest) < 1 {
				return errors.New("channel NAME dev INTERFACE")
			}
			ch.Dev, rest = rest[0], rest[1:]
		case KindSOCKS5, KindHTTP, KindSSTP:
			if len(rest) < 1 {
				return fmt.Errorf("channel NAME %s HOST:PORT", ch.Kind)
			}
			ch.Addr, rest = rest[0], rest[1:]
			if _, _, err := net.SplitHostPort(ch.Addr); err != nil {
				return fmt.Errorf("%q is not host:port", ch.Addr)
			}
		default:
			return fmt.Errorf("unknown channel kind %q", ch.Kind)
		}
		for len(rest) > 0 {
			k := rest[0]
			flag := k == "tls" || k == "insecure" || k == "off"
			if !flag && len(rest) < 2 {
				return fmt.Errorf("%s needs a value", k)
			}
			v := ""
			if !flag {
				v = rest[1]
			}
			switch k {
			case "user":
				ch.User = v
			case "pass":
				ch.Pass = v
			case "tls":
				ch.TLS = true
			case "insecure":
				ch.Insecure = true
			case "off":
				ch.Off = true
			case "udp":
				ch.UDP = v
			case "via":
				ch.Via = v
			case "auth":
				ch.Auth = v
			case "fail":
				ch.Fail = v
			case "check":
				if ch.Check == nil {
					ch.Check = &Check{}
				}
				ch.Check.Kind = v
			case "probe", "every", "fails":
				if ch.Check == nil {
					return fmt.Errorf("%s needs check first", k)
				}
				if k == "probe" {
					ch.Check.Probe = v
					break
				}
				n, err := strconv.Atoi(v)
				if err != nil || n < 1 {
					return fmt.Errorf("%s %q: a positive number", k, v)
				}
				if k == "every" {
					ch.Check.Every = n
				} else {
					ch.Check.Fails = n
				}
			default:
				return fmt.Errorf("unknown channel option %q", k)
			}
			if flag {
				rest = rest[1:]
			} else {
				rest = rest[2:]
			}
		}
		c.Channels = append(c.Channels, ch)
	case "prio":
		then := slices.Index(t, "then")
		if len(t) < 4 || then < 2 || then == len(t)-1 {
			return errors.New("prio NAME CHANNEL… then direct|block [multi] [limit CHANNEL IN/OUT]…")
		}
		p := Prio{Name: t[1], Use: t[2:then], Last: t[then+1]}
		for rest := t[then+2:]; len(rest) > 0; {
			switch {
			case rest[0] == "multi":
				p.Multi, rest = true, rest[1:]
			case rest[0] == "limit" && len(rest) >= 3:
				in, out, ok := strings.Cut(rest[2], "/")
				a, err1 := strconv.Atoi(in)
				b, err2 := strconv.Atoi(out)
				if !ok || err1 != nil || err2 != nil || a < 0 || b < 0 {
					return fmt.Errorf("limit %s %q: IN/OUT in Mbit/s, 0 = none", rest[1], rest[2])
				}
				if p.Limits == nil {
					p.Limits = map[string]Limit{}
				}
				p.Limits[rest[1]], rest = Limit{a, b}, rest[3:]
			default:
				return fmt.Errorf("prio %s: unknown option %q", p.Name, rest[0])
			}
		}
		if !validName(p.Name) {
			return fmt.Errorf("prio name %q: lowercase letters, digits, - _ .", p.Name)
		}
		if c.Prio(p.Name) != nil {
			return fmt.Errorf("prio %s twice", p.Name)
		}
		c.Prios = append(c.Prios, p)
	case "rule":
		if len(t) != 5 || t[3] != "via" {
			return errors.New("rule member|tag|vlan|list VALUE via CHANNEL")
		}
		if t[1] != "member" && t[1] != "tag" && t[1] != "vlan" && t[1] != "list" {
			return fmt.Errorf("unknown rule match %q", t[1])
		}
		if t[1] == "vlan" {
			id, err := strconv.Atoi(t[2])
			if err != nil || id < 0 || id > 4094 {
				return errors.New("rule vlan wants an id 0…4094")
			}
			t[2] = strconv.Itoa(id)
		}
		c.Rules = append(c.Rules, Rule{Match: t[1], Value: t[2], Via: t[4]})
	case "default":
		switch {
		case len(t) == 2 && (t[1] == Direct || t[1] == Block):
			c.Default = t[1]
		case len(t) == 3 && t[1] == "via":
			c.Default = t[2]
		default:
			return errors.New("default direct | default block | default via PRIO|CHANNEL")
		}
	default:
		return fmt.Errorf("unknown command %q", t[0])
	}
	return nil
}

func (c *Config) target(s string) bool {
	return s == Direct || s == Block || c.Channel(s) != nil || c.Prio(s) != nil
}

// check resolves names and refuses loops in via and fail.
func (c *Config) check() error {
	for _, ch := range c.Channels {
		switch ch.UDP {
		case "", Direct, Block:
		case "proxy":
			if ch.Kind != KindSOCKS5 {
				return fmt.Errorf("channel %s: udp proxy needs socks5", ch.Name)
			}
		default:
			return fmt.Errorf("channel %s: udp %q (proxy, direct, block)", ch.Name, ch.UDP)
		}
		switch ch.Auth {
		case "", "auto", "mschapv2", "pap":
		default:
			return fmt.Errorf("channel %s: auth %q (auto, mschapv2, pap)", ch.Name, ch.Auth)
		}
		if k := ch.Check; k != nil {
			switch k.Kind {
			case "ping":
				if ch.Kind != KindDev {
					return fmt.Errorf("channel %s: ping checks need a kernel interface; use check tcp", ch.Name)
				}
				if k.Probe == "" {
					return fmt.Errorf("channel %s: check ping needs probe ADDRESS", ch.Name)
				}
			case "tcp":
				if _, _, err := net.SplitHostPort(k.Probe); err != nil {
					return fmt.Errorf("channel %s: check tcp needs probe HOST:PORT", ch.Name)
				}
			default:
				return fmt.Errorf("channel %s: check %q (ping, tcp)", ch.Name, k.Kind)
			}
			if k.Every > 3600 || k.Fails > 100 {
				return fmt.Errorf("channel %s: every is up to 3600 s, fails up to 100", ch.Name)
			}
		}
		if c.Prio(ch.Name) != nil {
			return fmt.Errorf("%s is both a channel and a prio", ch.Name)
		}
		if ch.Fail != "" && (ch.Fail != Direct && ch.Fail != Block && c.Channel(ch.Fail) == nil) {
			return fmt.Errorf("channel %s: fail %q is no channel", ch.Name, ch.Fail)
		}
		if ch.Via != "" {
			v := c.Channel(ch.Via)
			if v == nil {
				return fmt.Errorf("channel %s: via %q is no channel", ch.Name, ch.Via)
			}
			if ch.Kind != KindSOCKS5 && ch.Kind != KindHTTP {
				return fmt.Errorf("channel %s: only proxies go via another channel", ch.Name)
			}
		}
		seen := map[string]bool{}
		for x := &ch; x != nil && x.Via != ""; x = c.Channel(x.Via) {
			if seen[x.Name] {
				return fmt.Errorf("channel %s: via goes round in a circle", ch.Name)
			}
			seen[x.Name] = true
		}
		seen = map[string]bool{}
		for x := &ch; x != nil && x.Fail != ""; x = c.Channel(x.Fail) {
			if seen[x.Name] {
				return fmt.Errorf("channel %s: fail goes round in a circle", ch.Name)
			}
			seen[x.Name] = true
		}
	}
	for _, p := range c.Prios {
		if p.Last != Direct && p.Last != Block {
			return fmt.Errorf("prio %s: then %q (direct, block)", p.Name, p.Last)
		}
		seen := map[string]bool{}
		for _, u := range p.Use {
			if u != Direct && c.Channel(u) == nil {
				return fmt.Errorf("prio %s: %q is no channel", p.Name, u)
			}
			if seen[u] {
				return fmt.Errorf("prio %s: %s twice", p.Name, u)
			}
			seen[u] = true
		}
		for ch, l := range p.Limits {
			if !seen[ch] {
				return fmt.Errorf("prio %s: limit on %s, which is not in the list", p.Name, ch)
			}
			if l.In > 100000 || l.Out > 100000 {
				return fmt.Errorf("prio %s: limit on %s is over 100000 Mbit/s", p.Name, ch)
			}
		}
	}
	for _, r := range c.Rules {
		if !c.target(r.Via) {
			return fmt.Errorf("rule %s %s: %q is no channel", r.Match, r.Value, r.Via)
		}
	}
	if c.Default != "" && !c.target(c.Default) {
		return fmt.Errorf("default: %q is no channel", c.Default)
	}
	return nil
}

// Render writes c in the form Parse reads.
func Render(c *Config) string {
	var b strings.Builder
	b.WriteString("# Written by loft-panel: how members' traffic leaves this node.\n")
	for _, ch := range c.Channels {
		fmt.Fprintf(&b, "channel %s %s", ch.Name, ch.Kind)
		if ch.Kind == KindDev {
			fmt.Fprintf(&b, " %s", Quote(ch.Dev))
		} else {
			fmt.Fprintf(&b, " %s", Quote(ch.Addr))
		}
		opt := func(k, v string) {
			if v != "" {
				fmt.Fprintf(&b, " %s %s", k, Quote(v))
			}
		}
		opt("user", ch.User)
		opt("pass", ch.Pass)
		if ch.TLS {
			b.WriteString(" tls")
		}
		if ch.Insecure {
			b.WriteString(" insecure")
		}
		if ch.Off {
			b.WriteString(" off")
		}
		opt("auth", ch.Auth)
		opt("udp", ch.UDP)
		opt("via", ch.Via)
		opt("fail", ch.Fail)
		if k := ch.Check; k != nil {
			opt("check", k.Kind)
			opt("probe", k.Probe)
			if k.Every > 0 {
				fmt.Fprintf(&b, " every %d", k.Every)
			}
			if k.Fails > 0 {
				fmt.Fprintf(&b, " fails %d", k.Fails)
			}
		}
		b.WriteByte('\n')
	}
	for _, p := range c.Prios {
		last := p.Last
		if last == "" {
			last = Direct
		}
		fmt.Fprintf(&b, "prio %s %s then %s", p.Name, strings.Join(p.Use, " "), last)
		if p.Multi {
			b.WriteString(" multi")
		}
		for _, u := range p.Use {
			if l, ok := p.Limits[u]; ok && (l.In > 0 || l.Out > 0) {
				fmt.Fprintf(&b, " limit %s %d/%d", u, l.In, l.Out)
			}
		}
		b.WriteByte('\n')
	}
	for _, r := range c.Rules {
		fmt.Fprintf(&b, "rule %s %s via %s\n", r.Match, Quote(r.Value), r.Via)
	}
	switch c.Default {
	case "", Direct:
		b.WriteString("default direct\n")
	case Block:
		b.WriteString("default block\n")
	default:
		fmt.Fprintf(&b, "default via %s\n", c.Default)
	}
	return b.String()
}

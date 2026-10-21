// Package egress decides how a member's traffic leaves the node: directly
// (the server's own route), through a kernel interface (a WireGuard,
// OpenVPN or ZeroTier channel), through a SOCKS5 or HTTP(S) proxy, or
// through an SSTP session the node holds itself. Rules pick a channel by
// member, member tag or named address list; each channel names what to do
// when it fails (another channel, direct, block).
//
// egress.conf, next to network.conf:
//
//	channel wg-nl dev wg-nl fail wg-fi
//	channel px-office socks5 192.0.2.40:1080 user loft pass "s3cret" udp proxy via wg-nl fail direct
//	channel web http 192.0.2.41:3128 tls fail block
//	channel office sstp vpn.example.org:443 user office pass x auth mschapv2 fail direct
//	rule member bob via px-office
//	rule tag office via wg-nl
//	rule list blocked-ru via wg-nl
//	default direct
package egress

import (
	"errors"
	"fmt"
	"net"
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
	Fail string
}

// Rule sends matching traffic to Via (a channel, direct or block).
type Rule struct {
	Match string // member | tag | list
	Value string
	Via   string
}

// Config is a parsed egress.conf.
type Config struct {
	Channels []Channel
	Rules    []Rule
	Default  string // a channel, direct or block; empty = direct
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
	case "rule":
		if len(t) != 5 || t[3] != "via" {
			return errors.New("rule member|tag|list VALUE via CHANNEL")
		}
		if t[1] != "member" && t[1] != "tag" && t[1] != "list" {
			return fmt.Errorf("unknown rule match %q", t[1])
		}
		c.Rules = append(c.Rules, Rule{Match: t[1], Value: t[2], Via: t[4]})
	case "default":
		switch {
		case len(t) == 2 && (t[1] == Direct || t[1] == Block):
			c.Default = t[1]
		case len(t) == 3 && t[1] == "via":
			c.Default = t[2]
		default:
			return errors.New("default direct | default block | default via CHANNEL")
		}
	default:
		return fmt.Errorf("unknown command %q", t[0])
	}
	return nil
}

func (c *Config) target(s string) bool { return s == Direct || s == Block || c.Channel(s) != nil }

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
		if ch.Fail != "" && !c.target(ch.Fail) {
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

package adm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"

	"golang.org/x/crypto/curve25519"

	"github.com/a3app/loft/internal/config"
	"github.com/a3app/loft/internal/egress"
	"github.com/a3app/loft/internal/netconf"
	"github.com/a3app/loft/server/panel/admproto"
)

// Upstream channels. The panel's view (with secrets) is
// /etc/loft-panel/channels.json; from it the helper writes the kernel side
// (wg-quick, openvpn-client, zerotier-cli), a routing table per channel
// (default dev CHANNEL in its table, ip rule oif CHANNEL lookup it, so only
// sockets the node binds to the channel use it: the server's own default
// route never moves), and the node's egress.conf, then reloads the node.

const (
	tableBase = 1000  // channel tables: 1000, 1001, …
	prefBase  = 10000 // rule priorities: two per table, before main (32766)
	// ChannelProto marks main-table routes a channel brings (WireGuard AllowedIPs).
	ChannelProto = "201"
	xrayPortBase = 10800
)

type chanStore struct {
	Channels []admproto.Channel `json:"channels"`
	Prios    []admproto.Prio    `json:"prios,omitempty"`
	Rules    admproto.ExitRules `json:"rules"`
	seeded   bool               `json:"-"` // ensureDefaultPrio just created the leftover list
}

func (o *Ops) chanFile() string {
	return filepath.Join(firstNonEmpty(o.PanelEtc, "/etc/loft-panel"), "channels.json")
}
func (o *Ops) egressFile() string { return filepath.Join(o.Dir, "egress.conf") }

func (o *Ops) readChans() (chanStore, error) {
	var s chanStore
	b, err := os.ReadFile(o.chanFile())
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return s, err
	}
	if err == nil {
		if err := json.Unmarshal(b, &s); err != nil {
			return s, err
		}
	}
	migrate(&s)
	return s, nil
}

func (o *Ops) writeChans(s chanStore) error {
	b, _ := json.MarshalIndent(s, "", "  ")
	if err := os.MkdirAll(filepath.Dir(o.chanFile()), 0o755); err != nil {
		return err
	}
	return storeWrite(o.chanFile(), append(b, '\n'), 0o600)
}

func find(s chanStore, name string) int {
	return slices.IndexFunc(s.Channels, func(c admproto.Channel) bool { return c.Name == name })
}

var chanName = regexp.MustCompile(`^[a-z][a-z0-9-]{0,11}$`)

func wgKey(k string) bool {
	b, err := base64.StdEncoding.DecodeString(k)
	return err == nil && len(b) == 32
}

// WGPublic derives a WireGuard public key.
func WGPublic(priv string) string {
	b, err := base64.StdEncoding.DecodeString(priv)
	if err != nil || len(b) != 32 {
		return ""
	}
	pub, err := curve25519.X25519(b, curve25519.Basepoint)
	if err != nil {
		return ""
	}
	return base64.StdEncoding.EncodeToString(pub)
}

func hostPort(s string, defPort string) (string, error) {
	if _, _, err := net.SplitHostPort(s); err != nil {
		if defPort == "" || strings.Contains(s, ":") && !strings.HasPrefix(s, "[") {
			return "", fmt.Errorf("%q is not host:port", s)
		}
		s = net.JoinHostPort(s, defPort)
	}
	h, p, _ := net.SplitHostPort(s)
	if n, err := strconv.Atoi(p); err != nil || n < 1 || n > 65535 || h == "" || strings.ContainsAny(h, " /\"'\\") {
		return "", fmt.Errorf("%q is not host:port", s)
	}
	return s, nil
}

func cidrs(list []string) ([]string, error) {
	var out []string
	for _, x := range list {
		x = strings.TrimSpace(x)
		if x == "" {
			continue
		}
		p, err := netip.ParsePrefix(x)
		if err != nil {
			return nil, fmt.Errorf("%q is not a subnet", x)
		}
		out = append(out, p.Masked().String())
	}
	return out, nil
}

// ifAddrs are an interface's own addresses with their prefix (not masked).
func ifAddrs(list []string) ([]string, error) {
	var out []string
	for _, x := range list {
		x = strings.TrimSpace(x)
		if x == "" {
			continue
		}
		p, err := netip.ParsePrefix(x)
		if err != nil || p.Addr() == p.Masked().Addr() && p.Bits() < p.Addr().BitLen()-1 {
			return nil, fmt.Errorf("%q is not an address with a prefix", x)
		}
		out = append(out, p.String())
	}
	return out, nil
}

// sysName is the next free system name for a channel type: wg0, ovpn1, …
func sysName(typ string, s chanStore) string {
	prefix := map[string]string{admproto.ChanWG: "wg", admproto.ChanOVPN: "ovpn", admproto.ChanZT: "zt", admproto.ChanSSTP: "sstp", admproto.ChanPX: "px", admproto.ChanXR: "xr"}[typ]
	if prefix == "" {
		prefix = "ch"
	}
	for i := 0; ; i++ {
		n := prefix + strconv.Itoa(i)
		if find(s, n) < 0 {
			return n
		}
	}
}

// OpenVPN directives a .ovpn may not carry: they run programs, touch files
// or change what the helper sets (device, routes, scripts).
var ovpnRefused = map[string]bool{"up": true, "down": true, "route-up": true, "route-pre-down": true, "ipchange": true,
	"client-connect": true, "client-disconnect": true, "learn-address": true, "auth-user-pass-verify": true,
	"tls-verify": true, "plugin": true, "script-security": true, "iproute": true, "config": true, "cd": true,
	"chroot": true, "writepid": true, "log": true, "log-append": true, "status": true, "management": true,
	"dev": true, "dev-type": true, "dev-node": true, "daemon": true, "user": true, "group": true, "auth-user-pass": true,
	"route-noexec": true, "route-nopull": true, "redirect-gateway": true, "tmp-dir": true, "setenv": true, "machine-readable-output": true}

func ovpnDirective(line string) string {
	l := strings.TrimSpace(line)
	if l == "" || l[0] == '#' || l[0] == ';' || l[0] == '<' {
		return ""
	}
	return strings.ToLower(strings.Fields(strings.TrimPrefix(l, "--"))[0])
}

// checkChannel validates c and fills what the helper owns, keeping
// secrets of old when the page sent none.
func checkChannel(c *admproto.Channel, old *admproto.Channel, s chanStore) error {
	if old == nil && c.Name == "" {
		c.Name = sysName(c.Type, s)
	}
	if !chanName.MatchString(c.Name) {
		return fmt.Errorf("channel name %q: a lower-case latin letter, then lower-case letters, digits or -, up to 12", c.Name)
	}
	c.Title = strings.Join(strings.Fields(c.Title), " ")
	if c.Title == "" {
		c.Title = c.Name
	}
	if utf8.RuneCountInString(c.Title) > 40 {
		return errors.New("connection name: up to 40 characters")
	}
	for _, x := range s.Channels {
		if x.Name != c.Name && strings.EqualFold(firstNonEmpty(x.Title, x.Name), c.Title) {
			return fmt.Errorf("a connection named %q exists", c.Title)
		}
	}
	if old != nil && old.Type != c.Type {
		return errors.New("a channel keeps its type: delete it and add a new one")
	}
	c.Fail = "" // failover lives in priority lists now
	if err := checkCheck(c); err != nil {
		return err
	}
	if c.Via != "" && (c.Via == c.Name || find(s, c.Via) < 0) {
		return fmt.Errorf("connect through: %q is no channel", c.Via)
	}
	switch c.Type {
	case admproto.ChanWG:
		w := c.WG
		if w == nil {
			return errors.New("missing WireGuard settings")
		}
		if w.PrivateKey == "" && old != nil && old.WG != nil {
			w.PrivateKey = old.WG.PrivateKey
		}
		if !wgKey(w.PrivateKey) {
			return errors.New("private key: 44 characters of base64")
		}
		addrs, err := ifAddrs(strings.Split(w.Address, ","))
		if err != nil || len(addrs) == 0 {
			return errors.New("address: like 10.8.0.2/24")
		}
		w.Address = strings.Join(addrs, ", ")
		if w.ListenPort < 0 || w.ListenPort > 65535 || (w.MTU != 0 && (w.MTU < 1280 || w.MTU > 9000)) {
			return errors.New("listen port 1–65535, MTU 1280–9000")
		}
		if len(w.Peers) == 0 {
			return errors.New("a WireGuard channel needs a peer")
		}
		for i := range w.Peers {
			p := &w.Peers[i]
			if !wgKey(p.PublicKey) {
				return fmt.Errorf("peer %d: public key, 44 characters of base64", i+1)
			}
			if p.PSK == "" && old != nil && old.WG != nil && i < len(old.WG.Peers) && old.WG.Peers[i].PublicKey == p.PublicKey {
				p.PSK = old.WG.Peers[i].PSK
			}
			if p.PSK != "" && !wgKey(p.PSK) {
				return fmt.Errorf("peer %d: preshared key, 44 characters of base64", i+1)
			}
			if p.Endpoint != "" {
				if p.Endpoint, err = hostPort(p.Endpoint, "51820"); err != nil {
					return fmt.Errorf("peer %d: %v", i+1, err)
				}
			}
			if p.AllowedIPs, err = cidrs(p.AllowedIPs); err != nil || len(p.AllowedIPs) == 0 {
				return fmt.Errorf("peer %d: allowed subnets, like 0.0.0.0/0", i+1)
			}
			if p.Keepalive < 0 || p.Keepalive > 65535 || strings.ContainsAny(p.Name, "\n\r") {
				return fmt.Errorf("peer %d: keepalive 0–65535", i+1)
			}
		}
		w.PublicKey = WGPublic(w.PrivateKey)
		c.Dev = c.Name
	case admproto.ChanSSTP:
		x := c.SSTP
		if x == nil {
			return errors.New("missing SSTP settings")
		}
		var err error
		if x.Server, err = hostPort(x.Server, "443"); err != nil {
			return fmt.Errorf("server: %v", err)
		}
		if x.Password == "" && old != nil && old.SSTP != nil {
			x.Password = old.SSTP.Password
		}
		if x.User == "" || x.Password == "" {
			return errors.New("SSTP needs a user name and a password")
		}
		if !slices.Contains([]string{"", "auto", "mschapv2", "pap"}, x.Auth) {
			return errors.New("authentication: auto, MS-CHAP v2 or PAP")
		}
		if c.Via != "" {
			return errors.New("SSTP connects directly")
		}
	case admproto.ChanOVPN:
		x := c.OVPN
		if x == nil {
			return errors.New("missing OpenVPN settings")
		}
		if len(x.Config) > 64<<10 || !strings.Contains(x.Config, "remote") {
			return errors.New("OpenVPN configuration: paste the .ovpn file (with a remote line)")
		}
		for _, ln := range strings.Split(x.Config, "\n") {
			if d := ovpnDirective(ln); ovpnRefused[d] {
				return fmt.Errorf("the .ovpn line %q is set by the panel or not allowed: remove it", strings.TrimSpace(ln))
			}
		}
		if x.Password == "" && old != nil && old.OVPN != nil {
			x.Password = old.OVPN.Password
		}
		if (x.User == "") != (x.Password == "") || strings.ContainsAny(x.User+x.Password, "\n\r") {
			return errors.New("OpenVPN login: both a user name and a password, or neither")
		}
		c.Dev = c.Name
	case admproto.ChanZT:
		x := c.ZT
		if x == nil || !regexp.MustCompile(`^[0-9a-f]{16}$`).MatchString(strings.ToLower(x.Network)) {
			return errors.New("network ID: 16 hexadecimal characters")
		}
		x.Network = strings.ToLower(x.Network)
		if old != nil && old.ZT != nil && old.ZT.Network == x.Network {
			c.Dev = old.Dev
		}
		if c.Via != "" {
			return errors.New("ZeroTier connects directly")
		}
	case admproto.ChanPX:
		x := c.PX
		if x == nil || !slices.Contains([]string{"http", "https", "socks5"}, x.Kind) {
			return errors.New("proxy type: HTTP, HTTPS or SOCKS5")
		}
		var err error
		if x.Addr, err = hostPort(x.Addr, ""); err != nil {
			return fmt.Errorf("proxy address: %v", err)
		}
		if x.Password == "" && old != nil && old.PX != nil && x.User != "" {
			x.Password = old.PX.Password
		}
		if x.User == "" {
			x.Password = ""
		}
		if !slices.Contains([]string{"", "direct", "proxy", "block"}, x.UDP) || (x.UDP == "proxy" && x.Kind != "socks5") {
			return errors.New("UDP: direct, block, or through the proxy for SOCKS5")
		}
	case admproto.ChanXR:
		x := c.XR
		if x == nil {
			return errors.New("missing Xray settings")
		}
		if _, err := ParseXrayLink(x.Link); err != nil {
			return err
		}
		if old != nil && old.XR != nil {
			x.Port = old.XR.Port
		}
	default:
		return fmt.Errorf("unknown channel type %q", c.Type)
	}
	if c.Accept && c.Type != admproto.ChanWG && c.Type != admproto.ChanOVPN && c.Type != admproto.ChanZT {
		return errors.New("only WireGuard, OpenVPN and ZeroTier channels take incoming connections")
	}
	if old != nil {
		c.Table = old.Table
		if c.Type == admproto.ChanZT && c.Dev == "" {
			c.Dev = old.Dev
		}
	}
	return nil
}

func redact(c admproto.Channel) admproto.Channel {
	if c.WG != nil {
		w := *c.WG
		w.HasKey, w.PrivateKey = w.PrivateKey != "", ""
		w.Peers = slices.Clone(w.Peers)
		for i := range w.Peers {
			w.Peers[i].HasPSK, w.Peers[i].PSK = w.Peers[i].PSK != "", ""
		}
		c.WG = &w
	}
	if c.SSTP != nil {
		x := *c.SSTP
		x.HasPassword, x.Password = x.Password != "", ""
		c.SSTP = &x
	}
	if c.OVPN != nil {
		x := *c.OVPN
		x.HasPassword, x.Password = x.Password != "", ""
		c.OVPN = &x
	}
	if c.PX != nil {
		x := *c.PX
		x.HasPassword, x.Password = x.Password != "", ""
		c.PX = &x
	}
	return c
}

// Channels is the External channels tab.
func (o *Ops) Channels(ctx context.Context) (admproto.ChannelsView, error) {
	o.chanMu.Lock()
	s, err := o.readChans()
	if err != nil {
		o.chanMu.Unlock()
		return admproto.ChannelsView{}, err
	}
	if s.seeded {
		if aerr := o.applyChans(ctx, s, s); aerr == nil {
			s.seeded = false
		}
	}
	o.chanMu.Unlock()
	v := admproto.ChannelsView{Rules: s.Rules, Channels: []admproto.Channel{}, Prios: s.Prios}
	if v.Prios == nil {
		v.Prios = []admproto.Prio{}
	}
	for _, c := range s.Channels {
		v.Channels = append(v.Channels, redact(c))
	}
	if v.Rules.Rules == nil {
		v.Rules.Rules = []admproto.ExitRule{}
	}
	v.Status = o.channelStatus(ctx, s)
	if c, err := config.LoadServer(o.nodeConf()); err == nil {
		for k := range c.Lists {
			v.Lists = append(v.Lists, k)
		}
		sort.Strings(v.Lists)
	}
	return v, nil
}

// ChannelSave adds or changes a channel and applies it.
func (o *Ops) ChannelSave(ctx context.Context, c admproto.Channel) (admproto.ChannelsView, error) {
	o.chanMu.Lock()
	s, err := o.readChans()
	if err != nil {
		o.chanMu.Unlock()
		return admproto.ChannelsView{}, err
	}
	i := find(s, c.Name)
	var old *admproto.Channel
	if i >= 0 {
		cp := s.Channels[i]
		old = &cp
	}
	if err := checkChannel(&c, old, s); err != nil {
		o.chanMu.Unlock()
		return admproto.ChannelsView{}, err
	}
	if err := o.overlapsOverlay(c); err != nil {
		o.chanMu.Unlock()
		return admproto.ChannelsView{}, err
	}
	if c.Table == 0 {
		c.Table = freeTable(s)
	}
	if c.Type == admproto.ChanXR && c.XR.Port == 0 {
		c.XR.Port = freeXrayPort(s)
	}
	next := s
	next.Channels = slices.Clone(s.Channels)
	next.Prios = slices.Clone(s.Prios)
	if i >= 0 {
		next.Channels[i] = c
	} else {
		next.Channels = append(next.Channels, c)
		autoJoin(&next, c)
	}
	if !c.Exit {
		dropFromPrios(&next, c.Name)
	}
	err = o.applyChans(ctx, s, next)
	o.chanMu.Unlock()
	if err != nil {
		return admproto.ChannelsView{}, err
	}
	return o.Channels(ctx)
}

func usedByRules(s chanStore, name string) bool {
	if s.Rules.Default == name {
		return true
	}
	return slices.ContainsFunc(s.Rules.Rules, func(r admproto.ExitRule) bool { return r.Via == name })
}

func freeTable(s chanStore) int {
	for t := tableBase; ; t++ {
		if !slices.ContainsFunc(s.Channels, func(c admproto.Channel) bool { return c.Table == t }) {
			return t
		}
	}
}

func freeXrayPort(s chanStore) int {
	for p := xrayPortBase; ; p++ {
		if !slices.ContainsFunc(s.Channels, func(c admproto.Channel) bool { return c.XR != nil && c.XR.Port == p }) {
			return p
		}
	}
}

// ChannelDelete removes a channel nothing else refers to.
func (o *Ops) ChannelDelete(ctx context.Context, name string) (admproto.ChannelsView, error) {
	o.chanMu.Lock()
	s, err := o.readChans()
	if err != nil {
		o.chanMu.Unlock()
		return admproto.ChannelsView{}, err
	}
	i := find(s, name)
	if i < 0 {
		o.chanMu.Unlock()
		return admproto.ChannelsView{}, fmt.Errorf("no channel %q", name)
	}
	for _, c := range s.Channels {
		if c.Via == name {
			o.chanMu.Unlock()
			return admproto.ChannelsView{}, fmt.Errorf("channel %s connects through it", c.Name)
		}
	}
	next := s
	next.Channels = slices.Delete(slices.Clone(s.Channels), i, i+1)
	next.Prios = slices.Clone(s.Prios)
	dropFromPrios(&next, name)
	err = o.applyChans(ctx, s, next)
	o.chanMu.Unlock()
	if err != nil {
		return admproto.ChannelsView{}, err
	}
	return o.Channels(ctx)
}

// ChannelSwitch turns a channel on or off.
func (o *Ops) ChannelSwitch(ctx context.Context, req admproto.ChannelSwitch) (admproto.ChannelsView, error) {
	o.chanMu.Lock()
	s, err := o.readChans()
	if err != nil {
		o.chanMu.Unlock()
		return admproto.ChannelsView{}, err
	}
	i := find(s, req.Name)
	if i < 0 {
		o.chanMu.Unlock()
		return admproto.ChannelsView{}, fmt.Errorf("no channel %q", req.Name)
	}
	next := s
	next.Channels = slices.Clone(s.Channels)
	next.Channels[i].On = req.On
	err = o.applyChans(ctx, s, next)
	o.chanMu.Unlock()
	if err != nil {
		return admproto.ChannelsView{}, err
	}
	return o.Channels(ctx)
}

// ChannelRules replaces the exit rules.
func (o *Ops) ChannelRules(ctx context.Context, r admproto.ExitRules) (admproto.ChannelsView, error) {
	o.chanMu.Lock()
	s, err := o.readChans()
	if err != nil {
		o.chanMu.Unlock()
		return admproto.ChannelsView{}, err
	}
	ok := func(v string) error {
		if v == egress.Direct || v == egress.Block || findPrio(s, v) >= 0 {
			return nil
		}
		return fmt.Errorf("%q is no priority list", v)
	}
	if len(r.Rules) > 200 {
		o.chanMu.Unlock()
		return admproto.ChannelsView{}, errors.New("at most 200 rules")
	}
	for i, x := range r.Rules {
		if !slices.Contains([]string{"member", "tag", "vlan", "list"}, x.Match) || x.Value == "" || strings.ContainsAny(x.Value, "\n\r\"") {
			o.chanMu.Unlock()
			return admproto.ChannelsView{}, fmt.Errorf("rule %d: match a member, a tag, a vlan or a list", i+1)
		}
		if x.Match == "vlan" {
			id, err := strconv.Atoi(x.Value)
			if err != nil || id < 0 || id > 4094 {
				o.chanMu.Unlock()
				return admproto.ChannelsView{}, fmt.Errorf("rule %d: vlan id 0…4094", i+1)
			}
			r.Rules[i].Value = strconv.Itoa(id)
		}
		if err := ok(x.Via); err != nil {
			o.chanMu.Unlock()
			return admproto.ChannelsView{}, fmt.Errorf("rule %d: %v", i+1, err)
		}
	}
	if r.Default == "" || r.Default == egress.Direct {
		r.Default = defaultPrioID
	}
	if err := ok(r.Default); err != nil {
		o.chanMu.Unlock()
		return admproto.ChannelsView{}, fmt.Errorf("the rest: %v", err)
	}
	next := s
	next.Rules = r
	err = o.applyChans(ctx, s, next)
	o.chanMu.Unlock()
	if err != nil {
		return admproto.ChannelsView{}, err
	}
	return o.Channels(ctx)
}

// EgressConf is what the node gets from the channels and rules.
func EgressConf(s chanStore) (string, error) {
	c := &egress.Config{Default: s.Rules.Default}
	for _, ch := range s.Channels {
		e := egress.Channel{Name: ch.Name, Off: !ch.On}
		if k := ch.Check; k != nil {
			e.Check = &egress.Check{Kind: k.Kind, Probe: k.Probe, Every: k.Every, Fails: k.Fails}
		}
		switch ch.Type {
		case admproto.ChanWG, admproto.ChanOVPN, admproto.ChanZT:
			e.Kind, e.Dev = egress.KindDev, firstNonEmpty(ch.Dev, "loft-none")
		case admproto.ChanSSTP:
			e.Kind, e.Addr, e.User, e.Pass, e.Auth, e.Insecure = egress.KindSSTP, ch.SSTP.Server, ch.SSTP.User, ch.SSTP.Password, ch.SSTP.Auth, ch.SSTP.Insecure
		case admproto.ChanPX:
			e.Addr, e.User, e.Pass, e.UDP, e.Via = ch.PX.Addr, ch.PX.User, ch.PX.Password, ch.PX.UDP, ch.Via
			e.Kind = egress.KindHTTP
			switch ch.PX.Kind {
			case "socks5":
				e.Kind = egress.KindSOCKS5
			case "https":
				e.TLS = true
			}
		case admproto.ChanXR:
			// Xray listens on a local SOCKS port and does the chaining itself.
			e.Kind, e.Addr, e.UDP = egress.KindSOCKS5, "127.0.0.1:"+strconv.Itoa(ch.XR.Port), "proxy"
		}
		c.Channels = append(c.Channels, e)
	}
	for _, p := range s.Prios {
		e := egress.Prio{Name: p.ID, Use: p.Use, Last: p.Last, Multi: p.Multi}
		for ch, l := range p.Limits {
			if e.Limits == nil {
				e.Limits = map[string]egress.Limit{}
			}
			e.Limits[ch] = egress.Limit{In: l.In, Out: l.Out}
		}
		c.Prios = append(c.Prios, e)
	}
	for _, r := range s.Rules.Rules {
		c.Rules = append(c.Rules, egress.Rule{Match: r.Match, Value: r.Value, Via: r.Via})
	}
	text := egress.Render(c)
	if _, err := egress.Parse(text); err != nil { // the node must take what we write
		return "", err
	}
	return text, nil
}

// applyChans moves the server from have to want: kernel side, egress.conf,
// node reload, routing tables. On a failure before the node reload the
// old files are put back.
func (o *Ops) applyChans(ctx context.Context, have, want chanStore) error {
	conf, err := EgressConf(want)
	if err != nil {
		return err
	}
	for _, c := range have.Channels {
		if i := find(want, c.Name); i < 0 || want.Channels[i].Type != c.Type {
			o.chanDown(ctx, c, true)
		}
	}
	for _, c := range want.Channels {
		var prev *admproto.Channel
		if i := find(have, c.Name); i >= 0 {
			prev = &have.Channels[i]
		}
		if err := o.chanUp(ctx, c, prev); err != nil {
			return fmt.Errorf("%s: %v", c.Name, err)
		}
	}
	if err := o.writeChans(want); err != nil {
		return err
	}
	if err := o.writeXray(ctx, want); err != nil {
		return err
	}
	old, _ := os.ReadFile(o.egressFile())
	if string(old) != conf {
		if err := o.writeOwned(o.egressFile(), []byte(conf), 0o640, o.UID, o.GID); err != nil {
			return err
		}
		if err := o.applySettled(ctx); err != nil {
			return err
		}
	}
	// SSTP/proxy/Xray live in the node; kernel routes are unchanged, so a
	// host snapshot (ZeroTier can sit for seconds) would only stall the save.
	if kernelSide(have) != kernelSide(want) {
		o.dropSnap()
		o.Reconcile(ctx)
	}
	return nil
}

// kernelSide is the WireGuard / OpenVPN / ZeroTier set Reconcile owns.
func kernelSide(s chanStore) string {
	var b strings.Builder
	for _, c := range s.Channels {
		switch c.Type {
		case admproto.ChanWG, admproto.ChanOVPN, admproto.ChanZT:
			net := ""
			if c.ZT != nil {
				net = c.ZT.Network
			}
			fmt.Fprintf(&b, "%s %s %t %s %d %s\n", c.Name, c.Type, c.On, c.Dev, c.Table, net)
		}
	}
	return b.String()
}

// putFile writes a file outside the helper's sandbox (/etc/wireguard,
// /etc/openvpn are read-only in here): through the state directory and
// install(1) on the host.
func (o *Ops) putFile(ctx context.Context, dst string, data []byte, mode os.FileMode) error {
	if o.Exec != nil { // tests: Root is the host
		p := filepath.Join(o.Root, dst)
		os.MkdirAll(filepath.Dir(p), 0o755)
		return os.WriteFile(p, data, mode)
	}
	if err := os.MkdirAll(o.stateDir(), 0o700); err != nil {
		return err
	}
	tmp := filepath.Join(o.stateDir(), "put."+filepath.Base(dst))
	if err := os.WriteFile(tmp, data, 0o600); err != nil {
		return err
	}
	defer os.Remove(tmp)
	_, err := o.host(ctx, "install", "-D", "-m", fmt.Sprintf("%04o", mode), tmp, dst)
	return err
}

func (o *Ops) rmFile(ctx context.Context, path string) {
	if o.Exec != nil {
		os.Remove(filepath.Join(o.Root, path))
		return
	}
	o.host(ctx, "rm", "-f", path)
}

func wgConf(c admproto.Channel) string {
	w := c.WG
	var b strings.Builder
	fmt.Fprintf(&b, "# Written by loft-panel (channel %s); edits are replaced.\n[Interface]\nPrivateKey = %s\nAddress = %s\n", c.Name, w.PrivateKey, w.Address)
	if w.ListenPort > 0 {
		fmt.Fprintf(&b, "ListenPort = %d\n", w.ListenPort)
	}
	if w.MTU > 0 {
		fmt.Fprintf(&b, "MTU = %d\n", w.MTU)
	}
	// No routes from wg-quick: the panel's table and rule send only the
	// node's sockets bound to this interface; subnets go in the main table.
	b.WriteString("Table = off\n")
	for _, p := range w.Peers {
		b.WriteString("\n[Peer]\n")
		if p.Name != "" {
			fmt.Fprintf(&b, "# %s\n", p.Name)
		}
		fmt.Fprintf(&b, "PublicKey = %s\n", p.PublicKey)
		if p.PSK != "" {
			fmt.Fprintf(&b, "PresharedKey = %s\n", p.PSK)
		}
		if p.Endpoint != "" {
			fmt.Fprintf(&b, "Endpoint = %s\n", p.Endpoint)
		}
		fmt.Fprintf(&b, "AllowedIPs = %s\n", strings.Join(p.AllowedIPs, ", "))
		if p.Keepalive > 0 {
			fmt.Fprintf(&b, "PersistentKeepalive = %d\n", p.Keepalive)
		}
	}
	return b.String()
}

func ovpnConf(c admproto.Channel) string {
	x := c.OVPN
	var b strings.Builder
	fmt.Fprintf(&b, "# Written by loft-panel (channel %s); edits are replaced.\n", c.Name)
	b.WriteString(strings.TrimRight(strings.ReplaceAll(x.Config, "\r\n", "\n"), "\n"))
	fmt.Fprintf(&b, "\n\n# Set by loft-panel.\ndev %s\ndev-type tun\nscript-security 1\npull-filter ignore \"redirect-gateway\"\npull-filter ignore \"route-gateway\"\n", c.Name)
	if !x.Routes {
		b.WriteString("route-nopull\n")
	}
	if x.User != "" {
		fmt.Fprintf(&b, "auth-user-pass /etc/openvpn/client/%s.auth\n", c.Name)
	}
	return b.String()
}

func unitOf(c admproto.Channel) string {
	switch c.Type {
	case admproto.ChanWG:
		return "wg-quick@" + c.Name + ".service"
	case admproto.ChanOVPN:
		return "openvpn-client@" + c.Name + ".service"
	}
	return ""
}

// chanUp writes and starts (or stops) the kernel side of c.
func (o *Ops) chanUp(ctx context.Context, c admproto.Channel, prev *admproto.Channel) error {
	var files map[string]string
	switch c.Type {
	case admproto.ChanWG:
		files = map[string]string{"/etc/wireguard/" + c.Name + ".conf": wgConf(c)}
	case admproto.ChanOVPN:
		files = map[string]string{"/etc/openvpn/client/" + c.Name + ".conf": ovpnConf(c)}
		if c.OVPN.User != "" {
			files["/etc/openvpn/client/"+c.Name+".auth"] = c.OVPN.User + "\n" + c.OVPN.Password + "\n"
		}
	case admproto.ChanZT:
		if prev != nil && prev.ZT != nil && prev.ZT.Network != c.ZT.Network && prev.On {
			o.host(ctx, "zerotier-cli", "leave", prev.ZT.Network)
		}
		if !c.On {
			o.host(ctx, "zerotier-cli", "leave", c.ZT.Network)
			return nil
		}
		if _, err := o.host(ctx, "zerotier-cli", "join", c.ZT.Network); err != nil {
			return fmt.Errorf("zerotier-cli join: %v (is the ZeroTier client package installed and running?)", err)
		}
		m := "0"
		if c.ZT.Managed {
			m = "1"
		}
		for _, kv := range []string{"allowManaged=" + m, "allowGlobal=0", "allowDefault=0", "allowDNS=0"} {
			o.host(ctx, "zerotier-cli", "set", c.ZT.Network, kv)
		}
		return nil
	default:
		return nil // the node holds SSTP and proxies; Xray is written with writeXray
	}
	changed := prev == nil || prev.On != c.On
	for path, body := range files {
		mode := os.FileMode(0o600)
		if err := o.putFile(ctx, path, []byte(body), mode); err != nil {
			return err
		}
		changed = true
	}
	unit := unitOf(c)
	if !c.On {
		o.Systemctl(ctx, "disable", "--now", unit)
		return nil
	}
	if changed {
		if out, err := o.Systemctl(ctx, "enable", unit); err != nil {
			return fmt.Errorf("systemctl enable %s: %v %s", unit, err, out)
		}
		if out, err := o.Systemctl(ctx, "restart", unit); err != nil {
			return fmt.Errorf("%s did not start: %v %s", unit, err, out)
		}
	}
	return nil
}

// chanDown stops c and, when gone, removes its files and routing.
func (o *Ops) chanDown(ctx context.Context, c admproto.Channel, gone bool) {
	switch c.Type {
	case admproto.ChanWG, admproto.ChanOVPN:
		o.Systemctl(ctx, "disable", "--now", unitOf(c))
		if gone && c.Type == admproto.ChanWG {
			o.rmFile(ctx, "/etc/wireguard/"+c.Name+".conf")
		}
		if gone && c.Type == admproto.ChanOVPN {
			o.rmFile(ctx, "/etc/openvpn/client/"+c.Name+".conf")
			o.rmFile(ctx, "/etc/openvpn/client/"+c.Name+".auth")
		}
	case admproto.ChanZT:
		o.host(ctx, "zerotier-cli", "leave", c.ZT.Network)
	}
	if gone && c.Table > 0 {
		o.host(ctx, "ip", "route", "flush", "table", strconv.Itoa(c.Table))
	}
}

type ipRule struct {
	Priority int    `json:"priority"`
	Src      string `json:"src"`
	SrcLen   int    `json:"srclen"`
	Oif      string `json:"oifname"`
	Table    string `json:"table"`
}

// Reconcile puts every kernel channel's table and rules in place and drops
// those of channels that are gone. Interfaces come and go (OpenVPN
// reconnects, ZeroTier waits for authorisation), so the helper runs it
// after every change and every 20 seconds.
func (o *Ops) Reconcile(ctx context.Context) {
	o.recMu.Lock()
	defer o.recMu.Unlock()
	s, err := o.readChans()
	if err != nil {
		return
	}
	snap := o.hostSnapshot(ctx, s)
	changed := false
	// ZeroTier picks its own interface name: learn it.
	for i, c := range s.Channels {
		if c.Type == admproto.ChanZT {
			if d := snap.ztDev[c.ZT.Network]; d != "" && d != c.Dev {
				s.Channels[i].Dev, changed = d, true
			}
		}
	}
	if changed || s.seeded {
		o.writeChans(s)
		if conf, err := EgressConf(s); err == nil {
			if old, _ := os.ReadFile(o.egressFile()); string(old) != conf {
				o.writeOwned(o.egressFile(), []byte(conf), 0o640, o.UID, o.GID)
				o.applySettled(ctx)
			}
		}
	}
	type want struct {
		pref  int
		oif   string
		src   string
		table int
	}
	var rules []want
	routes := map[string]bool{} // "dst dev" of channel routes in main
	for _, c := range s.Channels {
		if c.Table == 0 || c.Dev == "" || !c.On || !snap.devs[c.Dev] {
			continue
		}
		if c.Type != admproto.ChanWG && c.Type != admproto.ChanOVPN && c.Type != admproto.ChanZT {
			continue
		}
		t := strconv.Itoa(c.Table)
		o.host(ctx, "ip", "route", "replace", "default", "dev", c.Dev, "table", t)
		p := prefBase + 2*(c.Table-tableBase)
		rules = append(rules, want{pref: p, oif: c.Dev, table: c.Table})
		if c.Accept {
			for _, a := range snap.addrs[c.Dev] {
				rules = append(rules, want{pref: p + 1, src: a, table: c.Table})
			}
		}
		if c.Type == admproto.ChanWG {
			for _, peer := range c.WG.Peers {
				for _, dst := range peer.AllowedIPs {
					if strings.HasSuffix(dst, "/0") || strings.Contains(dst, ":") {
						continue
					}
					routes[dst+" "+c.Dev] = true
				}
			}
		}
	}
	for _, r := range snap.rules {
		if r.Priority < prefBase || r.Priority >= prefBase+2000 {
			continue
		}
		keep := slices.ContainsFunc(rules, func(w want) bool {
			return w.pref == r.Priority && strconv.Itoa(w.table) == r.Table && w.oif == r.Oif && (w.src == "" && r.Src == "" || w.src == r.Src)
		})
		if !keep {
			args := []string{"rule", "del", "pref", strconv.Itoa(r.Priority)}
			if r.Oif != "" {
				args = append(args, "oif", r.Oif)
			}
			if r.Src != "" {
				args = append(args, "from", r.Src)
			}
			o.host(ctx, "ip", args...)
		}
	}
	for _, w := range rules {
		have := slices.ContainsFunc(snap.rules, func(r ipRule) bool {
			return w.pref == r.Priority && strconv.Itoa(w.table) == r.Table && w.oif == r.Oif && (w.src == "" && r.Src == "" || w.src == r.Src)
		})
		if have {
			continue
		}
		args := []string{"rule", "add", "pref", strconv.Itoa(w.pref)}
		if w.oif != "" {
			args = append(args, "oif", w.oif)
		}
		if w.src != "" {
			args = append(args, "from", w.src)
		}
		o.host(ctx, "ip", append(args, "lookup", strconv.Itoa(w.table))...)
	}
	for _, r := range snap.chanRoutes {
		if !routes[r] {
			dst, dev, _ := strings.Cut(r, " ")
			o.host(ctx, "ip", "route", "del", dst, "dev", dev, "proto", ChannelProto)
		}
	}
	for r := range routes {
		if !slices.Contains(snap.chanRoutes, r) {
			dst, dev, _ := strings.Cut(r, " ")
			o.host(ctx, "ip", "route", "replace", dst, "dev", dev, "proto", ChannelProto)
		}
	}
}

// snapshot is the host's state in one command.
type snapshot struct {
	devs       map[string]bool
	addrs      map[string][]string // dev → IPv4 addresses
	stats      map[string][2]uint64
	rules      []ipRule
	chanRoutes []string // "dst dev" with proto 201
	ztDev      map[string]string
	zt         map[string]ztNet
	ztNode     string
	wg         map[string]wgDev
	units      map[string][2]string // unit → state, since
}

type ztNet struct {
	ID       string   `json:"nwid"`
	Name     string   `json:"name"`
	Status   string   `json:"status"`
	Dev      string   `json:"portDeviceName"`
	Assigned []string `json:"assignedAddresses"`
}

type wgDev struct {
	handshake int64
	endpoint  string
}

const snapFresh = 3 * time.Second

func snapKey(s chanStore) string {
	var b strings.Builder
	for _, c := range s.Channels {
		b.WriteString(c.Name)
		b.WriteByte('/')
		b.WriteString(c.Type)
		b.WriteByte('/')
		b.WriteString(unitOf(c))
		b.WriteByte(';')
	}
	return b.String()
}

func (o *Ops) dropSnap() {
	o.snapMu.Lock()
	o.snapAt = time.Time{}
	o.snapMu.Unlock()
}

func (o *Ops) hostSnapshot(ctx context.Context, s chanStore) snapshot {
	key := snapKey(s)
	o.snapMu.Lock()
	defer o.snapMu.Unlock()
	if time.Since(o.snapAt) < snapFresh && o.snapKey == key {
		return o.snap
	}
	sn := o.hostSnapshotRun(ctx, s)
	o.snap, o.snapAt, o.snapKey = sn, time.Now(), key
	return sn
}

func (o *Ops) hostSnapshotRun(ctx context.Context, s chanStore) snapshot {
	sn := snapshot{devs: map[string]bool{}, addrs: map[string][]string{}, stats: map[string][2]uint64{}, ztDev: map[string]string{},
		zt: map[string]ztNet{}, wg: map[string]wgDev{}, units: map[string][2]string{}}
	var units []string
	hasZT := false
	for _, c := range s.Channels {
		if u := unitOf(c); u != "" {
			units = append(units, u)
		}
		hasZT = hasZT || c.Type == admproto.ChanZT
	}
	script := "ip -j -s link show; echo @@; ip -j -4 addr show; echo @@; ip -j rule show; echo @@; ip -j route show proto " + ChannelProto +
		"; echo @@; wg show all dump 2>/dev/null; echo @@; "
	if hasZT {
		script += "timeout -k 1 2 zerotier-cli -j listnetworks 2>/dev/null; echo @@; timeout -k 1 2 zerotier-cli -j info 2>/dev/null; echo @@; "
	} else {
		script += "echo; echo @@; echo; echo @@; "
	}
	if len(units) > 0 {
		script += "systemctl show -p Id,ActiveState,ActiveEnterTimestamp " + strings.Join(units, " ")
	}
	out, err := o.host(ctx, "sh", "-c", script)
	if err != nil && out == "" {
		return sn
	}
	part := strings.Split(out, "@@")
	for len(part) < 8 {
		part = append(part, "")
	}
	var links []struct {
		IfName string `json:"ifname"`
		Stats  struct {
			RX struct {
				Bytes uint64 `json:"bytes"`
			} `json:"rx"`
			TX struct {
				Bytes uint64 `json:"bytes"`
			} `json:"tx"`
		} `json:"stats64"`
	}
	json.Unmarshal([]byte(part[0]), &links)
	for _, l := range links {
		sn.devs[l.IfName] = true
		sn.stats[l.IfName] = [2]uint64{l.Stats.TX.Bytes, l.Stats.RX.Bytes}
	}
	var addrs []ipAddr
	json.Unmarshal([]byte(part[1]), &addrs)
	for _, a := range addrs {
		for _, ai := range a.AddrInfo {
			sn.addrs[a.IfName] = append(sn.addrs[a.IfName], ai.Local)
		}
	}
	var rules []ipRule
	json.Unmarshal([]byte(part[2]), &rules)
	for _, r := range rules {
		if r.Src == "all" {
			r.Src = ""
		}
		sn.rules = append(sn.rules, r)
	}
	var rr []ipRoute
	json.Unmarshal([]byte(part[3]), &rr)
	for _, r := range rr {
		sn.chanRoutes = append(sn.chanRoutes, r.Dst+" "+r.Dev)
	}
	for _, ln := range strings.Split(strings.TrimSpace(part[4]), "\n") {
		f := strings.Split(ln, "\t")
		if len(f) == 9 { // peer line: dev pub psk endpoint allowed handshake rx tx keepalive
			hs, _ := strconv.ParseInt(f[5], 10, 64)
			d := sn.wg[f[0]]
			if hs > d.handshake {
				d.handshake = hs
			}
			if d.endpoint == "" && f[3] != "(none)" {
				d.endpoint = f[3]
			}
			sn.wg[f[0]] = d
		}
	}
	var nets []ztNet
	json.Unmarshal([]byte(part[5]), &nets)
	for _, n := range nets {
		sn.zt[n.ID] = n
		sn.ztDev[n.ID] = n.Dev
	}
	var info struct {
		Address string `json:"address"`
	}
	json.Unmarshal([]byte(part[6]), &info)
	sn.ztNode = info.Address
	for _, blk := range strings.Split(part[7], "\n\n") {
		var id, st, since string
		for _, ln := range strings.Split(blk, "\n") {
			k, v, _ := strings.Cut(strings.TrimSpace(ln), "=")
			switch k {
			case "Id":
				id = v
			case "ActiveState":
				st = v
			case "ActiveEnterTimestamp":
				since = v
			}
		}
		if id != "" {
			sn.units[id] = [2]string{st, since}
		}
	}
	return sn
}

type nodeEgress struct {
	Channels []egress.Status `json:"channels"`
}

// channelStatus is every channel's state from the host and the node.
func (o *Ops) channelStatus(ctx context.Context, s chanStore) []admproto.ChannelStatus {
	sn := o.hostSnapshot(ctx, s)
	var ne nodeEgress
	if b, err := os.ReadFile(filepath.Join(o.Dir, "egress-status.json")); err == nil {
		json.Unmarshal(b, &ne)
	}
	node := map[string]egress.Status{}
	for _, x := range ne.Channels {
		node[x.Name] = x
	}
	out := []admproto.ChannelStatus{}
	for _, c := range s.Channels {
		st := admproto.ChannelStatus{Name: c.Name, Dev: c.Dev, State: "down"}
		if n, ok := node[c.Name]; ok {
			st.Conns = n.Conns
		}
		switch c.Type {
		case admproto.ChanWG, admproto.ChanOVPN:
			u := sn.units[unitOf(c)]
			st.Since = u[1]
			st.Addrs = sn.addrs[c.Dev]
			st.TX, st.RX = sn.stats[c.Dev][0], sn.stats[c.Dev][1]
			switch {
			case u[0] == "failed":
				st.Detail = "the service failed: journalctl -u " + unitOf(c)
			case !sn.devs[c.Dev]:
				if u[0] == "active" || u[0] == "activating" {
					st.State = "connecting"
				}
			case c.Type == admproto.ChanWG:
				w := sn.wg[c.Dev]
				st.Handshake, st.Endpoint = w.handshake, w.endpoint
				st.State = "connecting"
				if w.handshake > 0 && nowUnix()-w.handshake < 180 {
					st.State = "up"
				}
			default:
				st.State = "connecting"
				if len(st.Addrs) > 0 {
					st.State = "up"
				}
			}
		case admproto.ChanZT:
			n, ok := sn.zt[c.ZT.Network]
			st.NodeID = sn.ztNode
			if ok {
				st.NetName, st.Addrs = n.Name, n.Assigned
				st.TX, st.RX = sn.stats[n.Dev][0], sn.stats[n.Dev][1]
				switch n.Status {
				case "OK":
					st.State = "up"
				case "ACCESS_DENIED":
					st.State, st.Detail = "denied", "authorise this node in the network's controller"
				case "REQUESTING_CONFIGURATION":
					st.State = "connecting"
				default:
					st.Detail = strings.ToLower(n.Status)
				}
			}
		default:
			if n, ok := node[c.Name]; ok {
				st.State, st.Detail, st.TX, st.RX = n.State, n.Error, n.TX, n.RX
				if n.IP != "" {
					st.Addrs = []string{n.IP}
				}
			} else if c.On {
				// Node writes egress-status.json on a tick; a just-saved SSTP
				// is dialing, not down.
				st.State = "connecting"
			}
		}
		// The node's link check: the channel is up but carries nothing.
		if n, ok := node[c.Name]; ok && n.State == "nolink" && st.State == "up" {
			st.State, st.Detail = "nolink", n.Error
		}
		if !c.On {
			st.State = "off"
		}
		if c.Accept && st.State == "up" {
			st.ExternalIP = o.externalIP(ctx, c.Dev)
		}
		out = append(out, st)
	}
	return out
}

func nowUnix() int64 { return time.Now().Unix() }

// externalIP asks the internet which address the channel's traffic comes
// from (cached for ten minutes).
func (o *Ops) externalIP(ctx context.Context, dev string) string {
	o.extMu.Lock()
	if o.ext == nil {
		o.ext = map[string]extIP{}
	}
	e, ok := o.ext[dev]
	o.extMu.Unlock()
	if ok && time.Since(e.at) < 10*time.Minute {
		return e.ip
	}
	out, err := o.host(ctx, "curl", "-s", "-4", "--max-time", "5", "--interface", dev, "https://api.ipify.org")
	ip := strings.TrimSpace(out)
	if err != nil || net.ParseIP(ip) == nil {
		ip = ""
	}
	o.extMu.Lock()
	o.ext[dev] = extIP{ip: ip, at: time.Now()}
	o.extMu.Unlock()
	return ip
}

type extIP struct {
	ip string
	at time.Time
}

// overlapsOverlay refuses a WireGuard address or subnet inside the loft
// network: the node would take it for a member.
func (o *Ops) overlapsOverlay(c admproto.Channel) error {
	if c.Type != admproto.ChanWG {
		return nil
	}
	cur, err := o.NetworkGet()
	if err != nil || cur.Text == "" {
		return nil
	}
	n, err := netconf.ParseCommands(cur.Text)
	if err != nil {
		return nil
	}
	ov, err := netip.ParsePrefix(n.CIDR)
	if err != nil {
		return nil
	}
	var mine []string
	mine = append(mine, strings.Split(c.WG.Address, ",")...)
	for _, p := range c.WG.Peers {
		for _, a := range p.AllowedIPs {
			if !strings.HasSuffix(a, "/0") {
				mine = append(mine, a)
			}
		}
	}
	for _, x := range mine {
		if p, err := netip.ParsePrefix(strings.TrimSpace(x)); err == nil && p.Overlaps(ov) {
			return fmt.Errorf("%s overlaps the loft network %s: pick another subnet", strings.TrimSpace(x), ov)
		}
	}
	return nil
}

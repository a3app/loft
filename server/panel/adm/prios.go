package adm

// This file holds the priority lists of the "External channels" tab
// (channels.go). A priority list is an ordered set of channels: an exit
// rule sends traffic to a list, and the node (loft-node) uses the first
// channel in it that works, falling back to the next. It also validates
// link checks, the probes that decide whether a channel works.

import (
	"context"
	"errors"
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/a3app/loft/internal/egress"
	"github.com/a3app/loft/server/panel/admproto"
)

// Priority lists replace the per-channel "on failure" chains: a rule sends
// traffic to a list, the node takes the first working channel of it.

const maxPrios = 50

// defaultPrioID is the reserved list leftover members use. Direct is a
// Use item: the host's own default route. The name "direct" is a terminal
// in egress.conf, so the list itself is called "default".
const (
	defaultPrioID    = "default"
	defaultPrioTitle = "Политика по умолчанию"
)

// findPrio returns the index of the list with this ID, or -1.
func findPrio(s chanStore, id string) int {
	return slices.IndexFunc(s.Prios, func(p admproto.Prio) bool { return p.ID == id })
}

// freePrioID is the first unused list ID of the form prio1, prio2, ...
func freePrioID(s chanStore) string {
	for n := 1; ; n++ {
		id := "prio" + strconv.Itoa(n)
		if findPrio(s, id) < 0 {
			return id
		}
	}
}

// dropFromPrios takes name out of every list. Use and Limits are cloned
// so the caller's have-store is left as it was.
func dropFromPrios(s *chanStore, name string) {
	for i := range s.Prios {
		if slices.Contains(s.Prios[i].Use, name) {
			s.Prios[i].Use = slices.DeleteFunc(slices.Clone(s.Prios[i].Use), func(u string) bool { return u == name })
		}
		if s.Prios[i].Limits != nil {
			if _, ok := s.Prios[i].Limits[name]; ok {
				lim := make(map[string]admproto.PrioLimit, len(s.Prios[i].Limits))
				for k, v := range s.Prios[i].Limits {
					if k != name {
						lim[k] = v
					}
				}
				s.Prios[i].Limits = lim
			}
		}
	}
}

// migrate turns rules that still name a channel into priority lists built
// from that channel's fail chain, then drops the chains. It runs on every
// read; a store that has no such rules comes back unchanged.
//
// Inside, conv maps one rule target: a channel name becomes a list built by
// following that channel's Fail chain (stopping at a loop, direct or
// block); anything else is returned as it is. A channel converted once
// reuses its list. Last, ensureDefaultPrio makes sure the default list
// exists.
func migrate(s *chanStore) {
	made := map[string]string{} // channel → list ID
	conv := func(v string) string {
		if v == "" || v == egress.Direct || v == egress.Block || findPrio(*s, v) >= 0 {
			return v
		}
		i := find(*s, v)
		if i < 0 {
			return v
		}
		if id, ok := made[v]; ok {
			return id
		}
		p := admproto.Prio{ID: freePrioID(*s), Last: egress.Direct}
		seen := map[string]bool{}
		for x := v; ; {
			j := find(*s, x)
			if j < 0 || seen[x] {
				break
			}
			seen[x] = true
			p.Use = append(p.Use, x)
			next := s.Channels[j].Fail
			if next == "" || next == egress.Direct || next == egress.Block {
				if next == egress.Block {
					p.Last = egress.Block
				}
				break
			}
			x = next
		}
		title := firstNonEmpty(s.Channels[i].Title, v)
		p.Title = title
		for n := 2; slices.ContainsFunc(s.Prios, func(q admproto.Prio) bool { return strings.EqualFold(q.Title, p.Title) }); n++ {
			p.Title = title + " " + strconv.Itoa(n)
		}
		s.Prios = append(s.Prios, p)
		made[v] = p.ID
		return p.ID
	}
	for i := range s.Rules.Rules {
		s.Rules.Rules[i].Via = conv(s.Rules.Rules[i].Via)
	}
	s.Rules.Default = conv(s.Rules.Default)
	for i := range s.Channels {
		s.Channels[i].Fail = ""
	}
	ensureDefaultPrio(s)
}

// ensureDefaultPrio makes the leftover policy a real list: the host
// default route first, then every exit channel. New channels join it
// (Auto). Last is block: native is already in the list. On first create,
// leftover (empty or "direct", or an old fail-chain list) is pointed at it.
func ensureDefaultPrio(s *chanStore) {
	if i := findPrio(*s, defaultPrioID); i >= 0 {
		if s.Prios[i].Title == "" {
			s.Prios[i].Title = defaultPrioTitle
			s.seeded = true
		}
		return
	}
	p := admproto.Prio{
		ID:    defaultPrioID,
		Title: defaultPrioTitle,
		Use:   []string{egress.Direct},
		Last:  egress.Block,
		Auto:  true,
	}
	for _, c := range s.Channels {
		if c.Exit && c.Name != "" && c.Name != egress.Direct {
			p.Use = append(p.Use, c.Name)
		}
	}
	s.Prios = append([]admproto.Prio{p}, s.Prios...)
	s.Rules.Default = defaultPrioID
	s.seeded = true
}

// checkCheck validates and fills a channel's link probe.
func checkCheck(c *admproto.Channel) error {
	k, err := normalizeCheck(c.Check, c.Type == admproto.ChanWG || c.Type == admproto.ChanOVPN || c.Type == admproto.ChanZT)
	if err != nil {
		return err
	}
	c.Check = k
	return nil
}

// normalizeCheck fills defaults. pingOK is false for proxies and SSTP.
func normalizeCheck(k *admproto.ChanCheck, pingOK bool) (*admproto.ChanCheck, error) {
	if k == nil || k.Kind == "" || k.Kind == "off" {
		return nil, nil
	}
	k.Probe = strings.TrimSpace(k.Probe)
	switch k.Kind {
	case "ping":
		if !pingOK {
			return nil, errors.New("link check: ping works for WireGuard, OpenVPN, ZeroTier and the WAN adapter")
		}
		if k.Probe == "" || strings.ContainsAny(k.Probe, " /\"") {
			return nil, errors.New("link check: an IP address or a host name to ping")
		}
	case "tcp":
		k.Probe = withCheckPort(k.Probe, "80")
		if h, _, err := net.SplitHostPort(k.Probe); err != nil || h == "" {
			return nil, errors.New("link check: a host or host:port for the TCP connect")
		}
	case "tls":
		k.Probe = withCheckPort(k.Probe, "443")
		if h, _, err := net.SplitHostPort(k.Probe); err != nil || h == "" {
			return nil, errors.New("link check: a host or host:port for the TLS check")
		}
	case "auto":
		k.Probe = ""
	default:
		return nil, fmt.Errorf("link check: %q (off, ping, tcp, tls, auto)", k.Kind)
	}
	if k.Every == 0 {
		k.Every = 10
	}
	if k.Fails == 0 {
		k.Fails = 3
	}
	if k.Every < 1 || k.Every > 3600 {
		return nil, errors.New("link check: an interval from 1 to 3600 s")
	}
	if k.Fails < 1 || k.Fails > 100 {
		return nil, errors.New("link check: from 1 to 100 failures in a row")
	}
	return k, nil
}

// withCheckPort adds def as the port when s is a bare host.
func withCheckPort(s, def string) string {
	if s == "" {
		return s
	}
	if _, _, err := net.SplitHostPort(s); err == nil {
		return s
	}
	return net.JoinHostPort(s, def)
}

// checkPrio validates a priority list from the page and tidies it in
// place: a unique title of up to 40 characters, a fallback (Last) of
// direct or block, each entry either "direct" or an exit channel listed
// once, and speed limits only for channels still in the list.
func checkPrio(p *admproto.Prio, s chanStore) error {
	// 1. Title and fallback.
	p.Title = strings.Join(strings.Fields(p.Title), " ")
	if p.Title == "" {
		return errors.New("name the list")
	}
	if utf8.RuneCountInString(p.Title) > 40 {
		return errors.New("list name: up to 40 characters")
	}
	for _, q := range s.Prios {
		if q.ID != p.ID && strings.EqualFold(q.Title, p.Title) {
			return fmt.Errorf("a list named %q exists", p.Title)
		}
	}
	if p.Last == "" {
		p.Last = egress.Direct
	}
	if p.Last != egress.Direct && p.Last != egress.Block {
		return errors.New("if none works: direct or block")
	}
	if p.Use == nil {
		p.Use = []string{}
	}
	// 2. On return, drop limits of channels not in the list and zero limits.
	// The deferred cleanup runs after the loop below has filled seen.
	seen := map[string]bool{}
	defer func() {
		for ch, l := range p.Limits {
			if !seen[ch] || (l.In == 0 && l.Out == 0) {
				delete(p.Limits, ch) // a channel taken out of the list drops its limit
			}
		}
		if len(p.Limits) == 0 {
			p.Limits = nil
		}
	}()
	// 3. Limits are in Mbit/s; the server's own direct route takes none.
	for ch, l := range p.Limits {
		if ch == egress.Direct {
			delete(p.Limits, ch)
			continue
		}
		if l.In < 0 || l.Out < 0 || l.In > 100000 || l.Out > 100000 {
			return fmt.Errorf("speed limit of %s: 0 to 100000 Mbit/s", ch)
		}
	}
	// 4. Entries: direct, or an existing exit channel, each once.
	for _, u := range p.Use {
		if u == egress.Direct {
			if seen[u] {
				return fmt.Errorf("%s is in the list twice", u)
			}
			seen[u] = true
			continue
		}
		i := find(s, u)
		if i < 0 {
			return fmt.Errorf("%q is no channel", u)
		}
		if !s.Channels[i].Exit {
			return fmt.Errorf("%s is not used for internet exit", firstNonEmpty(s.Channels[i].Title, u))
		}
		if seen[u] {
			return fmt.Errorf("%s is in the list twice", u)
		}
		seen[u] = true
	}
	return nil
}

// autoJoin puts a new exit channel at the end of every list that takes new ones.
func autoJoin(s *chanStore, c admproto.Channel) {
	if !c.Exit {
		return
	}
	for i := range s.Prios {
		if s.Prios[i].Auto && !slices.Contains(s.Prios[i].Use, c.Name) {
			s.Prios[i].Use = append(slices.Clone(s.Prios[i].Use), c.Name)
		}
	}
}

// ChannelPrio adds a list (empty ID) or changes one.
func (o *Ops) ChannelPrio(ctx context.Context, p admproto.Prio) (admproto.ChannelsView, error) {
	o.chanMu.Lock()
	s, err := o.readChans()
	if err != nil {
		o.chanMu.Unlock()
		return admproto.ChannelsView{}, err
	}
	i := -1
	if p.ID == "" {
		if len(s.Prios) >= maxPrios {
			o.chanMu.Unlock()
			return admproto.ChannelsView{}, fmt.Errorf("at most %d lists", maxPrios)
		}
		p.ID = freePrioID(s)
	} else if i = findPrio(s, p.ID); i < 0 {
		o.chanMu.Unlock()
		return admproto.ChannelsView{}, fmt.Errorf("no list %q", p.ID)
	}
	if p.ID == defaultPrioID {
		p.Title = defaultPrioTitle
	}
	if err := checkPrio(&p, s); err != nil {
		o.chanMu.Unlock()
		return admproto.ChannelsView{}, err
	}
	next := s
	next.Prios = slices.Clone(s.Prios)
	if i >= 0 {
		next.Prios[i] = p
	} else {
		next.Prios = append(next.Prios, p)
	}
	err = o.applyChans(ctx, s, next)
	o.chanMu.Unlock()
	if err != nil {
		return admproto.ChannelsView{}, err
	}
	return o.Channels(ctx)
}

// ChannelPrioDelete removes a list no rule picks.
func (o *Ops) ChannelPrioDelete(ctx context.Context, id string) (admproto.ChannelsView, error) {
	o.chanMu.Lock()
	s, err := o.readChans()
	if err != nil {
		o.chanMu.Unlock()
		return admproto.ChannelsView{}, err
	}
	i := findPrio(s, id)
	if i < 0 {
		o.chanMu.Unlock()
		return admproto.ChannelsView{}, fmt.Errorf("no list %q", id)
	}
	if id == defaultPrioID {
		o.chanMu.Unlock()
		return admproto.ChannelsView{}, errors.New("the default policy cannot be deleted")
	}
	if usedByRules(s, id) {
		o.chanMu.Unlock()
		return admproto.ChannelsView{}, errors.New("exit rules send traffic to this list: change them first")
	}
	next := s
	next.Prios = slices.Delete(slices.Clone(s.Prios), i, i+1)
	err = o.applyChans(ctx, s, next)
	o.chanMu.Unlock()
	if err != nil {
		return admproto.ChannelsView{}, err
	}
	return o.Channels(ctx)
}

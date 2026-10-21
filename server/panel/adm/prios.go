package adm

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

func findPrio(s chanStore, id string) int {
	return slices.IndexFunc(s.Prios, func(p admproto.Prio) bool { return p.ID == id })
}

func freePrioID(s chanStore) string {
	for n := 1; ; n++ {
		id := "prio" + strconv.Itoa(n)
		if findPrio(s, id) < 0 {
			return id
		}
	}
}

// prioUsing is the first list that holds channel name, or nil.
func prioUsing(s chanStore, name string) *admproto.Prio {
	for i := range s.Prios {
		if slices.Contains(s.Prios[i].Use, name) {
			return &s.Prios[i]
		}
	}
	return nil
}

// migrate turns rules that still name a channel into priority lists built
// from that channel's fail chain, then drops the chains. It runs on every
// read; a store that has no such rules comes back unchanged.
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
}

// checkCheck validates and fills a channel's link probe.
func checkCheck(c *admproto.Channel) error {
	k := c.Check
	if k == nil || k.Kind == "" {
		c.Check = nil
		return nil
	}
	k.Probe = strings.TrimSpace(k.Probe)
	switch k.Kind {
	case "ping":
		if c.Type != admproto.ChanWG && c.Type != admproto.ChanOVPN && c.Type != admproto.ChanZT {
			return errors.New("link check: ping works for WireGuard, OpenVPN and ZeroTier; use a TCP connect")
		}
		if k.Probe == "" || strings.ContainsAny(k.Probe, " :/\"") {
			return errors.New("link check: an IP address or a host name to ping")
		}
	case "tcp":
		if h, _, err := net.SplitHostPort(k.Probe); err != nil || h == "" {
			return errors.New("link check: host:port for the TCP connect")
		}
	default:
		return fmt.Errorf("link check: %q (ping, tcp)", k.Kind)
	}
	if k.Every == 0 {
		k.Every = 10
	}
	if k.Fails == 0 {
		k.Fails = 3
	}
	if k.Every < 1 || k.Every > 3600 {
		return errors.New("link check: an interval from 1 to 3600 s")
	}
	if k.Fails < 1 || k.Fails > 100 {
		return errors.New("link check: from 1 to 100 failures in a row")
	}
	return nil
}

func checkPrio(p *admproto.Prio, s chanStore) error {
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
	seen := map[string]bool{}
	for _, u := range p.Use {
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

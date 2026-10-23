package main

// On-the-fly front rotation: SOCKS stays bound, Supervise dials the next type
// after the live dialer closes, and the next HTTPS sample uses that front.

import (
	"context"
	"fmt"
	"os"
	"time"

	"github.com/a3app/loft/clients/go/inbound"
	"github.com/a3app/loft/internal/config"
	"github.com/a3app/loft/internal/transport"
)

type hop struct {
	o   config.Outbound
	err error
}

// runSwitch brings the front list up under one SOCKS listener and rotates
// after each live HTTPS sample. A repeated type means the node admitted
// nothing further; the run stops there.
func runSwitch(ctx context.Context, token, raw, path, cdnName string, muxOn bool, kinds []string, httpN int, timeout time.Duration) ([]row, error) {
	if len(kinds) == 0 {
		return nil, fmt.Errorf("no fronts")
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout*time.Duration(len(kinds)+1))
	defer cancel()
	cfg := benchConfig(token, raw, path, cdnName, muxOn, kinds)
	h := &transport.Holder{}
	defer h.Close()
	hops := make(chan hop, len(kinds)+1)
	go func() {
		err := transport.SuperviseOn(runCtx, cfg, nil, func(d transport.Dialer, o config.Outbound) {
			h.Set(d)
			hops <- hop{o: o}
		})
		hops <- hop{err: err}
	}()

	ln, err := inbound.ListenSOCKS5("127.0.0.1:0", h)
	if err != nil {
		return nil, fmt.Errorf("socks: %w", err)
	}
	defer ln.Close()
	socksAddr := ln.Addr().String()

	var rows []row
	prev := ""
	for i := 0; i < len(kinds); i++ {
		var got hop
		select {
		case got = <-hops:
		case <-runCtx.Done():
			return rows, runCtx.Err()
		}
		if got.err != nil {
			if len(rows) == 0 {
				return rows, got.err
			}
			return rows, nil
		}
		if got.o.Type == prev {
			fmt.Fprintf(os.Stderr, "switch stayed on %s\n", prev)
			rows = append(rows, row{
				Kind:  prev,
				Path:  "switch",
				Extra: "stayed " + transport.SessionLine(raw),
			})
			return rows, nil
		}
		ip := transport.OverlayIP()
		line := transport.SessionLine(raw)
		fmt.Fprintf(os.Stderr, "switch live %s overlay=%s socks=%s %s\n", got.o.Type, ip, socksAddr, line)
		if c, err := h.Open("1.1.1.1:80"); err != nil {
			fmt.Fprintf(os.Stderr, "switch open %s: %v\n", got.o.Type, err)
		} else {
			_ = c.Close()
		}
		cf, err := httpsThrough(runCtx, socksAddr, httpN, timeout)
		cr := row{Kind: got.o.Type, Path: "socks-https", Extra: line}
		if prev != "" {
			cr.Path = "switch-https"
			cr.Extra = prev + " -> " + got.o.Type + " " + line
		}
		if err != nil {
			cr.Err = err.Error()
			rows = append(rows, cr)
			return rows, nil
		}
		cr.MS = float64(cf.Milliseconds())
		cr.Down = mbit(httpN, cf)
		rows = append(rows, cr)
		prev = got.o.Type
		used := map[string]bool{got.o.Type: true}
		for _, row := range rows {
			used[row.Kind] = true
		}
		next := nextKind(kinds, got.o.Type, used)
		if next == "" {
			break
		}
		d := h.Live()
		if d == nil {
			return rows, fmt.Errorf("switch: no dialer")
		}
		// Supervise is blocked on Done. Reorder so the post-rotate
		// start index is next, then close. A failed next is tried once
		// and StartSelected continues down the list.
		n := int((transport.RotateCount() + 1) % uint64(len(cfg.Outbounds)))
		seq := orderForRotate(kindList(cfg), next, n)
		cfg.Fronts = seq
		var nextOut []config.Outbound
		for _, t := range seq {
			nextOut = append(nextOut, outboundFor(raw, t, path, cdnName, muxOn))
		}
		cfg.Outbounds = nextOut
		fmt.Fprintf(os.Stderr, "switch drop %s -> %s\n", got.o.Type, next)
		_ = d.Close()
	}
	return rows, nil
}

// nextKind is the first name after cur that this run has not measured.
func nextKind(kinds []string, cur string, used map[string]bool) string {
	start := 0
	for i, k := range kinds {
		if k == cur {
			start = i + 1
			break
		}
	}
	for i := start; i < len(kinds); i++ {
		if !used[kinds[i]] {
			return kinds[i]
		}
	}
	return ""
}

// kindList is the outbound types in cfg order.
func kindList(cfg *config.ClientConfig) []string {
	var out []string
	for _, o := range cfg.Outbounds {
		out = append(out, o.Type)
	}
	return out
}

// orderForRotate places next at index n and keeps the other names in order.
// filterOutbounds rotates by n, so the dial starts at next.
func orderForRotate(kinds []string, next string, n int) []string {
	if len(kinds) == 0 {
		return nil
	}
	rest := make([]string, 0, len(kinds)-1)
	for _, k := range kinds {
		if k != next {
			rest = append(rest, k)
		}
	}
	if n < 0 {
		n = 0
	}
	n %= len(kinds)
	out := make([]string, 0, len(kinds))
	ri := 0
	for i := 0; i < len(kinds); i++ {
		if i == n {
			out = append(out, next)
			continue
		}
		if ri < len(rest) {
			out = append(out, rest[ri])
			ri++
		}
	}
	return out
}

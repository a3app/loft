package main

// A/B comparison of the experimental options (branch experimentos):
// disorder (transport/disorder.go) and l3 shaping (transport/l3shape.go).
//
// -ab N runs N rounds per front. Each round dials the front twice, once
// with the options off and once on, in alternating order (off first on odd
// rounds, on first on even), so a network that drifts during the test hurts
// both sides equally. Every dial measures: time to a live session
// (TCP + TLS + loft handshake together), the time to open a new connection
// through the tunnel and get the first byte back (five times, median: this
// is where l3 shaping adds its cork wait), and one download and one upload
// of -bytes to the bench origin. At the end a table shows, per front and
// setting, failures, median and 95th percentile handshake and first-byte
// times, median speeds, and for l3 shaping the padding overhead.
//
// -soak D keeps one session busy with back-to-back downloads for D with the
// options off and D with them on, in four alternating slices each. It
// counts transfer errors, session deaths and reconnects, and stalls (no
// bytes for over a second), which is the stability part of the comparison.
//
// -csv FILE also writes every A/B sample as one CSV line, to compare runs
// from different networks later.
//
// For l3 the bench origin must not be a loopback address: the node does not
// forward l3 packets to its own loopback. Run "loft-bench -serve
// 0.0.0.0:9474" on the node and pass -origin <node address>:9474.

import (
	"context"
	"encoding/csv"
	"fmt"
	"io"
	"net"
	"os"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/a3app/loft/clients/go/inbound"
	"github.com/a3app/loft/internal/transport"
)

// benchParams are the settings every A/B and soak dial shares.
type benchParams struct {
	token, raw, path, origin, cdn string
	mux                           bool
	nbyte                         int
	timeout                       time.Duration
}

// variant is one setting of the experimental options.
type variant struct {
	name              string
	disorder, l3Shape bool
}

// apply switches the options for the next dials.
func (v variant) apply() {
	transport.SetDisorder(v.disorder)
	transport.SetL3Shape(v.l3Shape)
}

// abVariants is "off" and "on", where "on" turns on the options the user
// named with -disorder and -l3-shape.
func abVariants(disorder, l3Shape bool) []variant {
	var on []string
	if disorder {
		on = append(on, "disorder")
	}
	if l3Shape {
		on = append(on, "l3_shape")
	}
	return []variant{{name: "off"}, {name: strings.Join(on, "+"), disorder: disorder, l3Shape: l3Shape}}
}

// sample is what one dial of one variant measured.
type sample struct {
	kind, variant string
	round         int
	hsMS          float64
	firstMS       float64 // median time to first byte on a new connection
	down, up      float64 // Mbit/s
	shaped        bool    // l3 only: the node agreed to shaping
	stats         transport.L3ShapeStats
	err           string
}

// abFirstN is how many new connections each sample opens to time the first byte.
const abFirstN = 5

// abSample dials kind with variant v and measures one sample.
func abSample(ctx context.Context, p benchParams, kind string, v variant, round int) (s sample) {
	s = sample{kind: kind, variant: v.name, round: round}
	v.apply()
	before := transport.L3ShapeCounters()
	defer func() { s.stats = transport.L3ShapeCounters().Sub(before) }()

	hs, cancel := context.WithTimeout(ctx, 20*time.Second)
	t0 := time.Now()
	d, _, err := dialFront(hs, p.token, p.raw, kind, p.path, p.cdn, p.mux)
	cancel()
	if err != nil {
		s.err = "dial: " + err.Error()
		return s
	}
	defer d.Close()
	s.hsMS = ms(time.Since(t0))
	if l, ok := d.(*transport.L3); ok {
		s.shaped = l.Shaped()
	}
	// An l3 front carries IP packets; a small TCP/IP stack on top of it
	// gives the same Open as the mux fronts.
	w, err := inbound.WrapL3(d)
	if err != nil {
		s.err = "l3 stack: " + err.Error()
		return s
	}
	if w != d {
		defer w.Close()
	}

	var firsts []float64
	for i := 0; i < abFirstN; i++ {
		t := time.Now()
		_, err := withConn(ctx, p.timeout, func(context.Context) (net.Conn, error) {
			return w.Open(p.origin)
		}, originEchoRTT)
		if err != nil {
			s.err = "first byte: " + err.Error()
			return s
		}
		firsts = append(firsts, ms(time.Since(t)))
	}
	s.firstMS = pct(firsts, 50)

	dt, err := withConn(ctx, p.timeout, func(context.Context) (net.Conn, error) {
		return w.Open(p.origin)
	}, func(c net.Conn) (time.Duration, error) { return originDownload(c, p.nbyte) })
	if err != nil {
		s.err = "down: " + err.Error()
		return s
	}
	s.down = mbit(p.nbyte, dt)
	dt, err = withConn(ctx, p.timeout, func(context.Context) (net.Conn, error) {
		return w.Open(p.origin)
	}, func(c net.Conn) (time.Duration, error) { return originUpload(c, p.nbyte) })
	if err != nil {
		s.err = "up: " + err.Error()
		return s
	}
	s.up = mbit(p.nbyte, dt)
	return s
}

// runAB runs rounds A/B rounds for every kind, prints the summary and
// returns all samples. csvOut, when not nil, gets one line per sample.
func runAB(ctx context.Context, p benchParams, kinds []string, rounds int, vs []variant, csvOut io.Writer) []sample {
	var cw *csv.Writer
	if csvOut != nil {
		cw = csv.NewWriter(csvOut)
		_ = cw.Write([]string{"kind", "variant", "round", "handshake_ms", "first_byte_ms", "down_mbit", "up_mbit", "shaped", "payload_b", "pad_b", "chaff_n", "chaff_b", "padded_n", "err"})
	}
	var all []sample
	for _, kind := range kinds {
		if kind == "l3" && originLoopback(p.origin) {
			fmt.Fprintf(os.Stderr, "ab: l3 cannot reach a loopback origin (%s); run -serve on a public address and pass -origin\n", p.origin)
		}
		for r := 1; r <= rounds && ctx.Err() == nil; r++ {
			order := vs
			if r%2 == 0 {
				order = []variant{vs[1], vs[0]}
			}
			for _, v := range order {
				s := abSample(ctx, p, kind, v, r)
				all = append(all, s)
				fmt.Fprintf(os.Stderr, "ab %s %s round %d: hs=%.0fms first=%.0fms down=%.2f up=%.2f %s\n", kind, v.name, r, s.hsMS, s.firstMS, s.down, s.up, s.err)
				if cw != nil {
					_ = cw.Write([]string{s.kind, s.variant, strconv.Itoa(s.round), f1(s.hsMS), f1(s.firstMS), f2(s.down), f2(s.up),
						strconv.FormatBool(s.shaped), u(s.stats.Payload), u(s.stats.Pad), u(s.stats.ChaffN), u(s.stats.Chaff), u(s.stats.PaddedN), s.err})
					cw.Flush()
				}
			}
		}
	}
	printAB(os.Stdout, all, vs)
	return all
}

// printAB writes one summary line per front and variant.
func printAB(w io.Writer, all []sample, vs []variant) {
	fmt.Fprintf(w, "%-5s %-18s %5s %9s %9s %9s %9s %9s %9s %8s\n",
		"type", "variant", "fail", "hs_p50", "hs_p95", "first_p50", "first_p95", "down_Mbit", "up_Mbit", "pad_%")
	kinds := map[string]bool{}
	var order []string
	for _, s := range all {
		if !kinds[s.kind] {
			kinds[s.kind] = true
			order = append(order, s.kind)
		}
	}
	for _, k := range order {
		for _, v := range vs {
			var hs, first, down, up []float64
			var fails, n int
			var st transport.L3ShapeStats
			shaped := 0
			for _, s := range all {
				if s.kind != k || s.variant != v.name {
					continue
				}
				n++
				st.Payload += s.stats.Payload
				st.Pad += s.stats.Pad
				st.Chaff += s.stats.Chaff
				if s.shaped {
					shaped++
				}
				if s.err != "" {
					fails++
					continue
				}
				hs = append(hs, s.hsMS)
				first = append(first, s.firstMS)
				down = append(down, s.down)
				up = append(up, s.up)
			}
			pad := "-"
			if st.Payload > 0 && (st.Pad+st.Chaff) > 0 {
				pad = fmt.Sprintf("%.1f", float64(st.Pad+st.Chaff)*100/float64(st.Payload))
			}
			name := v.name
			if v.l3Shape && k == "l3" && shaped < n {
				name += fmt.Sprintf(" (shaped %d/%d)", shaped, n)
			}
			fmt.Fprintf(w, "%-5s %-18s %2d/%-2d %9.0f %9.0f %9.0f %9.0f %9.2f %9.2f %8s\n",
				k, name, fails, n, pct(hs, 50), pct(hs, 95), pct(first, 50), pct(first, 95), pct(down, 50), pct(up, 50), pad)
		}
	}
}

// soakResult is what one variant's soak slices added up to.
type soakResult struct {
	dur        time.Duration
	bytes      int64
	transfers  int
	errs       int
	deaths     int
	reconnects []float64 // ms from a dead session to a live one
	stalls     int
	maxGap     time.Duration
}

// soakStall is the gap without bytes that counts as a stall.
const soakStall = time.Second

// runSoak keeps one session per variant busy for dur in total, in four
// alternating slices, and prints errors, deaths, reconnects and stalls.
func runSoak(ctx context.Context, p benchParams, kind string, vs []variant, dur time.Duration) map[string]*soakResult {
	const slices = 4
	res := map[string]*soakResult{}
	for _, v := range vs {
		res[v.name] = &soakResult{}
	}
	slice := dur / slices
	for i := 0; i < slices && ctx.Err() == nil; i++ {
		for _, v := range vs {
			soakSlice(ctx, p, kind, v, slice, res[v.name])
		}
	}
	fmt.Printf("%-5s %-18s %8s %9s %6s %6s %7s %10s %7s %9s\n", "type", "variant", "minutes", "avg_Mbit", "xfers", "errs", "deaths", "reconn_ms", "stalls", "max_gap_s")
	for _, v := range vs {
		r := res[v.name]
		avg := 0.0
		if r.dur > 0 {
			avg = float64(r.bytes) * 8 / r.dur.Seconds() / 1e6
		}
		fmt.Printf("%-5s %-18s %8.1f %9.2f %6d %6d %7d %10.0f %7d %9.1f\n", kind, v.name, r.dur.Minutes(), avg, r.transfers, r.errs, r.deaths, pct(r.reconnects, 50), r.stalls, r.maxGap.Seconds())
	}
	return res
}

// soakSlice runs back-to-back downloads with variant v for d. A dead
// session is dialed again; the time that takes is a reconnect.
func soakSlice(ctx context.Context, p benchParams, kind string, v variant, d time.Duration, r *soakResult) {
	v.apply()
	end := time.Now().Add(d)
	t0 := time.Now()
	defer func() { r.dur += time.Since(t0) }()
	var cur transport.Dialer
	var open transport.Dialer
	closeCur := func() {
		if open != nil && open != cur {
			_ = open.Close()
		}
		if cur != nil {
			_ = cur.Close()
		}
		cur, open = nil, nil
	}
	defer closeCur()
	var deadAt time.Time
	for time.Now().Before(end) && ctx.Err() == nil {
		if cur == nil {
			hs, cancel := context.WithTimeout(ctx, 20*time.Second)
			dd, _, err := dialFront(hs, p.token, p.raw, kind, p.path, p.cdn, p.mux)
			cancel()
			if err != nil {
				r.errs++
				time.Sleep(time.Second)
				continue
			}
			w, err := inbound.WrapL3(dd)
			if err != nil {
				_ = dd.Close()
				r.errs++
				continue
			}
			cur, open = dd, w
			if !deadAt.IsZero() {
				r.reconnects = append(r.reconnects, ms(time.Since(deadAt)))
				deadAt = time.Time{}
			}
		}
		n, gaps, maxGap, err := soakDownload(ctx, open, p.origin, p.nbyte, p.timeout)
		r.bytes += n
		r.transfers++
		r.stalls += gaps
		if maxGap > r.maxGap {
			r.maxGap = maxGap
		}
		if err != nil {
			r.errs++
		}
		select {
		case <-cur.Done():
			r.deaths++
			deadAt = time.Now()
			closeCur()
		default:
		}
	}
}

// soakDownload downloads n bytes from the bench origin through d and
// counts gaps of soakStall or more between reads.
func soakDownload(ctx context.Context, d transport.Dialer, origin string, n int, timeout time.Duration) (int64, int, time.Duration, error) {
	cctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	c, err := d.Open(origin)
	if err != nil {
		return 0, 0, 0, err
	}
	defer c.Close()
	go func() { <-cctx.Done(); _ = c.SetDeadline(time.Now()) }()
	if err := writeCmdSize(c, originDown, n); err != nil {
		return 0, 0, 0, err
	}
	buf := make([]byte, 32<<10)
	var got int64
	stalls := 0
	var maxGap time.Duration
	last := time.Now()
	for got < int64(n) {
		k, err := c.Read(buf)
		now := time.Now()
		if k > 0 {
			if gap := now.Sub(last); gap >= soakStall {
				stalls++
				if gap > maxGap {
					maxGap = gap
				}
			}
			last = now
			got += int64(k)
		}
		if err != nil {
			return got, stalls, maxGap, err
		}
	}
	return got, stalls, maxGap, nil
}

// originLoopback reports whether the bench origin is on a loopback address.
func originLoopback(hostport string) bool {
	h, _, err := net.SplitHostPort(hostport)
	if err != nil {
		return false
	}
	ip := net.ParseIP(h)
	return h == "localhost" || (ip != nil && ip.IsLoopback())
}

// pct is the p-th percentile of xs (nearest rank), 0 for none.
func pct(xs []float64, p float64) float64 {
	if len(xs) == 0 {
		return 0
	}
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	i := int(p/100*float64(len(s))+0.5) - 1
	if i < 0 {
		i = 0
	}
	if i >= len(s) {
		i = len(s) - 1
	}
	return s[i]
}

// ms is d in milliseconds as a float.
func ms(d time.Duration) float64 { return float64(d.Microseconds()) / 1000 }

// f1, f2 and u format CSV numbers.
func f1(x float64) string { return strconv.FormatFloat(x, 'f', 1, 64) }
func f2(x float64) string { return strconv.FormatFloat(x, 'f', 2, 64) }
func u(x uint64) string   { return strconv.FormatUint(x, 10) }

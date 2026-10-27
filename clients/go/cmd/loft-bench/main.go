// Command loft-bench repeats the macOS app connect path (front, SOCKS5,
// TCP probe) and measures mux/SOCKS throughput for GET and WS.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/a3app/loft/clients/go/inbound"
	"github.com/a3app/loft/internal/protect"
	"github.com/a3app/loft/internal/transport"
	"github.com/a3app/loft/internal/version"
	"github.com/a3app/loft/internal/wire"
)

// main is either -serve (node-side origin) or the client suite against a front.
func main() {
	version.ExitIfAsked("loft-bench", os.Args[1:])
	serve := flag.String("serve", "", "listen host:port as bench origin (run on the node)")
	token := flag.String("token", os.Getenv("LOFT_TOKEN"), "member key")
	rawURL := flag.String("url", envOr("LOFT_URL", "https://loft.example.com:444"), "front URL")
	path := flag.String("path", "/static/js", "GET path")
	origin := flag.String("origin", "127.0.0.1:9474", "bench origin the node dials (loopback on the node)")
	types := flag.String("type", "ws,get", "comma list: get,ws,h2,quic,h3,l3 or all")
	cdnName := flag.String("cdn", "", "edge profile: yandex (GET, 16 KiB Cookie, verify TLS)")
	decoy := flag.String("decoy", "", "direct-path decoy SNI (dest hostname)")
	pbk := flag.String("pbk", "", "node X25519 public, hex or base64url; first hello is sid v3")
	muxOn := flag.Bool("mux", true, "inner mux; false = HTTP/2 CONNECT splice")
	live := flag.Bool("live", false, "skip the node-local origin; HTTPS through the front")
	switchOn := flag.Bool("switch", false, "keep SOCKS up and rotate -type while HTTPS flows")
	bytesN := flag.Int("bytes", 2<<20, "payload size for down/up")
	httpN := flag.Int("http", 1<<20, "HTTPS body for internet/direct samples")
	ccName := flag.String("cc", "", "congestion control ask: off | bbr | fixed; empty = off")
	ccUp := flag.Uint("cc-up", 0, "fixed: upload rate, Mbit/s")
	ccDown := flag.Uint("cc-down", 0, "fixed: download rate, Mbit/s")
	timeout := flag.Duration("timeout", 45*time.Second, "per-case deadline")
	ifName := flag.String("if", "", "outbound interface or local IP")
	h2MaxAge := flag.Duration("h2-max-age", 0, "GET HTTP/2 recycle; 0 keep until error")
	disorder := flag.Bool("disorder", false, "experimental: ClientHello start with TTL 1 (with -ab/-soak: the option to compare)")
	l3Shape := flag.Bool("l3-shape", false, "experimental: l3 shaping (with -ab/-soak: the option to compare)")
	abN := flag.Int("ab", 0, "A/B: rounds per front, options off vs on (needs -disorder and/or -l3-shape)")
	soak := flag.Duration("soak", 0, "stability: this long off and this long on, back-to-back downloads")
	csvPath := flag.String("csv", "", "A/B: also write every sample to this CSV file")
	flag.Parse()
	if *h2MaxAge > 0 {
		outboundH2MaxAgeMS = int(h2MaxAge.Milliseconds())
	}
	benchDecoy = strings.TrimSpace(*decoy)
	benchPBK = strings.TrimSpace(*pbk)
	cc, err := wire.ParseCCName(*ccName)
	if err != nil {
		fmt.Fprintf(os.Stderr, "loft-bench: %v\n", err)
		os.Exit(2)
	}
	transport.SetClientCC(cc, uint32(*ccUp)*1000, uint32(*ccDown)*1000)
	transport.SetDisorder(*disorder)
	transport.SetL3Shape(*l3Shape)

	if *serve != "" {
		if err := serveOrigin(*serve); err != nil {
			fmt.Fprintf(os.Stderr, "loft-bench serve: %v\n", err)
			os.Exit(1)
		}
		return
	}
	if err := protect.BindUplink(*ifName); err != nil {
		fmt.Fprintf(os.Stderr, "loft-bench: -if: %v\n", err)
		os.Exit(2)
	}
	if s := protect.Bound(); s != "" {
		fmt.Fprintf(os.Stderr, "if %s\n", s)
	}
	if *token == "" || *rawURL == "" {
		fmt.Fprintln(os.Stderr, "loft-bench: -token and -url required (or LOFT_TOKEN / LOFT_URL)")
		os.Exit(2)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	fmt.Fprintf(os.Stderr, "loft-bench url=%s origin=%s cdn=%s mux=%v live=%v switch=%v bytes=%d http=%d stand_inner=%s stand_fan=%s stand_dispatch=%s\n", *rawURL, *origin, *cdnName, *muxOn, *live, *switchOn, *bytesN, *httpN, os.Getenv("LOFT_STAND_INNER"), os.Getenv("LOFT_STAND_FAN"), os.Getenv("LOFT_STAND_DISPATCH"))
	kinds := splitTypes(*types)
	if strings.EqualFold(strings.TrimSpace(*cdnName), "yandex") {
		var onlyGET []string
		for _, k := range kinds {
			if k == "get" {
				onlyGET = append(onlyGET, k)
			} else {
				fmt.Fprintf(os.Stderr, "cdn yandex carries GET; skip %s\n", k)
			}
		}
		if len(onlyGET) == 0 {
			onlyGET = []string{"get"}
		}
		kinds = onlyGET
	}
	if *abN > 0 || *soak > 0 {
		if !*disorder && !*l3Shape {
			fmt.Fprintln(os.Stderr, "loft-bench: -ab and -soak compare an option: add -disorder and/or -l3-shape")
			os.Exit(2)
		}
		p := benchParams{token: *token, raw: *rawURL, path: *path, origin: *origin, cdn: *cdnName, mux: *muxOn, nbyte: *bytesN, timeout: *timeout}
		vs := abVariants(*disorder, *l3Shape)
		if *abN > 0 {
			var out io.Writer
			if *csvPath != "" {
				f, err := os.Create(*csvPath)
				if err != nil {
					fmt.Fprintf(os.Stderr, "loft-bench: -csv: %v\n", err)
					os.Exit(2)
				}
				defer f.Close()
				out = f
			}
			runAB(ctx, p, kinds, *abN, vs, out)
		}
		if *soak > 0 {
			for _, k := range kinds {
				runSoak(ctx, p, k, vs, *soak)
			}
		}
		return
	}

	var rows []row
	if os.Getenv("LOFT_STAND") != "1" {
		rows = append(rows, measureDirect(ctx, *httpN, *timeout)...)
	}

	if *switchOn {
		rs, err := runSwitch(ctx, *token, *rawURL, *path, *cdnName, *muxOn, kinds, *httpN, *timeout)
		if err != nil {
			rows = append(rows, row{Kind: "switch", Path: "rotate", Err: err.Error()})
			fmt.Fprintf(os.Stderr, "switch: %v\n", err)
		} else {
			rows = append(rows, rs...)
		}
		printTable(rows)
		if anyFail(rows) {
			os.Exit(1)
		}
		return
	}

	for _, kind := range kinds {
		rs, err := runFront(ctx, *token, *rawURL, kind, *path, *origin, *cdnName, *muxOn, *live, *bytesN, *httpN, *timeout)
		if err != nil {
			rows = append(rows, row{Kind: kind, Path: "handshake", Err: err.Error()})
			fmt.Fprintf(os.Stderr, "front %s: %v\n", kind, err)
			continue
		}
		rows = append(rows, rs...)
	}
	printTable(rows)
	if anyFail(rows) {
		os.Exit(1)
	}
}

// envOr is os.Getenv(key) or fallback when empty.
func envOr(key, fallback string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return fallback
}

// runFront is one GUI-style session: Start front, SOCKS, TCP probe, mux and SOCKS bulk.
// live skips the node-local origin and samples HTTPS through the front.
func runFront(ctx context.Context, token, raw, kind, path, origin, cdnName string, muxOn, live bool, nbyte, httpN int, timeout time.Duration) ([]row, error) {
	hs, cancel := context.WithTimeout(ctx, 20*time.Second)
	t0 := time.Now()
	d, front, err := dialFront(hs, token, raw, kind, path, cdnName, muxOn)
	cancel()
	if err != nil {
		return nil, err
	}
	defer d.Close()
	hsMS := float64(time.Since(t0).Milliseconds())
	// An l3 front carries IP packets, not mux streams: put a small TCP/IP
	// stack on it so Open (mux rows) and the SOCKS listener work as on the
	// other fronts.
	if w, err := inbound.WrapL3(d); err != nil {
		return nil, fmt.Errorf("l3 stack: %w", err)
	} else if w != d {
		defer w.Close()
		d = w
	}
	ip := transport.OverlayIP()
	bulk := ""
	wantLanes := 0
	if os.Getenv("LOFT_STAND") == "1" && os.Getenv("LOFT_STAND_FAN") != "off" {
		if n, err := strconv.Atoi(os.Getenv("LOFT_STAND_COPIES")); err == nil && n > 1 {
			wantLanes = n
		}
		dead := time.Now().Add(3 * time.Second)
		if wantLanes > 1 {
			dead = time.Now().Add(20 * time.Second)
		}
		for time.Now().Before(dead) {
			bulk = transport.LiveBulk()
			if wantLanes > 1 && transport.LiveLaneCount() >= wantLanes {
				break
			}
			if wantLanes <= 1 && strings.Contains(bulk, "+") {
				break
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	bulk = transport.LiveBulk()
	fmt.Fprintf(os.Stderr, "session type=%s front=%s bulk=%s lanes=%d overlay=%s mux=%v handshake=%.0fms %s\n", kind, front, bulk, transport.LiveLaneCount(), ip, muxOn, hsMS, transport.SessionLine(raw))

	h := &transport.Holder{}
	h.Set(d)
	ln, err := inbound.ListenSOCKS5("127.0.0.1:0", h)
	if err != nil {
		return nil, fmt.Errorf("socks: %w", err)
	}
	defer ln.Close()
	socksAddr := ln.Addr().String()

	var rows []row
	note := "overlay=" + ip + " front=" + front + " bulk=" + bulk
	if !muxOn {
		note += " connect-splice"
	}
	rows = append(rows, row{Kind: kind, Path: "handshake", MS: hsMS, Extra: note + " " + transport.SessionLine(raw)})

	probeCtx, cancelP := context.WithTimeout(ctx, 8*time.Second)
	tcpOK, tcpMS, tcpErr := socksCarriesTCP(probeCtx, socksAddr)
	cancelP()
	pr := row{Kind: kind, Path: "socks-tcp", MS: tcpMS}
	if tcpErr != nil {
		pr.Err = tcpErr.Error()
	} else if !tcpOK {
		pr.Err = "no http"
	}
	rows = append(rows, pr)

	if os.Getenv("LOFT_STAND") == "1" {
		rows = append(rows, measureMux(ctx, d, origin, nbyte, timeout, kind)...)
	} else if live {
		cf, err := httpsThrough(ctx, socksAddr, httpN, timeout)
		cr := row{Kind: kind, Path: "socks-https", Extra: "speed.cloudflare.com"}
		if err != nil {
			cr.Err = err.Error()
		} else {
			cr.MS = float64(cf.Milliseconds())
			cr.Down = mbit(httpN, cf)
		}
		rows = append(rows, cr)
	} else {
		rows = append(rows, measureMux(ctx, d, origin, nbyte, timeout, kind)...)
		rows = append(rows, measureSOCKS(ctx, socksAddr, origin, nbyte, httpN, timeout, kind)...)
	}
	if g, ok := d.(*transport.GET); ok {
		fmt.Fprintf(os.Stderr, "%s\n", g.TripSummary())
	}
	return rows, nil
}

// row is one printed result line.
type row struct {
	Kind  string
	Path  string
	MS    float64
	Down  float64
	Up    float64
	Err   string
	Extra string
}

// anyFail is true when a case recorded Err.
func anyFail(rows []row) bool {
	for _, r := range rows {
		if r.Err != "" && r.Path != "socks-tcp" {
			return true
		}
	}
	return false
}

// printTable writes a fixed-width summary to stdout.
func printTable(rows []row) {
	fmt.Printf("%-6s %-14s %8s %10s %10s  %s\n", "type", "path", "ms", "down_Mbit", "up_Mbit", "note")
	for _, r := range rows {
		note := r.Extra
		if r.Err != "" {
			note = r.Err
		}
		fmt.Printf("%-6s %-14s %8.0f %10.2f %10.2f  %s\n", r.Kind, r.Path, r.MS, r.Down, r.Up, note)
	}
}

// Command loft-bench repeats the macOS app connect path (front, SOCKS5,
// TCP probe) and measures mux/SOCKS throughput for GET and WS.
package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/a3app/loft/clients/go/inbound"
	"github.com/a3app/loft/internal/protect"
	"github.com/a3app/loft/internal/transport"
	"github.com/a3app/loft/internal/version"
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
	timeout := flag.Duration("timeout", 45*time.Second, "per-case deadline")
	ifName := flag.String("if", "", "outbound interface or local IP")
	h2MaxAge := flag.Duration("h2-max-age", 0, "GET HTTP/2 recycle; 0 keep until error")
	flag.Parse()
	if *h2MaxAge > 0 {
		outboundH2MaxAgeMS = int(h2MaxAge.Milliseconds())
	}
	benchDecoy = strings.TrimSpace(*decoy)
	benchPBK = strings.TrimSpace(*pbk)

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

	fmt.Fprintf(os.Stderr, "loft-bench url=%s origin=%s cdn=%s mux=%v live=%v switch=%v bytes=%d http=%d\n", *rawURL, *origin, *cdnName, *muxOn, *live, *switchOn, *bytesN, *httpN)
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
	var rows []row
	direct := measureDirect(ctx, *httpN, *timeout)
	rows = append(rows, direct...)

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
	ip := transport.OverlayIP()
	fmt.Fprintf(os.Stderr, "session type=%s front=%s overlay=%s mux=%v handshake=%.0fms %s\n", kind, front, ip, muxOn, hsMS, transport.SessionLine(raw))

	h := &transport.Holder{}
	h.Set(d)
	ln, err := inbound.ListenSOCKS5("127.0.0.1:0", h)
	if err != nil {
		return nil, fmt.Errorf("socks: %w", err)
	}
	defer ln.Close()
	socksAddr := ln.Addr().String()

	var rows []row
	note := "overlay=" + ip + " front=" + front
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

	if live {
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

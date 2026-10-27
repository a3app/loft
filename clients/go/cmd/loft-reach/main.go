// Command loft-reach measures whether Tor can reach its network from here:
// directly, through each of Tor Browser's built-in bridges, through bridges
// from a file, and through a running loft client's SOCKS5. Each case starts
// its own tor with a fresh data directory, so no case reuses what another
// one downloaded. The results go to a folder with report.md and one
// redacted log per case.
package main

import (
	"context"
	"flag"
	"fmt"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/a3app/loft/internal/config"
	"github.com/a3app/loft/internal/version"
)

// meta is what the person running the test tells us about the network, plus
// what the program finds out itself. It is printed at the top of report.md.
type meta struct {
	Network    string
	Place      string
	Note       string
	Started    time.Time
	Tool       string
	OS         string
	TorVersion string
	TorBrowser string
	LoftSOCKS  string
}

func main() {
	torBrowser := flag.String("tor-browser", "", "Tor Browser folder (tor, pluggable transports and built-in bridges are taken from it); empty = look in the usual places")
	torBin := flag.String("tor", "", "tor binary; overrides the one in Tor Browser")
	bridges := flag.String("bridges", "", "file with extra bridge lines (obfs4 ..., webtunnel ..., snowflake ...), one per line")
	lyrebird := flag.String("lyrebird", "", "lyrebird binary for obfs4, meek_lite, webtunnel bridges from -bridges")
	snowflake := flag.String("snowflake", "", "snowflake-client binary for snowflake bridges from -bridges")
	loftSOCKS := flag.String("loft-socks", config.DefaultSOCKS5, "SOCKS5 of a running loft client; empty = skip the loft case")
	cases := flag.String("cases", "", "comma list of cases to run (direct, builtin:obfs4, file:webtunnel, loft, ...); empty = all")
	timeout := flag.Duration("timeout", 3*time.Minute, "how long one case may take to reach 100%")
	onion := flag.String("onion", "", "optional http://….onion/ URL to fetch after bootstrap")
	out := flag.String("out", "", "results folder; empty = ./loft-reach-<UTC time>")
	network := flag.String("network", "", "operator and kind of line, e.g. \"MTS mobile 4G\" (goes into the report)")
	place := flag.String("place", "", "city or region (goes into the report)")
	note := flag.String("note", "", "anything else worth keeping with the results, e.g. which loft front is in use")
	list := flag.Bool("list", false, "print the cases that would run and exit")
	flag.Usage = usage
	flag.Parse()

	env, err := findTor(*torBrowser, *torBin, *lyrebird, *snowflake)
	if err != nil {
		fmt.Fprintf(os.Stderr, "loft-reach: %v\n", err)
		os.Exit(2)
	}
	extra, err := readBridgeFile(*bridges)
	if err != nil {
		fmt.Fprintf(os.Stderr, "loft-reach: -bridges: %v\n", err)
		os.Exit(2)
	}
	all := buildCases(env, extra, strings.TrimSpace(*loftSOCKS))
	run, err := pickCases(all, *cases)
	if err != nil {
		fmt.Fprintf(os.Stderr, "loft-reach: %v\n", err)
		os.Exit(2)
	}
	if *list {
		for _, c := range run {
			fmt.Printf("%-20s %s\n", c.Name, c.Describe())
		}
		return
	}

	dir := *out
	if dir == "" {
		dir = "loft-reach-" + time.Now().UTC().Format("20060102-150405")
	}
	if err := os.MkdirAll(filepath.Join(dir, "logs"), 0o755); err != nil {
		fmt.Fprintf(os.Stderr, "loft-reach: %v\n", err)
		os.Exit(1)
	}

	m := meta{
		Network:    strings.TrimSpace(*network),
		Place:      strings.TrimSpace(*place),
		Note:       strings.TrimSpace(*note),
		Started:    time.Now().UTC(),
		Tool:       version.Line("loft-reach"),
		OS:         osLine(),
		TorVersion: torVersion(env.Tor),
		TorBrowser: redactPath(env.BrowserDir),
		LoftSOCKS:  strings.TrimSpace(*loftSOCKS),
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	var results []result
	for i, c := range run {
		fmt.Fprintf(os.Stderr, "[%d/%d] %s … ", i+1, len(run), c.Name)
		r := runCase(ctx, env, c, *timeout, strings.TrimSpace(*onion))
		results = append(results, r)
		fmt.Fprintln(os.Stderr, r.Short())
		if err := os.WriteFile(filepath.Join(dir, "logs", r.LogName()), []byte(r.Log), 0o644); err != nil {
			fmt.Fprintf(os.Stderr, "loft-reach: %v\n", err)
		}
		if ctx.Err() != nil {
			break
		}
	}

	report := filepath.Join(dir, "report.md")
	if err := os.WriteFile(report, []byte(renderReport(m, results)), 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "loft-reach: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "report: %s\n", report)
}

// socksUp reports whether something listens on addr.
func socksUp(addr string) bool {
	c, err := net.DialTimeout("tcp", addr, 2*time.Second)
	if err != nil {
		return false
	}
	_ = c.Close()
	return true
}

func usage() {
	fmt.Fprintf(os.Stderr, `loft-reach: can Tor reach its network from here, and through loft?

Every case starts its own tor with an empty data directory and waits for
"Bootstrapped 100%%". After that it asks check.torproject.org whether the
request really came out of Tor, and fetches -onion if given.

Cases:
  direct            tor with no bridges
  builtin:<name>    Tor Browser's built-in bridges of that kind (obfs4, snowflake, meek-azure, ...)
  file:<transport>  bridges of that transport from -bridges
  loft              tor with Socks5Proxy = -loft-socks (start the loft client first)

Results: <out>/report.md and <out>/logs/<case>.log. IP addresses, bridge
fingerprints and certificates, and your home folder are replaced in both.

Examples:
  loft-reach -network "MTS mobile 4G" -place Moscow
  loft-reach -tor-browser "/Applications/Tor Browser.app" -cases direct,builtin:obfs4,loft
  loft-reach -bridges my-bridges.txt -lyrebird /usr/bin/lyrebird -loft-socks 127.0.0.1:1080

Flags:
`)
	flag.PrintDefaults()
}

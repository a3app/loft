// Command loft-probe sends ICMP, TCP, UDP, DNS, HTTP, and HTTPS through
// loft mux or SOCKS5 without rewriting system routes or opening utun.
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

	"github.com/a3app/loft/internal/config"
	"github.com/a3app/loft/internal/protect"
)

// main parses flags, opens mux or SOCKS, and prints one result line per probe.
func main() {
	token := flag.String("token", os.Getenv("LOFT_TOKEN"), "member key")
	rawURL := flag.String("url", os.Getenv("LOFT_URL"), "front URL")
	kind := flag.String("type", "auto", "get | ws | h2 | quic | h3 | sstp | auto")
	path := flag.String("path", "/static/js", "GET path")
	user := flag.String("user", "", "SSTP username")
	sni := flag.String("sni", "", "SSTP TLS ServerName")
	pap := flag.Bool("pap", false, "SSTP: allow PAP and CHAP-MD5")
	cfgPath := flag.String("c", "", "client profile YAML (loft or SSTP)")
	viaKind := flag.String("via", "auto", "mux | socks | sstp | auto")
	socks := flag.String("socks", config.DefaultSOCKS5, "SOCKS5 of a live loft session")
	proto := flag.String("proto", "auto", "auto | all | icmp | tcp | udp | dns | http | https")
	count := flag.Int("count", 1, "ICMP echoes (suite icmp uses this too)")
	timeout := flag.Duration("timeout", 8*time.Second, "per-probe timeout")
	qname := flag.String("q", "example.com", "DNS question name")
	insecure := flag.Bool("insecure", false, "skip TLS verify (https probes and SSTP)")
	verbose := flag.Bool("v", false, "SSTP packet and DNS traces")
	dnsList := flag.String("dns", "", "SSTP DNS IPv4, comma/space; empty = profile, else IPCP then gateway")
	dnsReplace := flag.Bool("dns-replace", false, "SSTP: use only -dns / profile dns, skip IPCP and gateway")
	bodyBytes := flag.Int("bytes", 0, "HTTP(S): read this many body bytes and print B/s + stalls (0 = 256-byte snippet)")
	uploadBytes := flag.Int("upload", 0, "HTTP(S): POST this many body bytes and print upload B/s")
	ifName := flag.String("if", "", "outbound interface or local IP")
	flag.Usage = usage
	flag.Parse()
	if err := protect.BindUplink(*ifName); err != nil {
		fmt.Fprintf(os.Stderr, "loft-probe: -if: %v\n", err)
		os.Exit(2)
	}
	if s := protect.Bound(); s != "" {
		fmt.Fprintf(os.Stderr, "if %s\n", s)
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	specs, err := collectSpecs(flag.Args(), *proto)
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(2)
	}

	hs, cancelHS := context.WithTimeout(ctx, 45*time.Second)
	v, err := openVia(hs, viaArgs{
		viaKind: *viaKind, token: *token, rawURL: *rawURL, kind: *kind, path: *path,
		cfgPath: *cfgPath, socks: *socks, user: *user, sni: *sni,
		insecure: *insecure, pap: *pap, verbose: *verbose, wantICMP: needsICMP(specs),
		dns: *dnsList, dnsReplace: *dnsReplace,
	})
	cancelHS()
	if err != nil {
		fmt.Fprintf(os.Stderr, "loft-probe: %v\n", err)
		os.Exit(2)
	}
	defer v.close()

	fmt.Fprintf(os.Stderr, "loft-probe via=%s %s  (no system routes)\n", v.kind(), v.detail())

	var rs []result
	for _, sp := range specs {
		n := 1
		if sp.proto == "icmp" {
			n = *count
			if n < 1 {
				n = 1
			}
		}
		rs = append(rs, runSpec(ctx, v, sp, n, *timeout, *qname, *insecure, *bodyBytes, *uploadBytes)...)
	}
	fails := printResults(rs)
	if fails > 0 {
		os.Exit(1)
	}
}

// needsICMP is true when any spec is ICMP (SOCKS cannot carry those).
func needsICMP(specs []spec) bool {
	for _, s := range specs {
		if s.proto == "icmp" {
			return true
		}
	}
	return false
}

// collectSpecs builds the probe list from args, -proto, or the default suite.
func collectSpecs(args []string, protoFlag string) ([]spec, error) {
	protoFlag = strings.ToLower(strings.TrimSpace(protoFlag))
	if len(args) == 0 {
		if protoFlag == "" || protoFlag == "auto" || protoFlag == "all" {
			return defaultSuite(), nil
		}
		suite := defaultSuite()
		var out []spec
		for _, s := range suite {
			if s.proto == protoFlag {
				out = append(out, s)
			}
		}
		if len(out) == 0 {
			return nil, fmt.Errorf("no default target for -proto %s (pass a dest)", protoFlag)
		}
		return out, nil
	}
	var out []spec
	for _, a := range args {
		if protoFlag == "all" {
			out = append(out, expandAll(a)...)
			continue
		}
		sp, err := parseSpec(a, protoFlag)
		if err != nil {
			return nil, err
		}
		out = append(out, sp)
	}
	return out, nil
}

type viaArgs struct {
	viaKind, token, rawURL, kind, path, cfgPath, socks, user, sni string
	insecure, pap, verbose, wantICMP                              bool
	dns                                                           string
	dnsReplace                                                    bool
}

// openVia picks SSTP, mux, or SOCKS from -via, profile, token/url, or a live SOCKS port.
func openVia(ctx context.Context, a viaArgs) (via, error) {
	viaKind := strings.ToLower(strings.TrimSpace(a.viaKind))
	kind := strings.ToLower(strings.TrimSpace(a.kind))
	if viaKind == "sstp" || kind == "sstp" {
		return openSSTPFrom(ctx, a)
	}
	switch viaKind {
	case "socks":
		if a.wantICMP {
			fmt.Fprintln(os.Stderr, "icmp needs mux or sstp; SOCKS will skip icmp probes")
		}
		addr := strings.TrimSpace(a.socks)
		if addr == "" {
			addr = liveSOCKS("")
		}
		if addr == "" {
			return nil, fmt.Errorf("pass -socks host:port")
		}
		return openSocks(addr)
	case "mux":
		return openMuxFrom(ctx, a.token, a.rawURL, a.kind, a.path, a.cfgPath)
	case "auto", "":
		if p := a.cfgPath; p != "" || os.Getenv("LOFT_PROFILE") != "" {
			if p == "" {
				p = os.Getenv("LOFT_PROFILE")
			}
			if gp, err := loadGUIProfile(p); err == nil {
				if _, ok := gp.sstpOutbound(); ok {
					a.cfgPath = p
					return openSSTPFrom(ctx, a)
				}
			}
		}
		if a.token != "" && a.rawURL != "" {
			return openMuxFrom(ctx, a.token, a.rawURL, a.kind, a.path, a.cfgPath)
		}
		if a.cfgPath != "" || os.Getenv("LOFT_PROFILE") != "" {
			return openMuxFrom(ctx, a.token, a.rawURL, a.kind, a.path, a.cfgPath)
		}
		if p, err := findProfile(""); err == nil {
			if gp, err := loadGUIProfile(p); err == nil {
				if _, ok := gp.sstpOutbound(); ok {
					a.cfgPath = p
					return openSSTPFrom(ctx, a)
				}
			}
			return openMuxFrom(ctx, a.token, a.rawURL, a.kind, a.path, p)
		}
		if addr := liveSOCKS(a.socks); addr != "" {
			if a.wantICMP {
				fmt.Fprintln(os.Stderr, "no profile; live SOCKS (icmp skipped, use -c)")
			}
			return openSocks(addr)
		}
		return nil, fmt.Errorf("pass -c profile, -token and -url, or connect loft and use -via socks")
	default:
		return nil, fmt.Errorf("unknown -via %q (mux | socks | sstp | auto)", viaKind)
	}
}

func openSSTPFrom(ctx context.Context, a viaArgs) (via, error) {
	url, user, pass, sni := a.rawURL, a.user, a.token, a.sni
	insecure, pap := a.insecure, a.pap
	dns, dnsReplace := a.dns, a.dnsReplace
	if a.cfgPath != "" {
		p, err := loadGUIProfile(a.cfgPath)
		if err != nil {
			return nil, err
		}
		if o, ok := p.sstpOutbound(); ok {
			if url == "" {
				url = o.URL
			}
			if sni == "" {
				sni = p.SNI
			}
			if sni == "" {
				sni = o.SNI
			}
		} else if url == "" {
			return nil, fmt.Errorf("%s: no sstp outbound", a.cfgPath)
		}
		if user == "" {
			user = p.Username
		}
		if pass == "" {
			pass = p.Password
		}
		if p.PAPCHAP {
			pap = true
		}
		if p.tlsInsecure() {
			insecure = true
		}
		if dns == "" {
			dns = p.dnsString()
		}
		if p.DNSReplace {
			dnsReplace = true
		}
		fmt.Fprintf(os.Stderr, "profile %s  sstp handshake…\n", a.cfgPath)
	} else {
		fmt.Fprintln(os.Stderr, "sstp handshake…")
	}
	if url == "" || user == "" || pass == "" {
		return nil, fmt.Errorf("sstp needs -url -user -token (password) or -c profile")
	}
	return openSSTP(ctx, url, user, pass, sni, insecure, pap, a.verbose, dns, dnsReplace)
}

// openMuxFrom handshakes a front from -token/-url or the newest client profile.
func openMuxFrom(ctx context.Context, token, rawURL, kind, path, cfgPath string) (via, error) {
	if token != "" && rawURL != "" {
		fmt.Fprintln(os.Stderr, "front handshake…")
		return openMux(ctx, token, rawURL, kind, path, "")
	}
	p, err := findProfile(cfgPath)
	if err != nil {
		if liveSOCKS(config.DefaultSOCKS5) != "" {
			return nil, fmt.Errorf("%w; or use -via socks while loft is connected", err)
		}
		return nil, err
	}
	cfg, err := loadProfile(p)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", p, err)
	}
	o := pickOutbound(cfg)
	if kind == "" || kind == "auto" {
		kind = o.Type
	}
	if path == "" || path == "/static/js" {
		if o.Path != "" {
			path = o.Path
		}
	}
	url := rawURL
	if url == "" {
		url = o.URL
	}
	tok := token
	if tok == "" {
		tok = cfg.Token
	}
	fmt.Fprintf(os.Stderr, "profile %s  front handshake…\n", p)
	return openMux(ctx, tok, url, kind, path, o.CDN)
}

// usage prints loft-probe help and flag defaults.
func usage() {
	fmt.Fprintf(os.Stderr, `loft-probe — overlay egress debugger.

Sends ICMP, TCP, UDP, DNS, HTTP, HTTPS through loft without rewriting
system routes and without opening utun.

  loft-probe                         default suite (icmp/tcp/dns/http/https)
  loft-probe 8.8.8.8                 ICMP echo via overlay
  loft-probe -proto tcp 1.1.1.1:443
  loft-probe -proto http https://ifconfig.me/ip
  loft-probe -proto https -bytes 100000 -timeout 40s https://speed.cloudflare.com/__down?bytes=100000
  loft-probe -proto dns 8.8.8.8
  loft-probe -proto all 1.1.1.1
  loft-probe -if en0 -c ~/Library/Application\ Support/loft/profiles/example.yaml 8.8.8.8
  loft-probe -via socks example.com:443
  loft-probe -c ~/Library/Application\ Support/loft/profiles/example.yaml 8.8.8.8
  loft-probe -c ~/Library/Application\ Support/loft/profiles/kraska.yaml -v -proto https https://ifconfig.me/ip
  loft-probe -type sstp -url https://host -user NAME -token PASS -proto tcp 1.1.1.1:443

-via mux    own GET/WS/H2/QUIC/H3 session (covers ICMP). Uses -token/-url or -c profile.
-via sstp   SSTP+PPP userspace stack (TCP/UDP/ICMP/DNS). -c GUI yaml or -user/-token/-url.
-via socks  live loft SOCKS5 (TCP/UDP/HTTP). ICMP is skipped.
-via auto   SSTP if the profile is SSTP, else mux, else SOCKS.

-if en0    bind the front (and DNS) to that interface or a local/gateway IPv4.

Does not call loft-priv, does not install 0/1, does not change DNS.

`)
	flag.PrintDefaults()
}

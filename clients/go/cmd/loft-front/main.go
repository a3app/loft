// Command loft-front is the GUI helper: GET/WS/H2/QUIC/H3/L3 front, SOCKS5, and
// in-process or loft-priv TUN. It prints STATUS lines on stdout.
//
// The desktop apps start loft-front as a child process: it connects to the
// server over one or more "fronts" (ways of packing loft traffic into HTTPS:
// get, ws, h2, quic, h3, l3), or speaks SSTP, VLESS or Trojan to a foreign
// server, and gives the apps a local SOCKS5 port and optionally a TUN device
// (a virtual network card that captures all traffic). loft-priv is the small
// privileged helper that creates the TUN and passes its file descriptor here
// over a unix socket. The app reads the STATUS lines (state, overlay IP,
// byte rates) to draw its window.
package main

import (
	"context"
	"flag"
	"fmt"
	"net/url"
	"os"
	"os/signal"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/a3app/loft/clients/go/inbound"
	"github.com/a3app/loft/internal/cdn"
	"github.com/a3app/loft/internal/config"
	"github.com/a3app/loft/internal/protect"
	"github.com/a3app/loft/internal/sstp"
	"github.com/a3app/loft/internal/transport"
	"github.com/a3app/loft/internal/trojan"
	"github.com/a3app/loft/internal/vless"
	"github.com/a3app/loft/internal/wire"
)

// frontDecoy and frontPBK are -decoy / -pbk for direct loft fronts.
var frontDecoy, frontPBK string

// Per-front CLI paths. Empty uses outboundFor defaults / GET-cover remap.
var frontWSPath, frontH2Path, frontH3Path string

// statusFront is the live bulk front Supervise last handed the GUI (STATUS front=).
var statusFrontMu sync.Mutex
var statusFront string

// frontH2MaxAgeMS is -h2-max-age in milliseconds (0 = keep HTTP/2 until error).
var frontH2MaxAgeMS int

// main connects a loft front and serves SOCKS5 and optional TUN for the GUI.
func main() {
	token := flag.String("token", "", "member key")
	rawURL := flag.String("url", "", "front URL")
	kind := flag.String("type", "auto", "get | ws | h2 | quic | h3 | l3 | all | get,ws,h2 | sstp | vless | trojan | auto")
	helloFronts := flag.String("fronts", "", "Hello mux fronts; empty = outbound types. Comma -type still rotates.")
	path := flag.String("path", "", "GET path; empty get is "+config.DefaultGETPath)
	wsPath := flag.String("ws-path", "", "WS path; empty is "+config.DefaultWSPath)
	h2Path := flag.String("h2-path", "", "H2 CONNECT path; empty remaps GET cover to "+config.DefaultH2Path)
	h3Path := flag.String("h3-path", "", "H3 CONNECT path; empty same as h2")
	user := flag.String("user", "", "SSTP username")
	insecure := flag.Bool("insecure", false, "SSTP: skip TLS verify")
	pap := flag.Bool("pap", false, "SSTP: allow PAP and CHAP-MD5")
	sni := flag.String("sni", "", "SSTP/VLESS/Trojan TLS ServerName")
	socks := flag.String("socks", config.DebugSOCKS5, "SOCKS5 listen; empty/off to disable")
	cdnName := flag.String("cdn", "", "edge profile: yandex | cloudflare")
	innerName := flag.String("inner", "", "data cipher ask: clear | chacha | aes; empty = chacha")
	ccName := flag.String("cc", "", "congestion control ask: off | bbr | fixed; empty = off")
	ccUp := flag.Uint("cc-up", 0, "fixed: upload rate, Mbit/s")
	ccDown := flag.Uint("cc-down", 0, "fixed: download rate, Mbit/s")
	edgeURL := flag.String("edge", "", "CDN URL for the first GET; other fronts use -url")
	decoy := flag.String("decoy", "", "direct-path decoy SNI (dest hostname)")
	pbk := flag.String("pbk", "", "node X25519 public, hex or base64url")
	tun := flag.String("tun", "", "TUN device; empty = off, auto = create (needs root)")
	tunNoexec := flag.Bool("tun-noexec", false, "attach named TUN, skip addr/routes/DNS")
	tunSock := flag.String("tun-sock", "", "unix socket; loft-priv sends utun fd")
	tunPipe := flag.String("tun-pipe", "", "named pipe of length-prefixed IP packets; address and routes stay outside")
	probe := flag.Bool("probe", false, "handshake then exit")
	muxOn := flag.Bool("mux", true, "inner mux; false = HTTP/2 CONNECT splice")
	verbose := flag.Bool("v", false, "SSTP: packet and DNS traces on stderr")
	dnsList := flag.String("dns", "", "SSTP DNS IPv4, comma/space; empty = IPCP then PPP gateway")
	dnsReplace := flag.Bool("dns-replace", false, "SSTP: use only -dns, skip IPCP and gateway")
	instanceFile := flag.String("instance-file", "", "persist overlay instance_id (one file per GUI profile)")
	ifName := flag.String("if", "", "outbound interface or local IP")
	driveOn := flag.Bool("drive", false, "drive-debug log (handoff, stall, handshake)")
	traceFile := flag.String("trace", "", "drive log file; implies -drive")
	h2MaxAge := flag.Duration("h2-max-age", 0, "GET HTTP/2 recycle; 0 keep until error")
	flag.Parse()
	if *h2MaxAge > 0 {
		frontH2MaxAgeMS = int(h2MaxAge.Milliseconds())
	}
	if err := protect.BindUplink(*ifName); err != nil {
		fmt.Fprintf(os.Stderr, "loft-front: -if: %v\n", err)
		os.Exit(2)
	}
	if s := protect.Bound(); s != "" {
		fmt.Fprintf(os.Stderr, "if %s\n", s)
	}
	if *traceFile != "" {
		*driveOn = true
	}
	var drv *transport.Drive
	if *driveOn {
		logPath := *traceFile
		if logPath == "" {
			logPath = transport.DefaultDriveLog()
		}
		var err error
		drv, err = transport.OpenDriveLog(logPath)
		if err != nil {
			fmt.Fprintf(os.Stderr, "loft-front: -trace: %v\n", err)
			os.Exit(2)
		}
		defer func() {
			drv.Event("STOP")
			_ = drv.Close()
		}()
		transport.SetDrive(drv)
		drv.SetFront(*rawURL)
		fmt.Fprintf(os.Stderr, "drive log %s\n", logPath)
		start := []string{"if=" + protect.Bound(), "url=" + *rawURL, "type=" + *kind}
		if s := strings.TrimSpace(*edgeURL); s != "" {
			start = append(start, "edge="+s)
		}
		drv.Event("START", start...)
	}
	frontDecoy = strings.TrimSpace(*decoy)
	frontPBK = strings.TrimSpace(*pbk)
	frontWSPath = strings.TrimSpace(*wsPath)
	frontH2Path = strings.TrimSpace(*h2Path)
	frontH3Path = strings.TrimSpace(*h3Path)
	if strings.EqualFold(*kind, "auto") {
		switch {
		case strings.HasPrefix(strings.ToLower(*rawURL), "vless://"):
			*kind = "vless"
		case strings.HasPrefix(strings.ToLower(*rawURL), "trojan://"):
			*kind = "trojan"
		}
	}
	if strings.EqualFold(*kind, "vless") || strings.EqualFold(*kind, "trojan") {
		if *rawURL == "" {
			fmt.Fprintln(os.Stderr, "loft-front: -url required")
			os.Exit(2)
		}
	} else if *token == "" || *rawURL == "" {
		fmt.Fprintln(os.Stderr, "loft-front: -token and -url required")
		os.Exit(2)
	}
	if *instanceFile != "" {
		transport.SetInstanceFile(*instanceFile)
	}

	// The context ends on Ctrl-C or SIGTERM; everything below stops with it.
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// SSTP, VLESS and Trojan are not loft protocols and have their own loops.
	if strings.EqualFold(*kind, "sstp") {
		runSSTP(ctx, *rawURL, *user, *token, *socks, *tunSock, *probe, *insecure, *pap, *sni, *verbose, *dnsList, *dnsReplace)
		return
	}
	if strings.EqualFold(*kind, "vless") {
		runVLESS(ctx, *rawURL, *token, *socks, *tunSock, *probe, *insecure, *sni)
		return
	}
	if strings.EqualFold(*kind, "trojan") {
		runTrojan(ctx, *rawURL, *token, *socks, *tunSock, *probe, *insecure, *sni)
		return
	}

	// Build the loft client config from the flags and check the cipher and
	// congestion-control asks before dialing anything.
	status("connecting", "front", "", false)

	cfg := frontConfig(*token, *rawURL, *kind, *path, *muxOn, *cdnName)
	cfg.Inner = strings.TrimSpace(*innerName)
	if _, _, err := cfg.InnerAsk(); err != nil {
		fmt.Fprintf(os.Stderr, "loft-front: %v\n", err)
		os.Exit(2)
	}
	cfg.CC, cfg.CCUpMbps, cfg.CCDownMbps = strings.TrimSpace(*ccName), uint32(*ccUp), uint32(*ccDown)
	if _, _, _, err := cfg.CCAsk(); err != nil {
		fmt.Fprintf(os.Stderr, "loft-front: %v\n", err)
		os.Exit(2)
	}
	if s := strings.TrimSpace(*helloFronts); s != "" {
		cfg.Fronts = parseKindList(s)
	}
	// -fronts is the mux set Supervise tries. -edge is a GET inserted in
	// front of that set. WS, H2, QUIC, H3 and the fallback GET keep -url.
	addEnabledFronts(cfg)
	if err := config.PinEdgeGET(cfg, *edgeURL); err != nil {
		fmt.Fprintf(os.Stderr, "loft-front: %v\n", err)
		os.Exit(2)
	}
	// -url is the node. Edge GET dials the CDN; ws/h2/quic/h3 and mask_pub
	// stay on the node address.
	transport.SetDirectNode(*rawURL)
	for _, o := range cfg.Outbounds {
		transport.NoteOutbound(o)
	}
	if *probe {
		probeOutbounds(ctx, cfg, !multiKind(*kind))
		return
	}

	// The Holder is what SOCKS5 and TUN dial through. Supervise swaps the live
	// front inside it on every reconnect, so local connections survive.
	h := &transport.Holder{}
	defer h.Close()
	// atomic.Value rejects a second concrete type. get then h3 is a panic.
	var liveMu sync.Mutex
	var liveRaw transport.Dialer
	onSwitch = func() {
		liveMu.Lock()
		d := liveRaw
		liveMu.Unlock()
		if d == nil {
			fmt.Fprintln(os.Stderr, "switch: no front")
			return
		}
		fmt.Fprintln(os.Stderr, "switch: drop live front")
		_ = d.Close()
	}
	defer serveSOCKS(ctx, *socks, h).Close()

	// TUN state. bringTUN runs after each (re)connect: the first time it
	// creates the device (named pipe, existing device or a new one), later it
	// only re-attaches the Holder.
	var (
		tunMu   sync.Mutex
		tunDev  *inbound.Device
		tunLive atomic.Bool
		resumes atomic.Uint64
	)
	exclude := hostsOf(cfg, *rawURL)
	bringTUN := func() {
		ip := transport.OverlayIP()
		if ip == "" {
			return
		}
		tunMu.Lock()
		defer tunMu.Unlock()
		if tunDev != nil {
			tunDev.Attach(h)
			return
		}
		if *tunPipe != "" {
			ifc, err := inbound.OpenPacketPipe(*tunPipe)
			if err != nil {
				fmt.Fprintf(os.Stderr, "tun pipe: %v\n", err)
				return
			}
			c, err := inbound.StartTUNIO(ifc, ipCIDR(ip), h)
			if err != nil {
				fmt.Fprintf(os.Stderr, "tun: %v\n", err)
				return
			}
			tunDev = c
			tunLive.Store(true)
			fmt.Fprintf(os.Stderr, "tun up pipe\n")
			return
		}
		if *tun == "" {
			return
		}
		var (
			c   *inbound.Device
			err error
		)
		if *tunNoexec {
			c, err = inbound.AttachTUN(*tun, ipCIDR(ip), h)
		} else {
			c, err = inbound.StartTUN(*tun, ipCIDR(ip), false, h, exclude)
		}
		if err != nil {
			fmt.Fprintf(os.Stderr, "tun: %v\n", err)
			return
		}
		tunDev = c
		tunLive.Store(true)
		fmt.Fprintf(os.Stderr, "tun up %s\n", *tun)
	}
	defer func() {
		tunMu.Lock()
		c := tunDev
		tunDev = nil
		tunMu.Unlock()
		_ = c.Close()
	}()

	// With -tun-sock, loft-priv creates the utun and sends its fd over the
	// socket; the device starts as soon as the fd arrives.
	if *tunSock != "" {
		ln, err := inbound.ListenTUNSock(*tunSock)
		if err != nil {
			fmt.Fprintf(os.Stderr, "tun sock: %v\n", err)
		} else {
			defer ln.Close()
			go func() {
				fd, name, err := inbound.AcceptTUNFD(ln)
				if err != nil {
					fmt.Fprintf(os.Stderr, "tun fd: %v\n", err)
					return
				}
				c, err := inbound.StartTUNFromFD(fd, name, h)
				if err != nil {
					fmt.Fprintf(os.Stderr, "tun: %v\n", err)
					return
				}
				tunMu.Lock()
				tunDev = c
				tunMu.Unlock()
				tunLive.Store(true)
				fmt.Fprintf(os.Stderr, "tun up %s\n", name)
				status("on", "", transport.OverlayIP(), true)
				<-ctx.Done()
			}()
		}
	}

	// Run Supervise: keep one front up, reconnect on network change or stall,
	// and on each new front update the Holder, the TUN and the STATUS line.
	first := make(chan struct{})
	var once sync.Once
	pathCh := transport.MergePath(ctx, transport.WatchPath(ctx), transport.WatchStall(ctx, h, drv))
	go func() {
		err := transport.SuperviseOn(ctx, cfg, pathCh, func(d transport.Dialer, o config.Outbound) {
			// Supervise waits on this dialer. The holder may swap in an
			// L3Net wrapper; switch must close the dialer, not the wrapper.
			raw := d
			if live, ok := h.Live().(*inbound.L3Net); ok {
				if p, ok := d.(transport.IPPump); ok {
					live.Swap(d, p)
				} else {
					h.Set(d)
				}
			} else {
				if w, err := inbound.WrapL3(d); err != nil {
					fmt.Fprintf(os.Stderr, "l3 net: %v\n", err)
				} else {
					d = w
				}
				h.Set(d)
			}
			liveMu.Lock()
			liveRaw = raw
			liveMu.Unlock()
			setStatusFront(o.Type)
			ip := transport.OverlayIP()
			bringTUN()
			n := resumes.Add(1)
			line := transport.SessionLine(*rawURL)
			if n == 1 {
				fmt.Fprintf(os.Stderr, "front %s overlay=%s %s\n", o.Type, ip, line)
			} else {
				fmt.Fprintf(os.Stderr, "resume %s overlay=%s n=%d %s\n", o.Type, ip, n, line)
			}
			if drv != nil {
				drv.Event("LIVE", fmt.Sprintf("n=%d", n), "type="+o.Type, "overlay="+ip)
			}
			status("on", "", ip, tunLive.Load())
			once.Do(func() { close(first) })
		})
		if err != nil && ctx.Err() == nil {
			status("error", err.Error(), "", false)
			stop()
		}
	}()
	// Wait for the first connection, then report byte rates every second.
	select {
	case <-first:
	case <-ctx.Done():
		return
	}
	go spark(ctx, h, &tunLive)
	<-ctx.Done()
}

// multiKind is -type all or a comma list. Those sessions rotate in Supervise.
func multiKind(kind string) bool {
	k := strings.ToLower(strings.TrimSpace(kind))
	return k == "all" || strings.Contains(k, ",")
}

// probeOutbounds handshakes cfg.Outbounds. stopAtFirst returns on the first session.
func probeOutbounds(ctx context.Context, cfg *config.ClientConfig, stopAtFirst bool) {
	if cfg == nil || len(cfg.Outbounds) == 0 {
		status("error", "no front", "", false)
		os.Exit(2)
	}
	failed := 0
	for _, o := range cfg.Outbounds {
		status("connecting", o.Type, "", false)
		d, err := transport.Start(ctx, cfg.Token, o)
		if err != nil {
			failed++
			fmt.Fprintf(os.Stderr, "front %s: %v\n", o.Type, err)
			continue
		}
		ip := transport.OverlayIP()
		setStatusFront(o.Type)
		fmt.Fprintf(os.Stderr, "ok overlay=%s front=%s dns=%s %s\n", ip, o.Type, strings.Join(transport.OverlayDNS(), ","), transport.SessionLine(o.URL))
		_ = d.Close()
		if stopAtFirst {
			status("on", "", ip, false)
			return
		}
	}
	if failed == len(cfg.Outbounds) {
		status("error", "no front", "", false)
		os.Exit(2)
	}
}

// frontOrder is the try-list for -type. Stock public path is GET.
// all and a comma list are the mux fronts Supervise rotates.
// WS fallback is only for an explicit ws URL or -type ws.
func frontOrder(kind, raw string, muxOn, fallback bool) []string {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind == "all" {
		return append([]string{}, wire.FrontOrder...)
	}
	if strings.Contains(kind, ",") {
		return parseKindList(kind)
	}
	if kind == "auto" {
		kind = ""
	}
	if !muxOn && (kind == "" || kind == "get" || kind == "ws") {
		return []string{"get"}
	}
	switch kind {
	case "get":
		return []string{"get"}
	case "ws":
		if fallback {
			return []string{"ws", "get"}
		}
		return []string{"ws"}
	case "h2", "quic", "h3", "l3":
		return []string{kind}
	case "":
		if strings.HasPrefix(raw, "ws://") || strings.HasPrefix(raw, "wss://") {
			if fallback {
				return []string{"ws", "get"}
			}
			return []string{"ws"}
		}
		return []string{"get"}
	default:
		return []string{kind}
	}
}

// parseKindList keeps known front names, in the order given.
func parseKindList(kind string) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range strings.Split(kind, ",") {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		f, err := wire.ParseFront(p)
		if err != nil || seen[f] {
			continue
		}
		seen[f] = true
		out = append(out, f)
	}
	if len(out) == 0 {
		return []string{"get"}
	}
	return out
}

// addEnabledFronts appends mux fronts from cfg.Fronts that -type did not
// already dial. Order is GET, WS, H2, QUIC, H3. Mux-off stays one outbound.
func addEnabledFronts(cfg *config.ClientConfig) {
	if cfg == nil || len(cfg.Fronts) < 2 || len(cfg.Outbounds) == 0 {
		return
	}
	base := cfg.Outbounds[0]
	if base.Mux != nil && !*base.Mux {
		return
	}
	have := map[string]bool{}
	for _, o := range cfg.Outbounds {
		have[strings.ToLower(o.Type)] = true
	}
	want := map[string]bool{}
	for _, f := range cfg.Fronts {
		want[strings.ToLower(f)] = true
	}
	for _, f := range wire.FrontOrder {
		if !want[f] || have[f] {
			continue
		}
		o := outboundFor(base.URL, f, "", base.CDN)
		o.Insecure = base.Insecure
		o.Fingerprint = base.Fingerprint
		o.Decoy = base.Decoy
		o.MaskPub = base.MaskPub
		o.H2MaxAgeMS = base.H2MaxAgeMS
		cfg.Outbounds = append(cfg.Outbounds, o)
		have[f] = true
	}
}

// frontConfig is the in-memory ClientConfig Supervise uses for this helper.
func frontConfig(token, raw, kind, path string, muxOn bool, cdnName string) *config.ClientConfig {
	cfg := &config.ClientConfig{Token: token, Select: "auto"}
	for _, t := range frontOrder(kind, raw, muxOn, false) {
		o := outboundFor(raw, t, path, cdnName)
		if !muxOn {
			f := false
			o.Mux = &f
		}
		cfg.Outbounds = append(cfg.Outbounds, o)
	}
	return cfg
}

// dial tries fronts in frontOrder and returns the first working Dialer.
func dial(ctx context.Context, token, raw, kind, path string, muxOn bool, cdnName string) (transport.Dialer, string, error) {
	var last error
	for _, t := range frontOrder(kind, raw, muxOn, true) {
		status("connecting", t, "", false)
		o := outboundFor(raw, t, path, cdnName)
		if !muxOn {
			f := false
			o.Mux = &f
		}
		d, err := transport.Start(ctx, token, o)
		if err == nil {
			setStatusFront(t)
			return d, t, nil
		}
		last = err
		fmt.Fprintf(os.Stderr, "front %s: %v\n", t, err)
	}
	if last == nil {
		last = fmt.Errorf("no front")
	}
	return nil, "", last
}

// outboundFor builds an Outbound for a get/ws/h2/quic/h3/l3 front URL.
func outboundFor(raw, kind, path, cdnName string) config.Outbound {
	o := config.Outbound{
		Name:         "loft",
		Type:         kind,
		URL:          raw,
		Path:         path,
		CDN:          strings.TrimSpace(cdnName),
		Decoy:        strings.TrimSpace(frontDecoy),
		MaskPub:      strings.TrimSpace(frontPBK),
		Fingerprint:  "chrome",
		Insecure:     true,
		HeaderBudget: 16 << 10,
		Inflight:     8,
		H2MaxAgeMS:   frontH2MaxAgeMS,
	}
	if p, err := cdn.Lookup(o.CDN); err == nil && o.CDN != "" {
		o.HeaderBudget = p.HeaderBudget
		if p.Inflight > 0 {
			o.Inflight = p.Inflight
		}
		if strings.EqualFold(p.Name, "yandex") {
			o.Insecure = false
		}
	}
	switch kind {
	case "get":
		o.URL = httpOrigin(raw)
		if o.Path == "" {
			o.Path = config.DefaultGETPath
		}
	case "ws":
		wsPath := config.DefaultWSPath
		if frontWSPath != "" {
			wsPath = frontWSPath
			o.URL = forceWSPath(wsURL(raw), wsPath)
		} else {
			o.URL = withWSPath(wsURL(raw), wsPath)
		}
	case "h2", "h3", "l3":
		o.URL = httpOrigin(raw)
		custom := ""
		if kind == "h2" {
			custom = frontH2Path
		}
		if kind == "h3" {
			custom = frontH3Path
		}
		if custom != "" {
			o.Path = custom
		} else if o.Path == "" || getCoverPath(o.Path) {
			// GUI used to pass the GET cover (/static/js) for every loft kind.
			// CONNECT Match is /api/v1/tunnel; a leftover GET path is 404.
			o.Path = config.DefaultH2Path
		}
	case "quic":
		o.URL = httpOrigin(raw)
	}
	return o
}

// getCoverPath is a GET static cover. It is not an h2/h3 CONNECT resource.
func getCoverPath(p string) bool {
	p = strings.TrimSpace(p)
	return strings.HasPrefix(p, "/static/") || strings.HasSuffix(p, ".js")
}

// httpOrigin maps a ws(s) URL to the matching http(s) origin.
func httpOrigin(raw string) string {
	s := strings.TrimSpace(raw)
	switch {
	case strings.HasPrefix(s, "wss://"):
		s = "https://" + strings.TrimPrefix(s, "wss://")
	case strings.HasPrefix(s, "ws://"):
		s = "http://" + strings.TrimPrefix(s, "ws://")
	}
	u, err := url.Parse(s)
	if err != nil {
		return s
	}
	host := u.Host
	if host == "" {
		host = u.Path
	}
	scheme := u.Scheme
	if scheme == "" {
		scheme = "https"
	}
	return scheme + "://" + host
}

// wsURL maps an http(s) URL to the matching ws(s) URL.
func wsURL(raw string) string {
	s := strings.TrimSpace(raw)
	if strings.HasPrefix(s, "https://") {
		return "wss://" + strings.TrimPrefix(s, "https://")
	}
	if strings.HasPrefix(s, "http://") {
		return "ws://" + strings.TrimPrefix(s, "http://")
	}
	if !strings.HasPrefix(s, "ws://") && !strings.HasPrefix(s, "wss://") {
		s = "wss://" + s
	}
	return s
}

// withWSPath puts fallback on a URL that has no path (cover is /).
func withWSPath(raw, fallback string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	if u.Path == "" || u.Path == "/" {
		u.Path = fallback
	}
	return u.String()
}

// forceWSPath sets the WS resource, replacing a leftover GET cover.
func forceWSPath(raw, path string) string {
	u, err := url.Parse(raw)
	if err != nil {
		return raw
	}
	if path != "" {
		u.Path = path
	}
	return u.String()
}

// setStatusFront is the live bulk front STATUS front= reports.
func setStatusFront(t string) {
	statusFrontMu.Lock()
	statusFront = t
	statusFrontMu.Unlock()
}

// ipCIDR appends /24 when overlay IP has no prefix.
func ipCIDR(ip string) string {
	if ip == "" {
		return "10.7.0.2/24"
	}
	if strings.Contains(ip, "/") {
		return ip
	}
	return ip + "/24"
}

// hostsOf is every dial hostname TUN split routes must not capture.
func hostsOf(cfg *config.ClientConfig, raw string) []string {
	out := outboundHosts(raw)
	seen := map[string]bool{}
	for _, h := range out {
		seen[h] = true
	}
	if cfg == nil {
		return out
	}
	for _, o := range cfg.Outbounds {
		for _, h := range outboundHosts(o.URL) {
			if h == "" || seen[h] {
				continue
			}
			seen[h] = true
			out = append(out, h)
		}
	}
	return out
}

// outboundHosts returns the front hostname so TUN split routes can exclude it.
func outboundHosts(raw string) []string {
	u, err := url.Parse(strings.Replace(strings.Replace(raw, "wss://", "https://", 1), "ws://", "http://", 1))
	if err != nil {
		return nil
	}
	h := u.Hostname()
	if h == "" {
		return nil
	}
	return []string{h}
}

// runVLESS is VLESS TCP+TLS: SOCKS5 and optional utun via the userspace stack.
//
// Steps: dial and probe the VLESS server (connection errors go to STATUS),
// fix the overlay IP to vless.TunIP, serve SOCKS5, accept a utun fd from
// loft-priv if asked, then print byte rates every second until the context
// ends or the client dies.
func runVLESS(ctx context.Context, rawURL, uuid, socks, tunSock string, probe, insecure bool, sni string) {
	status("connecting", "vless", "", false)
	live := atomic.Bool{}
	cli, err := vless.New(vless.Config{
		URL: rawURL, UUID: uuid, SNI: sni, Insecure: insecure,
		Log: func(s string) {
			if live.Load() {
				return
			}
			status("connecting", s, "", false)
		},
	})
	if err != nil {
		status("error", err.Error(), "", false)
		os.Exit(2)
	}
	defer cli.Close()
	if err := cli.Probe(ctx); err != nil {
		status("error", err.Error(), "", false)
		os.Exit(2)
	}
	live.Store(true)
	ip := vless.TunIP
	transport.RememberOverlay(ip, ip)
	if probe {
		fmt.Fprintf(os.Stderr, "vless: up ip=%s\n", ip)
		status("on", "", ip, false)
		return
	}
	defer serveSOCKS(ctx, socks, cli).Close()
	tunLive := atomic.Bool{}
	if tunSock != "" {
		ln, err := inbound.ListenTUNSock(tunSock)
		if err != nil {
			fmt.Fprintf(os.Stderr, "tun sock: %v\n", err)
		} else {
			defer ln.Close()
			go func() {
				fd, name, err := inbound.AcceptTUNFD(ln)
				if err != nil {
					fmt.Fprintf(os.Stderr, "tun fd: %v\n", err)
					return
				}
				c, err := inbound.StartTUNFromFD(fd, name, cli)
				if err != nil {
					fmt.Fprintf(os.Stderr, "tun: %v\n", err)
					return
				}
				defer c.Close()
				tunLive.Store(true)
				fmt.Fprintf(os.Stderr, "tun up %s\n", name)
				status("on", "", ip, true)
				<-ctx.Done()
			}()
		}
	}
	on := func() { statusN("on", "", ip, tunLive.Load(), 0, 0) }
	on()
	t := time.NewTicker(time.Second)
	defer t.Stop()
	var prevUp, prevDown uint64
	primed := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-cli.Done():
			return
		case <-t.C:
			up, down := cli.Bytes()
			ru, rd := byteRates(up, down, &prevUp, &prevDown, &primed)
			statusN("on", "", ip, tunLive.Load(), ru, rd)
		}
	}
}

// runTrojan is Trojan TCP+TLS / REALITY / XHTTP: SOCKS5 and optional utun via the userspace stack.
//
// The steps are the same as runVLESS, with a Trojan client.
func runTrojan(ctx context.Context, rawURL, password, socks, tunSock string, probe, insecure bool, sni string) {
	status("connecting", "trojan", "", false)
	live := atomic.Bool{}
	cli, err := trojan.New(trojan.Config{
		URL: rawURL, Password: password, SNI: sni, Insecure: insecure,
		Log: func(s string) {
			if live.Load() {
				return
			}
			status("connecting", s, "", false)
		},
	})
	if err != nil {
		status("error", err.Error(), "", false)
		os.Exit(2)
	}
	defer cli.Close()
	if err := cli.Probe(ctx); err != nil {
		status("error", err.Error(), "", false)
		os.Exit(2)
	}
	live.Store(true)
	ip := trojan.TunIP
	transport.RememberOverlay(ip, ip)
	if probe {
		fmt.Fprintf(os.Stderr, "trojan: up ip=%s\n", ip)
		status("on", "", ip, false)
		return
	}
	defer serveSOCKS(ctx, socks, cli).Close()
	tunLive := atomic.Bool{}
	if tunSock != "" {
		ln, err := inbound.ListenTUNSock(tunSock)
		if err != nil {
			fmt.Fprintf(os.Stderr, "tun sock: %v\n", err)
		} else {
			defer ln.Close()
			go func() {
				fd, name, err := inbound.AcceptTUNFD(ln)
				if err != nil {
					fmt.Fprintf(os.Stderr, "tun fd: %v\n", err)
					return
				}
				c, err := inbound.StartTUNFromFD(fd, name, cli)
				if err != nil {
					fmt.Fprintf(os.Stderr, "tun: %v\n", err)
					return
				}
				defer c.Close()
				tunLive.Store(true)
				fmt.Fprintf(os.Stderr, "tun up %s\n", name)
				status("on", "", ip, true)
				<-ctx.Done()
			}()
		}
	}
	on := func() { statusN("on", "", ip, tunLive.Load(), 0, 0) }
	on()
	t := time.NewTicker(time.Second)
	defer t.Stop()
	var prevUp, prevDown uint64
	primed := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-cli.Done():
			return
		case <-t.C:
			up, down := cli.Bytes()
			ru, rd := byteRates(up, down, &prevUp, &prevDown, &primed)
			statusN("on", "", ip, tunLive.Load(), ru, rd)
		}
	}
}

// runSSTP is the SSTP VPN path: TLS + PPP, then packet TUN and optional SOCKS.
//
// Steps: dial SSTP (TLS, then PPP login), remember the PPP peer as the
// gateway, wrap the session in SSTPNet (a userspace TCP/IP stack so SOCKS
// connections leave through the PPP link, not the host network), apply the
// -dns choice, serve SOCKS5, accept a utun fd from loft-priv if asked, then
// print byte rates every second until the context ends or the session dies.
func runSSTP(ctx context.Context, rawURL, user, pass, socks, tunSock string, probe bool, insecure, pap bool, sni string, verbose bool, dnsList string, dnsReplace bool) {
	status("connecting", "sstp", "", false)
	live := atomic.Bool{}
	sess, err := sstp.Dial(ctx, sstp.Config{
		URL: rawURL, User: user, Password: pass, Insecure: insecure, PAPCHAP: pap, SNI: sni,
		Log: func(s string) {
			if live.Load() || strings.HasPrefix(s, "up ") {
				fmt.Fprintf(os.Stderr, "sstp: %s\n", s)
				return
			}
			status("connecting", s, "", false)
		},
	})
	if err != nil {
		status("error", err.Error(), "", false)
		os.Exit(2)
	}
	defer sess.Close()
	live.Store(true)
	ip := sess.IP.String()
	mtu := sess.MTU()
	if p := sess.Peer.To4(); p != nil {
		transport.RememberGateway(p.String())
	}
	if probe {
		fmt.Fprintf(os.Stderr, "sstp: up ip=%s peer=%s dns=%s mppe=%v mtu=%d\n", ip, sess.Peer, inbound.JoinDNS(sess.DNS), sess.MPPE, mtu)
		statusMTU("on", "", ip, false, inbound.JoinDNS(sess.DNS), mtu, 0, 0)
		return
	}
	sn, err := inbound.NewSSTPNet(sess)
	if err != nil {
		status("error", err.Error(), "", false)
		os.Exit(2)
	}
	defer sn.Close()
	sn.ApplyDNS(inbound.ParseDNSList(dnsList), dnsReplace)
	dns := inbound.JoinDNS(sn.DNS())
	fmt.Fprintf(os.Stderr, "sstp: up ip=%s peer=%s dns=%s mppe=%v mtu=%d\n", ip, sess.Peer, dns, sess.MPPE, mtu)
	on := func(tun bool, up, down uint64) { statusMTU("on", "", ip, tun, dns, sess.MTU(), up, down) }
	if verbose {
		sn.SetLog(func(f string, a ...any) {
			fmt.Fprintf(os.Stderr, "sstp: "+f+"\n", a...)
		})
	}
	defer serveSOCKS(ctx, socks, sn).Close()
	tunLive := atomic.Bool{}
	if tunSock != "" {
		ln, err := inbound.ListenTUNSock(tunSock)
		if err != nil {
			fmt.Fprintf(os.Stderr, "tun sock: %v\n", err)
		} else {
			defer ln.Close()
			go func() {
				fd, name, err := inbound.AcceptTUNFD(ln)
				if err != nil {
					fmt.Fprintf(os.Stderr, "tun fd: %v\n", err)
					return
				}
				dev := inbound.PacketTUNFromFD(fd, name)
				tunLive.Store(true)
				on(true, 0, 0)
				sn.AttachTUN(dev)
			}()
		}
	}
	on(tunLive.Load(), 0, 0)
	t := time.NewTicker(time.Second)
	defer t.Stop()
	var prevUp, prevDown uint64
	primed := false
	for {
		select {
		case <-ctx.Done():
			return
		case <-sn.Done():
			return
		case <-t.C:
			up, down := sn.Bytes()
			ru, rd := byteRates(up, down, &prevUp, &prevDown, &primed)
			on(tunLive.Load(), ru, rd)
		}
	}
}

// spark emits STATUS lines once a second with byte-rate deltas for the GUI.
func spark(ctx context.Context, h *transport.Holder, tun *atomic.Bool) {
	t := time.NewTicker(time.Second)
	defer t.Stop()
	var prevUp, prevDown uint64
	var primed bool
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			up, down := h.Bytes()
			ru, rd := byteRates(up, down, &prevUp, &prevDown, &primed)
			statusN("on", "", transport.OverlayIP(), tun.Load(), ru, rd)
		}
	}
}

// byteRates is the per-second delta for STATUS up/down (popover spark).
func byteRates(up, down uint64, prevUp, prevDown *uint64, primed *bool) (ru, rd uint64) {
	if *primed {
		if up >= *prevUp {
			ru = up - *prevUp
		}
		if down >= *prevDown {
			rd = down - *prevDown
		}
	}
	*primed = true
	*prevUp, *prevDown = up, down
	return
}

// status writes a STATUS line with zero byte rates.
func status(state, msg, ip string, tun bool) {
	statusN(state, msg, ip, tun, 0, 0)
}

// statusN writes one STATUS line. msg is last so the GUI can keep spaces.
func statusN(state, msg, ip string, tun bool, up, down uint64) {
	statusDNS(state, msg, ip, tun, strings.Join(transport.OverlayDNS(), ","), up, down)
}

// statusDNS writes a STATUS line with an explicit resolver list (SSTP).
func statusDNS(state, msg, ip string, tun bool, dns string, up, down uint64) {
	statusMTU(state, msg, ip, tun, dns, 0, up, down)
}

// statusMTU writes one STATUS line on stdout for the GUI as key=value pairs:
// state, overlay IP, overlay gateway, whether the TUN is up, DNS servers,
// MTU (SSTP only, when known), byte rates while on, overlay routes, the live
// front and the fronts learned from the server. The other status* helpers
// all end here.
func statusMTU(state, msg, ip string, tun bool, dns string, mtu int, up, down uint64) {
	tunS := "0"
	if tun {
		tunS = "1"
	}
	var line string
	// msg is always last so the GUI can keep spaces in the error text.
	gw := transport.OverlayGateway()
	rts := strings.Join(transport.OverlayRoutes(), ",")
	routeKV := ""
	if rts != "" {
		routeKV = " routes=" + rts
	}
	statusFrontMu.Lock()
	front := statusFront
	statusFrontMu.Unlock()
	if fan := transport.LiveBulk(); fan != "" {
		front = fan
	}
	if front != "" {
		routeKV += " front=" + front
	}
	if fronts := transport.FormatFronts(transport.LearnedFronts()); fronts != "" {
		routeKV += " fronts=" + fronts
	}
	if state == "on" {
		if mtu > 0 {
			line = fmt.Sprintf("STATUS state=%s ip=%s gw=%s tun=%s dns=%s mtu=%d up=%d down=%d%s msg=%s\n", state, ip, gw, tunS, dns, mtu, up, down, routeKV, sanitize(msg))
		} else {
			line = fmt.Sprintf("STATUS state=%s ip=%s gw=%s tun=%s dns=%s up=%d down=%d%s msg=%s\n", state, ip, gw, tunS, dns, up, down, routeKV, sanitize(msg))
		}
	} else {
		line = fmt.Sprintf("STATUS state=%s ip=%s gw=%s tun=%s dns=%s%s msg=%s\n", state, ip, gw, tunS, dns, routeKV, sanitize(msg))
	}
	_, _ = os.Stdout.WriteString(line)
	_ = os.Stdout.Sync()
}

// sanitize strips CR/LF from STATUS msg so the line stays one record.
func sanitize(s string) string {
	s = strings.ReplaceAll(s, "\n", " ")
	s = strings.ReplaceAll(s, "\r", " ")
	return s
}

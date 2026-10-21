// Command loft-front is the GUI helper: GET/WS/H2/QUIC/H3 front, SOCKS5, and
// in-process or loft-priv TUN. It prints STATUS lines on stdout.
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
)

// frontDecoy and frontPBK are -decoy / -pbk for direct loft fronts.
var frontDecoy, frontPBK string

// frontH2MaxAgeMS is -h2-max-age in milliseconds (0 = keep HTTP/2 until error).
var frontH2MaxAgeMS int

// main connects a loft front and serves SOCKS5 and optional TUN for the GUI.
func main() {
	token := flag.String("token", "", "member key")
	rawURL := flag.String("url", "", "front URL")
	kind := flag.String("type", "auto", "get | ws | h2 | quic | h3 | sstp | vless | trojan | auto")
	path := flag.String("path", "", "GET path; default /static/js for get")
	user := flag.String("user", "", "SSTP username")
	insecure := flag.Bool("insecure", false, "SSTP: skip TLS verify")
	pap := flag.Bool("pap", false, "SSTP: allow PAP and CHAP-MD5")
	sni := flag.String("sni", "", "SSTP/VLESS/Trojan TLS ServerName")
	socks := flag.String("socks", config.DebugSOCKS5, "SOCKS5 listen; empty/off to disable")
	cdnName := flag.String("cdn", "", "edge profile: yandex | cloudflare")
	decoy := flag.String("decoy", "", "direct-path decoy SNI (dest hostname)")
	pbk := flag.String("pbk", "", "node X25519 public, hex or base64url")
	tun := flag.String("tun", "", "TUN device; empty = off, auto = create (needs root)")
	tunNoexec := flag.Bool("tun-noexec", false, "attach named TUN, skip addr/routes/DNS")
	tunSock := flag.String("tun-sock", "", "unix socket; loft-priv sends utun fd")
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
	if err := protect.Bind(*ifName); err != nil {
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
		drv.Event("START", "if="+protect.Bound(), "url="+*rawURL, "type="+*kind)
	}
	frontDecoy = strings.TrimSpace(*decoy)
	frontPBK = strings.TrimSpace(*pbk)
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

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

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

	status("connecting", "front", "", false)

	if *probe {
		d, front, err := dial(ctx, *token, *rawURL, *kind, *path, *muxOn, *cdnName)
		if err != nil {
			status("error", err.Error(), "", false)
			os.Exit(2)
		}
		ip := transport.OverlayIP()
		status("on", "", ip, false)
		_ = d.Close()
		fmt.Fprintf(os.Stderr, "ok overlay=%s front=%s dns=%s\n", ip, front, strings.Join(transport.OverlayDNS(), ","))
		return
	}

	h := &transport.Holder{}
	defer h.Close()
	defer serveSOCKS(ctx, *socks, h).Close()

	var (
		tunMu   sync.Mutex
		tunDev  *inbound.Device
		tunLive atomic.Bool
		resumes atomic.Uint64
	)
	exclude := outboundHosts(*rawURL)
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

	cfg := frontConfig(*token, *rawURL, *kind, *path, *muxOn, *cdnName)
	first := make(chan struct{})
	var once sync.Once
	pathCh := transport.WatchPath(ctx)
	if drv != nil {
		pathCh = transport.MergePath(ctx, pathCh, transport.WatchStall(ctx, h, drv))
	}
	go func() {
		_ = transport.SuperviseOn(ctx, cfg, pathCh, func(d transport.Dialer, o config.Outbound) {
			h.Set(d)
			ip := transport.OverlayIP()
			bringTUN()
			n := resumes.Add(1)
			if n == 1 {
				fmt.Fprintf(os.Stderr, "front %s overlay=%s\n", o.Type, ip)
			} else {
				fmt.Fprintf(os.Stderr, "resume %s overlay=%s n=%d\n", o.Type, ip, n)
			}
			if drv != nil {
				drv.Event("LIVE", fmt.Sprintf("n=%d", n), "type="+o.Type, "overlay="+ip)
			}
			status("on", "", ip, tunLive.Load())
			once.Do(func() { close(first) })
		})
	}()
	select {
	case <-first:
	case <-ctx.Done():
		return
	}
	go spark(ctx, h, &tunLive)
	<-ctx.Done()
}

// frontOrder is the try-list for -type. Probe may fall back get↔ws; h2/quic/h3 stay.
func frontOrder(kind, raw string, muxOn, fallback bool) []string {
	kind = strings.ToLower(strings.TrimSpace(kind))
	if kind == "auto" {
		kind = ""
	}
	if !muxOn && (kind == "" || kind == "get" || kind == "ws") {
		return []string{"get"}
	}
	switch kind {
	case "get":
		if fallback {
			return []string{"get", "ws"}
		}
		return []string{"get"}
	case "ws":
		if fallback {
			return []string{"ws", "get"}
		}
		return []string{"ws"}
	case "h2", "quic", "h3":
		return []string{kind}
	case "":
		if strings.HasPrefix(raw, "ws://") || strings.HasPrefix(raw, "wss://") {
			return []string{"ws", "get"}
		}
		return []string{"get", "ws"}
	default:
		return []string{kind}
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

// outboundFor builds an Outbound for a get/ws/h2/quic/h3 front URL.
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
		Inflight:     48,
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
			o.Path = "/static/js"
		}
	case "ws":
		o.URL = withWSPath(wsURL(raw), "/api/v1/sync")
	case "h2", "h3":
		o.URL = httpOrigin(raw)
		if o.Path == "" {
			o.Path = "/api/v1/tunnel"
		}
	case "quic":
		o.URL = httpOrigin(raw)
	}
	return o
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

// Command loft is the CLI client: config-driven GET/WS/QUIC outbound,
// SOCKS5, and optional TUN with kill-switch.
package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net"
	"net/url"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"sync"
	"syscall"

	"github.com/a3app/loft/clients/go/inbound"
	"github.com/a3app/loft/internal/config"
	"github.com/a3app/loft/internal/protect"
	"github.com/a3app/loft/internal/transport"
	"github.com/a3app/loft/internal/version"
)

// main loads the client profile, supervises the outbound, and serves SOCKS5/TUN.
func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("loft: ")
	version.ExitIfAsked("loft", os.Args[1:])
	fs := flag.NewFlagSet("loft", flag.ExitOnError)
	cfgPath := fs.String("c", "clients/go/configs/client.yaml", "config file")
	ifName := fs.String("if", "", "outbound interface or local IP")
	driveOn := fs.Bool("drive", false, "drive-debug log (handoff, stall, handshake)")
	traceFile := fs.String("trace", "", "drive log file; implies -drive")
	showVer := fs.Bool("version", false, "print program and protocol versions")
	if err := fs.Parse(os.Args[1:]); err != nil {
		os.Exit(2)
	}
	if err := protect.Bind(*ifName); err != nil {
		log.Fatalf("-if: %v", err)
	}
	if s := protect.Bound(); s != "" {
		log.Printf("if %s", s)
	}
	if *traceFile != "" {
		*driveOn = true
	}
	if *showVer {
		version.Print("loft")
		return
	}
	cfg, err := config.LoadClient(*cfgPath)
	if err != nil {
		log.Fatal(err)
	}
	base := strings.TrimSuffix(*cfgPath, filepath.Ext(*cfgPath))
	transport.SetInstanceFile(base + ".instance")
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	h := &transport.Holder{}
	var drv *transport.Drive
	if *driveOn {
		logPath := *traceFile
		if logPath == "" {
			logPath = transport.DefaultDriveLog()
		}
		var err error
		drv, err = transport.OpenDriveLog(logPath)
		if err != nil {
			log.Fatalf("-trace: %v", err)
		}
		defer func() {
			drv.Event("STOP")
			_ = drv.Close()
		}()
		transport.SetDrive(drv)
		if len(cfg.Outbounds) > 0 {
			drv.SetFront(cfg.Outbounds[0].URL)
		}
		log.Printf("drive log %s", logPath)
		drv.Event("START", "if="+protect.Bound(), "cfg="+*cfgPath)
	}
	exclude := outboundHosts(cfg)
	var (
		tunMu  sync.Mutex
		tunDev *inbound.Device
	)
	defer func() { _ = tunDev.Close() }()
	bringTUN := func() {
		if cfg.Listen.TUN == "" {
			return
		}
		ip := transport.OverlayIP()
		if ip == "" {
			return
		}
		cidr := inbound.OverlayCIDR(ip, cfg.Listen.TunAddr)
		tunMu.Lock()
		defer tunMu.Unlock()
		if tunDev != nil {
			tunDev.Attach(h)
			return
		}
		c, err := inbound.StartTUN(cfg.Listen.TUN, cidr, cfg.Listen.KillSwitch, h, exclude)
		if err != nil {
			log.Printf("tun: %v", err)
			return
		}
		tunDev = c
		log.Printf("tun up overlay=%s kill_switch=%v", cidr, cfg.Listen.KillSwitch)
	}
	pathCh := transport.WatchPath(ctx)
	if drv != nil {
		pathCh = transport.MergePath(ctx, pathCh, transport.WatchStall(ctx, h, drv))
	}
	go func() {
		n := 0
		_ = transport.SuperviseOn(ctx, cfg, pathCh, func(d transport.Dialer, o config.Outbound) {
			h.Set(d)
			n++
			log.Printf("%s outbound %s (%s) up overlay=%s", version.Line("loft"), o.Name, o.Type, transport.OverlayIP())
			if drv != nil {
				drv.Event("LIVE", fmt.Sprintf("n=%d", n), "type="+o.Type, "name="+o.Name, "overlay="+transport.OverlayIP())
			}
			bringTUN()
		})
	}()

	ln, err := inbound.ListenSOCKS5(cfg.Listen.SOCKS5, h)
	if err != nil {
		log.Fatal(err)
	}
	defer ln.Close()
	log.Printf("socks5 %s", cfg.Listen.SOCKS5)

	<-ctx.Done()
}

// outboundHosts resolves front hostnames to IPs excluded from TUN split routes.
func outboundHosts(cfg *config.ClientConfig) []string {
	var out []string
	for _, o := range cfg.Outbounds {
		u, err := url.Parse(o.URL)
		if err != nil {
			continue
		}
		host := u.Hostname()
		if ip := net.ParseIP(host); ip != nil {
			out = append(out, host)
			continue
		}
		ips, err := net.LookupIP(host)
		if err != nil {
			continue
		}
		for _, ip := range ips {
			out = append(out, ip.String())
		}
	}
	return out
}

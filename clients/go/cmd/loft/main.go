// Command loft is the CLI client: config-driven GET/WS/QUIC outbound,
// SOCKS5, and optional TUN with kill-switch.
package main

import (
	"context"
	"flag"
	"fmt"
	"io"
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
	"github.com/a3app/loft/clients/go/lanui"
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
	if *showVer {
		version.Print("loft")
		return
	}
	cfg, err := config.LoadClient(*cfgPath)
	if err != nil {
		log.Fatal(err)
	}
	if *ifName == "" {
		*ifName = cfg.Listen.Bind
	}
	if err := protect.BindUplink(*ifName); err != nil {
		log.Fatalf("-if: %v", err)
	}
	if s := protect.Bound(); s != "" {
		log.Printf("if %s", s)
	}
	if *traceFile != "" {
		*driveOn = true
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
	pathCh := transport.MergePath(ctx, transport.WatchPath(ctx), transport.WatchStall(ctx, h, drv))

	sess := &session{parent: ctx, pathCh: pathCh, h: h, bringTUN: bringTUN, drv: drv}
	sess.start(cfg)

	ln, err := inbound.ListenSOCKS5(cfg.Listen.SOCKS5, h)
	if err != nil {
		log.Fatal(err)
	}
	defer ln.Close()
	log.Printf("socks5 %s", cfg.Listen.SOCKS5)

	if cfg.Listen.HTTP != "" {
		ring := lanui.NewRing(80)
		log.SetOutput(io.MultiWriter(os.Stderr, ring))
		ui := &lanui.Server{
			Addr: cfg.Listen.HTTP,
			Pass: cfg.Listen.HTTPPass,
			Log:  ring,
			Live: func() lanui.Status { return liveStatus(*cfgPath, sess, &tunMu, &tunDev) },
			Apply: func(p lanui.Patch) error {
				return applyPatch(*cfgPath, p, sess)
			},
		}
		go func() {
			log.Printf("http %s", cfg.Listen.HTTP)
			if err := ui.Listen(); err != nil {
				log.Printf("http: %v", err)
			}
		}()
	}

	<-ctx.Done()
}

type session struct {
	mu       sync.Mutex
	cancel   context.CancelFunc
	parent   context.Context
	pathCh   <-chan struct{}
	h        *transport.Holder
	bringTUN func()
	drv      *transport.Drive
	front    string
	name     string
}

func (s *session) start(cfg *config.ClientConfig) {
	s.mu.Lock()
	if s.cancel != nil {
		s.cancel()
	}
	ctx, cancel := context.WithCancel(s.parent)
	s.cancel = cancel
	s.mu.Unlock()
	go func() {
		err := transport.SuperviseOn(ctx, cfg, s.pathCh, func(d transport.Dialer, o config.Outbound) {
			if live, ok := s.h.Live().(*inbound.L3Net); ok {
				if p, ok := d.(transport.IPPump); ok {
					live.Swap(d, p)
				} else {
					s.h.Set(d)
				}
			} else {
				if w, err := inbound.WrapL3(d); err != nil {
					log.Printf("l3 net: %v", err)
				} else {
					d = w
				}
				s.h.Set(d)
			}
			s.mu.Lock()
			s.front, s.name = o.Type, o.Name
			s.mu.Unlock()
			log.Printf("%s outbound %s (%s) up overlay=%s", version.Line("loft"), o.Name, o.Type, transport.OverlayIP())
			if s.drv != nil {
				s.drv.Event("LIVE", "type="+o.Type, "name="+o.Name, "overlay="+transport.OverlayIP())
			}
			s.bringTUN()
		})
		if err != nil && ctx.Err() == nil {
			log.Printf("front: %v", err)
		}
	}()
}

func liveStatus(path string, sess *session, tunMu *sync.Mutex, tunDev **inbound.Device) lanui.Status {
	st := lanui.Status{
		Version: version.Line("loft"),
		Bind:    protect.Bound(),
		Overlay: transport.OverlayIP(),
	}
	if cfg, err := config.LoadClient(path); err == nil {
		st.SOCKS = cfg.Listen.SOCKS5
		st.HTTP = cfg.Listen.HTTP
		st.Uplink = cfg.Listen.Uplink
		st.TokenSet = cfg.Token != ""
		if len(cfg.Outbounds) > 0 {
			st.URL = cfg.Outbounds[0].URL
			st.Path = cfg.Outbounds[0].Path
			st.Front = cfg.Outbounds[0].Type
			st.Outbound = cfg.Outbounds[0].Name
		}
	}
	sess.mu.Lock()
	if sess.front != "" {
		st.Front = sess.front
	}
	if sess.name != "" {
		st.Outbound = sess.name
	}
	sess.mu.Unlock()
	tunMu.Lock()
	st.TUN = *tunDev != nil
	tunMu.Unlock()
	return st
}

func applyPatch(path string, p lanui.Patch, sess *session) error {
	cfg, err := config.LoadClient(path)
	if err != nil {
		return err
	}
	if t := strings.TrimSpace(p.Token); t != "" {
		cfg.Token = t
	}
	if len(cfg.Outbounds) == 0 {
		return fmt.Errorf("no outbound")
	}
	o := &cfg.Outbounds[0]
	if p.URL != "" {
		o.URL = p.URL
	}
	if p.Type != "" {
		o.Type = p.Type
	}
	if p.Path != "" {
		o.Path = p.Path
	}
	if err := config.ValidIFName(p.Uplink); err != nil {
		return err
	}
	cfg.Listen.Uplink = p.Uplink
	if err := config.WriteClient(path, cfg); err != nil {
		return err
	}
	rsc := filepath.Join(filepath.Dir(path), "uplink.rsc")
	if err := lanui.WriteUplinkRSC(rsc, p.Uplink, "172.31.20.0/24"); err != nil {
		return err
	}
	sess.start(cfg)
	log.Printf("saved uplink=%s", p.Uplink)
	return nil
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

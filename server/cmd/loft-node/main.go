// Command loft-node is the overlay node daemon: controller or relay.
// Config is YAML or switch-style; overlay state lives in -dir.
package main

import (
	"crypto/ed25519"
	"encoding/hex"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/a3app/loft/internal/config"
	ecrypto "github.com/a3app/loft/internal/crypto"
	"github.com/a3app/loft/internal/identity"
	"github.com/a3app/loft/internal/netconf"
	"github.com/a3app/loft/internal/transport"
	"github.com/a3app/loft/internal/version"
	"github.com/a3app/loft/internal/wire"
	"github.com/a3app/loft/server/origin"
)

// main loads overlay and server config, then serves TCP HTTPS and UDP QUIC/H3.
func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("loft-node: ")
	version.ExitIfAsked("loft-node", os.Args[1:])
	dir := flag.String("dir", "/var/lib/loft", "data directory")
	cfgPath := flag.String("c", "", "server config (role, relays, decoy)")
	listen := flag.String("listen", "", "override listen address")
	cert := flag.String("cert", "", "TLS cert pem")
	key := flag.String("key", "", "TLS key pem")
	showVer := flag.Bool("version", false, "print program and protocol versions")
	printPub := flag.Bool("print-pub", false, "print controller Ed25519 public key (64 hex) and exit")
	tracePath := flag.String("trace", "", "session event log (GET seq); empty = off")
	promoteNow := flag.Bool("promote", false, "standby only, as root: become the controller from the copy of its state (restart loft-node after)")
	envPath := flag.String("env", "/etc/loft/loft.env", "environment file with LOFT_TOKEN (for -promote)")
	rollKey := flag.Int("roll-key", 0, "replace the controller key; pinned clients and relays follow for this many days (restart the node after)")
	flag.Parse()
	if *showVer {
		version.Print("loft-node")
		return
	}
	if *printPub {
		if err := printControllerPub(*dir, *cfgPath); err != nil {
			log.Fatal(err)
		}
		return
	}
	if *promoteNow {
		if err := promote(*dir, *cfgPath, *envPath); err != nil {
			log.Fatal(err)
		}
		log.Printf("promoted: this node is the controller now. Restart loft-node, and do not start the old controller as a controller again.")
		return
	}
	if *rollKey > 0 {
		seed := filepath.Join(*dir, "controller.seed")
		k, err := ecrypto.RollControllerKey(seed, filepath.Join(*dir, "controller.roll"), time.Duration(*rollKey)*24*time.Hour)
		if err != nil {
			log.Fatal(err)
		}
		fmt.Printf("%x\n", k.New)
		log.Printf("controller key replaced; clients and relays pinning %x… follow until %s. Restart loft-node.", k.Old[:4], time.Unix(k.NotAfter, 0).UTC().Format(time.DateOnly))
		return
	}

	// A standby that took over by itself starts from the controller's files.
	promoted := autoPromoted(*dir)
	if promoted {
		st, err := os.Stat(*dir)
		if err != nil {
			log.Fatal(err)
		}
		if err := copyState(*dir, st); err != nil {
			log.Fatal(err)
		}
	}
	n, err := netconf.LoadDir(*dir)
	if err != nil {
		log.Fatal(err)
	}
	if !n.Enabled {
		log.Fatal("network disabled")
	}
	addr := *listen
	if addr == "" && len(n.Listen) > 0 {
		ip := n.Listen[0].IP
		if ip == "" {
			ip = "127.0.0.1"
		}
		addr = fmt.Sprintf("%s:%d", ip, n.Listen[0].Port)
	}
	if addr == "" {
		addr = "127.0.0.1:7643"
	}
	var cfg *config.ServerConfig
	if *cfgPath != "" {
		var err error
		cfg, err = config.LoadServer(*cfgPath)
		if err != nil {
			log.Fatal(err)
		}
	} else {
		cfg = &config.ServerConfig{
			Listen:  addr,
			GetPath: "/static/v3/build",
			WSPath:  "/api/v1/sync",
			Role:    "controller",
		}
	}
	if promoted && cfg.IsReplica() {
		sb := filepath.Join(*dir, "standby")
		ctlConf, err := os.ReadFile(filepath.Join(sb, "node.conf"))
		if err != nil {
			log.Fatal(err)
		}
		merged, err := mergePromoted(cfg, ctlConf)
		if err != nil {
			log.Fatal(err)
		}
		if tok, err := os.ReadFile(filepath.Join(sb, "loft.token")); err == nil {
			merged.Token = strings.TrimSpace(string(tok))
		}
		cfg = merged
		log.Printf("standby %s runs as the controller (took over by itself; loft-node -promote makes it permanent)", filepath.Base(*dir))
	} else if cfg.IsReplica() {
		transport.PromoteHook = func() {
			if _, err := takeOver(*dir, cfg.ID, nil); err != nil {
				log.Printf("loft-standby: %v", err)
				return
			}
			if err := markPromoted(*dir); err != nil {
				log.Printf("loft-standby: %v", err)
				return
			}
			os.Exit(3) // systemd restarts loft-node, which starts as the controller
		}
	}
	if !cfg.IsRelay() {
		if b, err := os.ReadFile(filepath.Join(*dir, fencedMark)); err == nil {
			log.Printf("loft-node: this controller was %s; not starting. Reinstall it as a standby, or remove %s to run it as a controller again.", strings.TrimSpace(string(b)), filepath.Join(*dir, fencedMark))
			return
		}
		transport.FenceFn = func(t wire.Takeover) { fence(*dir, t) }
	}
	if *listen != "" {
		cfg.Listen = *listen
	} else if cfg.Listen == "" {
		cfg.Listen = addr
	}
	if *cert != "" {
		cfg.CertFile = *cert
	}
	if *key != "" {
		cfg.KeyFile = *key
	}
	if cfg.Token == "" {
		cfg.Token = os.Getenv("LOFT_TOKEN")
	}
	if err := mergeLists(*dir, cfg); err != nil {
		log.Fatal(err)
	}
	ring, err := identity.Load(filepath.Join(*dir, "members.keys"))
	if err != nil {
		log.Fatal(err)
	}
	want := map[string]string{}
	for _, m := range n.Members {
		if m.KeySHA256 != "" {
			want[m.ID] = m.KeySHA256
		}
	}
	ring.Filter(want)
	transport.AuthKeys = ring
	if cfg.IsRelay() {
		key, err := os.ReadFile(filepath.Join(*dir, "relay.key"))
		if err != nil {
			log.Fatalf("relay: %v (the key the controller issued for this relay)", err)
		}
		cfg.RelayToken = strings.TrimSpace(string(key))
	} else {
		transport.StateFn = func() map[string][]byte { return controllerState(*dir, *cfgPath, cfg) }
		rk, err := loadRelayKeys(*dir, cfg.Relays)
		if err != nil {
			log.Fatal(err)
		}
		transport.RelayKeys = rk
		log.Printf("relay keys: %d", rk.Len())
	}
	// AuthShared: LOFT_TOKEN as client secret only when the ring is empty.
	// Non-empty members.keys never mixes the node token, even if the flag is on.
	transport.AuthShared = cfg.AuthShared
	innerIDs, err := cfg.InnerIDs()
	if err != nil {
		log.Fatal(err)
	}
	transport.SetInnerAllow(innerIDs)
	ccIDs, err := cfg.CCIDs()
	if err != nil {
		log.Fatal(err)
	}
	transport.SetCCPolicy(ccIDs, cfg.CCMaxUpMbps*1000, cfg.CCMaxDownMbps*1000)
	if len(ccIDs) > 0 {
		names := make([]string, len(ccIDs))
		for i, id := range ccIDs {
			names[i] = wire.CCName(id)
		}
		log.Printf("cc allow: %s", strings.Join(names, " "))
		if err := transport.TCPBBRReady(); err != nil {
			log.Printf("cc: TCP fronts stay on the kernel default, bbr unavailable (%v): load tcp_bbr and add bbr to net.ipv4.tcp_allowed_congestion_control", err)
		}
	}
	if len(innerIDs) > 0 {
		names := make([]string, len(innerIDs))
		for i, id := range innerIDs {
			names[i] = wire.InnerName(id)
		}
		log.Printf("inner allow: chacha %s", strings.Join(names, " "))
	}
	if cfg.Token == "" && ring.Len() == 0 {
		log.Fatal("LOFT_TOKEN or member keys required")
	}
	if ring.Len() > 0 {
		log.Printf("member keys: %d", ring.Len())
		if cfg.AuthShared {
			log.Printf("auth shared ignored: members.keys in use")
		}
	} else if cfg.AuthShared {
		log.Printf("auth shared: LOFT_TOKEN is the client secret")
	} else {
		log.Printf("member keys: 0; client auth needs members.keys (auth shared is off)")
	}
	if cfg.ControllerKey == "" {
		cfg.ControllerKey = filepath.Join(*dir, "controller.seed")
	}
	if *tracePath != "" {
		cfg.Trace = *tracePath
	}
	if cfg.Trace != "" {
		tr, err := transport.OpenSessTrace(cfg.Trace)
		if err != nil {
			log.Fatal(err)
		}
		if tr != nil {
			transport.SetSessTrace(tr)
			log.Printf("session trace %s", cfg.Trace)
		}
	}
	h, err := origin.New(cfg, n)
	if err != nil {
		log.Fatal(err)
	}
	log.Printf("%s network %s role=%s peers=%s listen %s", version.Line("loft-node"), n.ID, cfg.Role, n.Peers, cfg.Listen)
	if err := loadEgress(*dir, cfg); err != nil {
		log.Printf("%v: members leave directly until it is fixed", err)
	}
	go writeEgressStatus(*dir)
	go reloadOnHUP(*dir, *cfgPath, cfg)
	go writeNodeStatus(*dir)
	transport.EnableHTTP2ExtendedConnect()
	if err := origin.ListenAll(cfg, h); err != nil {
		log.Fatal(err)
	}
}

// printControllerPub writes the controller Ed25519 public key as 64 hex
// digits. Before the first start it makes the random seed, so the key it
// prints is the one the node will use.
func printControllerPub(dir, cfgPath string) error {
	seed := filepath.Join(dir, "controller.seed")
	if cfgPath != "" {
		cfg, err := config.LoadServer(cfgPath)
		if err != nil {
			return err
		}
		if cfg.ControllerKey != "" {
			seed = cfg.ControllerKey
		}
	}
	priv, err := ecrypto.LoadControllerKey(seed)
	if err != nil {
		return err
	}
	pub, ok := priv.Public().(ed25519.PublicKey)
	if !ok {
		return fmt.Errorf("controller key: not ed25519")
	}
	fmt.Println(hex.EncodeToString(pub))
	return nil
}

// loadRelayKeys reads {dir}/relays.keys (relay id → key) and keeps only keys
// whose SHA-256 matches the relay's key_sha256 in node.conf, the same way
// members.keys is checked against network.conf. A missing file is empty.
func loadRelayKeys(dir string, rels []config.RelayEntry) (*identity.Ring, error) {
	ring, err := identity.Load(filepath.Join(dir, "relays.keys"))
	if err != nil {
		return nil, err
	}
	want := map[string]string{}
	for _, r := range rels {
		if r.KeySHA256 != "" {
			want[r.ID] = r.KeySHA256
		}
	}
	ring.Filter(want)
	return ring, nil
}

// reloadOnHUP re-reads network.conf and members.keys on SIGHUP
// (systemctl reload loft-node). A file that does not load keeps the running
// config. Members whose entry did not change stay connected.
func reloadOnHUP(dir, cfgPath string, cfg *config.ServerConfig) {
	ch := make(chan os.Signal, 1)
	signal.Notify(ch, syscall.SIGHUP)
	for range ch {
		if err := reloadFleet(dir, cfgPath, cfg); err != nil {
			log.Printf("reload: %v (running relays and lists kept)", err)
		}
		if err := reload(dir, cfg); err != nil {
			log.Printf("reload: %v (running config kept)", err)
		}
	}
}

func reload(dir string, cfg *config.ServerConfig) error {
	n, err := netconf.LoadDir(dir)
	if err != nil {
		return err
	}
	ring, err := identity.Load(filepath.Join(dir, "members.keys"))
	if err != nil {
		return err
	}
	want := map[string]string{}
	for _, m := range n.Members {
		if m.KeySHA256 != "" {
			want[m.ID] = m.KeySHA256
		}
	}
	ring.Filter(want)
	if cfg.Token == "" && ring.Len() == 0 {
		return fmt.Errorf("no member keys and no LOFT_TOKEN")
	}
	if transport.AuthKeys == nil {
		transport.AuthKeys = identity.NewRing()
	}
	transport.AuthKeys.Replace(ring)
	closed := 0
	if o := transport.NetSwitch(); o != nil {
		closed = o.Reload(n)
	}
	if err := loadEgress(dir, cfg); err != nil {
		log.Printf("reload: %v (previous exit rules kept)", err)
	}
	log.Printf("reload: network %s, member keys %d, sessions closed %d", n.ID, ring.Len(), closed)
	return nil
}

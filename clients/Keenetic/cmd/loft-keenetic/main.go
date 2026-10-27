// Command loft-keenetic is the Entware client for KeeneticOS.
//
// With no -session it serves the LAN page and starts one child per enabled
// connection. A child is this binary again: loft-keenetic -session <id>.
// The child opens opkgtunN and does not install a default route.
package main

import (
	"context"
	"flag"
	"log"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"

	"github.com/a3app/loft/clients/Keenetic"
	"github.com/a3app/loft/internal/version"
)

func main() {
	log.SetFlags(log.LstdFlags | log.Lmsgprefix)
	log.SetPrefix("loft: ")
	version.ExitIfAsked("loft-keenetic", os.Args[1:])
	fs := flag.NewFlagSet("loft-keenetic", flag.ExitOnError)
	cfgPath := fs.String("c", "/opt/etc/loft/client.yaml", "config file")
	session := fs.String("session", "", "run one connection and exit with it")
	state := fs.String("state", "", "directory for per-connection status JSON")
	showVer := fs.Bool("version", false, "print program and protocol versions")
	if err := fs.Parse(os.Args[1:]); err != nil {
		os.Exit(2)
	}
	if *showVer {
		version.Print("loft-keenetic")
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if *session != "" {
		err := keenetic.RunSession(ctx, *cfgPath, *session, *state)
		if err != nil && ctx.Err() == nil {
			log.Fatal(err)
		}
		return
	}
	if err := ensureConfig(*cfgPath); err != nil {
		log.Fatal(err)
	}
	st := keenetic.OpenStore(*cfgPath)
	sup := &keenetic.Sup{Store: st, State: *state, Ring: keenetic.NewRing(80)}
	panel := &keenetic.Panel{Store: st, Sup: sup}
	go sup.Loop(ctx)
	if err := panel.Serve(ctx); err != nil && ctx.Err() == nil {
		log.Fatal(err)
	}
	sup.StopAll()
}

// sampleYAML is also pack/client.yaml.sample. check_updates is explicit:
// a missing bool would stay false.
const sampleYAML = `listen:
  http: ":8090"
keenetic:
  auth: http://127.0.0.1/auth
  rci: http://127.0.0.1:79/rci/show/ip/route
  check_updates: true
connections: []
`

func ensureConfig(path string) error {
	if _, err := os.Stat(path); err == nil {
		return nil
	} else if !os.IsNotExist(err) {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, []byte(sampleYAML), 0o600)
}

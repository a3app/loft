package main

import (
	"encoding/json"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/a3app/loft/internal/config"
	"github.com/a3app/loft/internal/transport"
	"github.com/a3app/loft/server/origin"
)

// mergeLists adds lists.conf (written by loft-panel) to the lists of
// node.conf; a name in lists.conf replaces the same name.
func mergeLists(dir string, cfg *config.ServerConfig) error {
	m, err := config.LoadLists(filepath.Join(dir, "lists.conf"))
	if err != nil {
		return err
	}
	if len(m) == 0 {
		return nil
	}
	if cfg.Lists == nil {
		cfg.Lists = map[string][]string{}
	}
	for k, v := range m {
		cfg.Lists[k] = v
	}
	return nil
}

// reloadFleet re-reads the relay allowlist from node.conf and lists.conf,
// re-signs both. The rest of node.conf still needs a restart.
func reloadFleet(dir, cfgPath string, cfg *config.ServerConfig) error {
	lists := cfg.Lists
	if cfgPath != "" {
		c, err := config.LoadServer(cfgPath)
		if err != nil {
			return err
		}
		if transport.FleetCtl != nil {
			transport.FleetCtl.SetRelays(c.Relays, c.Enrol)
		}
		lists = c.Lists
	}
	next := *cfg
	next.Lists = lists
	if err := mergeLists(dir, &next); err != nil {
		return err
	}
	cfg.Lists = next.Lists // the egress rules read these
	origin.ReloadLists(cfg)
	log.Printf("reload: %d lists", len(cfg.Lists))
	return nil
}

// writeNodeStatus keeps dir/node-status.json fresh for the panel: who is
// connected, which relays beat.
func writeNodeStatus(dir string) {
	path := filepath.Join(dir, "node-status.json")
	for range time.Tick(5 * time.Second) {
		st := struct {
			At      time.Time             `json:"at"`
			Members []transport.Peer      `json:"members"`
			Relays  []transport.LiveRelay `json:"relays"`
		}{At: time.Now().UTC(), Members: []transport.Peer{}, Relays: []transport.LiveRelay{}}
		if o := transport.NetSwitch(); o != nil {
			st.Members = o.Peers()
		}
		if transport.FleetCtl != nil {
			st.Relays = transport.FleetCtl.Live()
		}
		b, _ := json.Marshal(st)
		tmp := path + ".tmp"
		if err := os.WriteFile(tmp, b, 0o644); err == nil {
			os.Rename(tmp, path)
		} else {
			fmt.Fprintln(os.Stderr, "node-status:", err)
		}
	}
}

package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"time"

	"github.com/a3app/loft/clients/go/inbound"
	"github.com/a3app/loft/internal/config"
	"github.com/a3app/loft/internal/egress"
	"github.com/a3app/loft/internal/sstp"
	"github.com/a3app/loft/internal/transport"
)

// loadEgress reads egress.conf from dir (written by loft-panel) and swaps
// the exit rules in. No file: everything leaves directly, as before.
func loadEgress(dir string, cfg *config.ServerConfig) error {
	b, err := os.ReadFile(filepath.Join(dir, "egress.conf"))
	if errors.Is(err, os.ErrNotExist) {
		if old := transport.Egress.Swap(nil); old != nil {
			old.Close()
		}
		dumpEgressStatus(dir)
		return nil
	}
	if err != nil {
		return err
	}
	c, err := egress.Parse(string(b))
	if err != nil {
		return fmt.Errorf("egress.conf: %w", err)
	}
	r := egress.New(c, egress.Options{Lists: cfg.Lists, SSTP: dialSSTP, Log: log.Printf, Deny: transport.DeniedIP})
	if old := transport.Egress.Swap(r); old != nil {
		old.Close()
	}
	dumpEgressStatus(dir)
	log.Printf("egress: %d channels, %d rules", len(c.Channels), len(c.Rules))
	return nil
}

// dialSSTP holds an SSTP channel: the node's own client, a userspace IPv4
// stack over the PPP session (nothing changes in the server's routes).
func dialSSTP(ctx context.Context, ch egress.Channel) (egress.SSTPConn, error) {
	sess, err := sstp.Dial(ctx, sstp.Config{URL: "https://" + ch.Addr, User: ch.User, Password: ch.Pass,
		Insecure: ch.Insecure, PAPCHAP: ch.Auth == "pap" || ch.Auth == "auto" || ch.Auth == ""})
	if err != nil {
		return nil, err
	}
	n, err := inbound.NewSSTPNet(sess)
	if err != nil {
		sess.Close()
		return nil, err
	}
	return n, nil
}

func dumpEgressStatus(dir string) {
	path := filepath.Join(dir, "egress-status.json")
	var st []egress.Status
	if r := transport.Egress.Load(); r != nil {
		st = r.Status()
	}
	b, _ := json.Marshal(struct {
		At       time.Time       `json:"at"`
		Channels []egress.Status `json:"channels"`
	}{time.Now().UTC(), st})
	tmp := path + ".tmp"
	if os.WriteFile(tmp, b, 0o644) == nil {
		os.Rename(tmp, path)
	}
}

// writeEgressStatus keeps dir/egress-status.json fresh for the panel.
func writeEgressStatus(dir string) {
	dumpEgressStatus(dir)
	for range time.Tick(5 * time.Second) {
		dumpEgressStatus(dir)
	}
}

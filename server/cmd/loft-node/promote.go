package main

// Standby controller: what the controller sends to a standby, and how a
// standby becomes the controller.
//
// A standby (role replica) runs as a relay and keeps the controller's files
// in {dir}/standby. Promoting it makes those files its own: the members, the
// relay and controller keys and the mask secret are the controller's, so
// clients and relays keep working without new links; the address,
// certificate and decoy stay the standby's own. The old controller, if it
// comes back, must not run as a controller next to the promoted one.

import (
	"crypto/ed25519"
	"errors"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/a3app/loft/internal/config"
	"github.com/a3app/loft/internal/wire"
)

// stateFiles are the files in -dir a standby needs to take over.
// controller.epoch is the controller's Takeover: the standby needs its
// generation to sign the next one, but it is not copied over its own
// (takeOver writes the new one).
var stateFiles = []string{"network.conf", "members.keys", "relays.keys", "controller.seed", "controller.roll", "lists.conf", epochFile}

// epochFile holds the newest Takeover a node knows: on a controller its
// own generation, on a relay the newest it has seen.
const epochFile = "controller.epoch"

// fencedMark is the file a controller writes in -dir when a relay showed
// it a newer Takeover: it was replaced and does not start as a controller.
const fencedMark = "fenced"

// takeOver signs the next controller generation with the controller key
// from {dir}/standby and writes it to {dir}/controller.epoch. The new
// generation is one above the newest this standby knows, from the
// controller's copy or from what it saw as a relay.
func takeOver(dir, by string, like os.FileInfo) (wire.Takeover, error) {
	seed, err := os.ReadFile(filepath.Join(dir, "standby", "controller.seed"))
	if err != nil || len(seed) < ed25519.SeedSize {
		return wire.Takeover{}, errors.New("promote: no controller key in the standby copy")
	}
	var epoch uint64
	for _, p := range []string{filepath.Join(dir, epochFile), filepath.Join(dir, "standby", epochFile)} {
		if b, err := os.ReadFile(p); err == nil {
			if t, err := wire.DecodeTakeover(b); err == nil && t.Epoch > epoch {
				epoch = t.Epoch
			}
		}
	}
	t := wire.Takeover{Epoch: epoch + 1, At: time.Now().Unix(), By: by}
	t.Sign(ed25519.NewKeyFromSeed(seed[:ed25519.SeedSize]))
	return t, writeLike(filepath.Join(dir, epochFile), t.Encode(), 0o600, like)
}

// fence stops this controller for good after a relay showed it t: it
// leaves fencedMark so it does not start as a controller again, and exits
// with 0 so systemd does not restart it.
func fence(dir string, t wire.Takeover) {
	msg := fmt.Sprintf("replaced by standby %s at %s (generation %d)\n", t.By, time.Unix(t.At, 0).UTC().Format(time.RFC3339), t.Epoch)
	if err := os.WriteFile(filepath.Join(dir, fencedMark), []byte(msg), 0o600); err != nil {
		log.Printf("loft-node: %v", err)
	}
	log.Printf("loft-node: this controller was %s; stopping. Reinstall it as a standby, or remove %s to run it as a controller again.", strings.TrimSpace(msg), filepath.Join(dir, fencedMark))
	os.Exit(0)
}

// controllerState is transport.StateFn on a controller: the files above,
// node.conf, and the node token (the mask secret) as "loft.token".
func controllerState(dir, cfgPath string, cfg *config.ServerConfig) map[string][]byte {
	out := map[string][]byte{}
	for _, n := range stateFiles {
		if b, err := os.ReadFile(filepath.Join(dir, n)); err == nil {
			out[n] = b
		}
	}
	if cfgPath != "" {
		if b, err := os.ReadFile(cfgPath); err == nil {
			out["node.conf"] = b
		}
	}
	if cfg.Token != "" {
		out["loft.token"] = []byte(cfg.Token)
	}
	return out
}

// promotedMark is the file a standby writes in -dir when it took over by
// itself: from then on it starts as the controller from its standby copy.
const promotedMark = "promoted"

func autoPromoted(dir string) bool {
	_, err := os.Stat(filepath.Join(dir, promotedMark))
	return err == nil
}

func markPromoted(dir string) error {
	return os.WriteFile(filepath.Join(dir, promotedMark), []byte(time.Now().UTC().Format(time.RFC3339)+"\n"), 0o600)
}

// mergePromoted is the controller's node.conf (ctlConf) with this standby's
// own listen, address, certificate and decoy, as the controller role, and
// without this standby in the relay list.
func mergePromoted(mine *config.ServerConfig, ctlConf []byte) (*config.ServerConfig, error) {
	c, err := config.ParseCommands(string(ctlConf))
	if err != nil {
		return nil, fmt.Errorf("promote: controller node.conf: %w", err)
	}
	c.Role = "controller"
	c.Listen, c.Advertise, c.CertFile, c.KeyFile = mine.Listen, mine.Advertise, mine.CertFile, mine.KeyFile
	c.Decoy, c.Mask, c.DecoyProxy = mine.Decoy, mine.Mask, mine.DecoyProxy
	c.ID, c.Controller, c.ControllerAlt, c.ControllerPub = "", "", nil, ""
	c.PromoteAfter = 0
	c.Relays = slices.DeleteFunc(c.Relays, func(e config.RelayEntry) bool { return e.ID == mine.ID })
	return c, nil
}

// copyState copies the controller's files from {dir}/standby into dir, with
// the owner of like.
func copyState(dir string, like os.FileInfo) error {
	sb := filepath.Join(dir, "standby")
	for _, n := range stateFiles {
		if n == epochFile {
			continue
		}
		b, err := os.ReadFile(filepath.Join(sb, n))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if err := writeLike(filepath.Join(dir, n), b, 0o600, like); err != nil {
			return err
		}
	}
	return nil
}

// promote turns this standby into the controller (run as root, then restart
// loft-node). It copies the controller's files from {dir}/standby into dir,
// writes the controller's node.conf with this host's listen, address,
// certificate and decoy, and puts the controller's token into envPath.
func promote(dir, cfgPath, envPath string) error {
	sb := filepath.Join(dir, "standby")
	if _, err := os.Stat(filepath.Join(sb, "received")); err != nil {
		return errors.New("promote: no controller state in " + sb + " (is this a standby that has reached its controller?)")
	}
	if cfgPath == "" {
		return errors.New("promote: -c node.conf is required")
	}
	mine, err := config.LoadServer(cfgPath)
	if err != nil {
		return err
	}
	if !mine.IsReplica() {
		return errors.New("promote: this node is not a standby (role replica)")
	}
	ctlConf, err := os.ReadFile(filepath.Join(sb, "node.conf"))
	if err != nil {
		return fmt.Errorf("promote: controller node.conf: %w", err)
	}
	c, err := mergePromoted(mine, ctlConf)
	if err != nil {
		return err
	}
	text := "# loft-node config; promoted from standby " + mine.ID + ".\n" + config.RenderCommands(*c)
	if _, err := config.ParseCommands(text); err != nil {
		return fmt.Errorf("promote: %w", err)
	}
	st, err := os.Stat(dir)
	if err != nil {
		return err
	}
	if err := copyState(dir, st); err != nil {
		return err
	}
	if _, err := takeOver(dir, mine.ID, st); err != nil {
		return err
	}
	cst, err := os.Stat(cfgPath)
	if err != nil {
		return err
	}
	if err := writeLike(cfgPath, []byte(text), cst.Mode().Perm(), cst); err != nil {
		return err
	}
	if tok, err := os.ReadFile(filepath.Join(sb, "loft.token")); err == nil && envPath != "" {
		if err := setEnvToken(envPath, strings.TrimSpace(string(tok))); err != nil {
			return err
		}
	}
	os.Remove(filepath.Join(dir, promotedMark))
	return nil
}

// writeLike writes path through a temp file with the owner of like.
func writeLike(path string, b []byte, mode os.FileMode, like os.FileInfo) error {
	tmp := path + ".new"
	if err := os.WriteFile(tmp, b, mode); err != nil {
		return err
	}
	chownLike(tmp, like)
	return os.Rename(tmp, path)
}

// setEnvToken replaces LOFT_TOKEN in an environment file, keeping the rest.
func setEnvToken(path, tok string) error {
	old, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return err
	}
	var lines []string
	set := false
	for _, ln := range strings.Split(strings.TrimRight(string(old), "\n"), "\n") {
		if strings.HasPrefix(strings.TrimSpace(ln), "LOFT_TOKEN=") {
			ln, set = "LOFT_TOKEN="+tok, true
		}
		if ln != "" {
			lines = append(lines, ln)
		}
	}
	if !set {
		lines = append(lines, "LOFT_TOKEN="+tok)
	}
	mode := os.FileMode(0o640)
	st, err := os.Stat(path)
	if err == nil {
		mode = st.Mode().Perm()
	}
	return writeLike(path, []byte(strings.Join(lines, "\n")+"\n"), mode, st)
}

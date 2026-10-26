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
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/a3app/loft/internal/config"
)

// stateFiles are the files in -dir a standby needs to take over.
var stateFiles = []string{"network.conf", "members.keys", "relays.keys", "controller.seed", "controller.roll", "lists.conf"}

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
	c, err := config.ParseCommands(string(ctlConf))
	if err != nil {
		return fmt.Errorf("promote: controller node.conf: %w", err)
	}
	c.Role = "controller"
	c.Listen, c.Advertise, c.CertFile, c.KeyFile = mine.Listen, mine.Advertise, mine.CertFile, mine.KeyFile
	c.Decoy, c.Mask, c.DecoyProxy = mine.Decoy, mine.Mask, mine.DecoyProxy
	c.ID, c.Controller, c.ControllerAlt, c.ControllerPub = "", "", nil, ""
	c.Relays = slices.DeleteFunc(c.Relays, func(e config.RelayEntry) bool { return e.ID == mine.ID })
	text := "# loft-node config; promoted from standby " + mine.ID + ".\n" + config.RenderCommands(*c)
	if _, err := config.ParseCommands(text); err != nil {
		return fmt.Errorf("promote: %w", err)
	}

	st, err := os.Stat(dir)
	if err != nil {
		return err
	}
	for _, n := range stateFiles {
		b, err := os.ReadFile(filepath.Join(sb, n))
		if errors.Is(err, os.ErrNotExist) {
			continue
		}
		if err != nil {
			return err
		}
		if err := writeLike(filepath.Join(dir, n), b, 0o600, st); err != nil {
			return err
		}
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

package adm

// This file connects an existing loft node to the panel's nginx. The node's
// "decoy" is where it forwards TLS traffic that is not loft (a normal
// website), so pointing it at nginx makes the panel reachable on the node's
// own port 443.

import (
	"errors"
	"fmt"
	"os"
	"strings"

	"github.com/a3app/loft/internal/config"
	"github.com/a3app/loft/internal/startup"
	"github.com/a3app/loft/server/panel/store"
)

// WireNode points the node's decoy at the panel's nginx and turns on the
// PROXY header, so nginx (and the panel's login guard) see client addresses.
// It edits switch-style node.conf by appending lines, which keeps comments,
// and checks the result with the node's own parser before replacing the file.
//
// It refuses to touch a node whose first decoy is someone else's site: the
// PROXY header would break that origin. YAML node.conf is left alone too.
func WireNode(path, decoy string) (changed bool, note string, err error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return false, "", err
	}
	body := string(b)
	if startup.LooksYAML(body) {
		return false, fmt.Sprintf("%s is YAML: add `decoy: [%s]` and `decoy_proxy: true` by hand", path, decoy), nil
	}
	c, err := config.ParseCommands(body)
	if err != nil {
		return false, "", fmt.Errorf("%s: %w", path, err)
	}
	// Work out which lines are missing; a foreign decoy stops the edit.
	var add []string
	switch {
	case len(c.Decoy) == 0:
		add = append(add, "decoy "+decoy)
	case c.Decoy[0] != decoy:
		return false, fmt.Sprintf("decoy is %s, not %s: left as is; the panel works, but sees every client as 127.0.0.1", c.Decoy[0], decoy), nil
	}
	if !c.DecoyProxy {
		add = append(add, "decoy_proxy enable")
	}
	if len(add) == 0 {
		return false, "already wired", nil
	}
	if !strings.HasSuffix(body, "\n") {
		body += "\n"
	}
	body += "# loft-panel: non-loft TLS goes to the panel's nginx with the client address\n" + strings.Join(add, "\n") + "\n"
	// Re-parse the edited text so a bad edit never reaches disk.
	n, err := config.ParseCommands(body)
	if err != nil {
		return false, "", fmt.Errorf("edited %s does not parse: %w", path, err)
	}
	if len(n.Decoy) == 0 || n.Decoy[0] != decoy || !n.DecoyProxy {
		return false, "", errors.New("edited node.conf does not have the decoy it should")
	}
	// Keep the old file as .prev, write the new one, keep the owner.
	st, err := os.Stat(path)
	if err != nil {
		return false, "", err
	}
	if err := store.WriteAtomic(path+".prev", b, st.Mode().Perm()); err != nil {
		return false, "", err
	}
	if err := store.WriteAtomic(path, []byte(body), st.Mode().Perm()); err != nil {
		return false, "", err
	}
	if uid, gid, ok := owner(st); ok {
		os.Chown(path, uid, gid)
		os.Chown(path+".prev", uid, gid)
	}
	return true, "added: " + strings.Join(add, "; "), nil
}

// Package adm is what loft-panel-adm does as root: read and replace the
// node's network.conf with rollback, issue member keys, restart loft-node.
// Paths and system calls are fields so tests run without root.
package adm

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/a3app/loft/internal/identity"
	"github.com/a3app/loft/internal/netconf"
	"github.com/a3app/loft/server/panel/admproto"
	"github.com/a3app/loft/server/panel/store"
)

// MaxNetworkConf bounds network.conf; a few thousand members fit easily.
const MaxNetworkConf = 512 << 10

// Ops holds the node layout and how to talk to systemd.
type Ops struct {
	Dir      string // /var/lib/loft
	Unit     string // loft-node.service
	NodeBin  string // /usr/bin/loft-node, for -version
	UID, GID int    // owner of files under Dir (user loft); -1 keeps the writer's
	// Systemctl runs systemctl with args and returns its stdout.
	Systemctl func(ctx context.Context, args ...string) (string, error)
	// Settle is how long a restarted node must stay active before apply succeeds.
	Settle time.Duration
	// Etc is /etc/loft (node.conf, tls/); NginxSite and PanelEnv are the
	// panel's nginx site and env file. Empty means the Debian default.
	Etc, NginxSite, PanelEnv string
	// NginxTest replaces `nginx -t` (tests).
	NginxTest func() error
	// NginxEnabled is the sites-enabled link to the site; PanelTLS holds the
	// panel's own certificate when it has a name of its own.
	NginxEnabled, PanelTLS string

	// Packages and jobs. State is the helper's own directory (job.json,
	// job.log); Exe is this binary, which the job unit runs as run-job.
	State, Exe string
	// SystemdRun starts the job unit (tests); nil runs systemd-run.
	SystemdRun func(ctx context.Context, args ...string) error
	// Exec runs a query (dpkg-query, apt-cache) and returns stdout; Stream
	// runs a job step writing to w. Nil runs the real command.
	Exec   func(ctx context.Context, name string, args ...string) (string, error)
	Stream func(ctx context.Context, w io.Writer, name string, args ...string) error
	// Root prefixes system paths a job writes (tests).
	Root string
	// HTTP and ReleaseBase are for downloading loft releases (tests).
	HTTP        *http.Client
	ReleaseBase string
	// ReleaseAPI and ReleaseDL replace api.github.com/repos and github.com
	// for release packages (tests).
	ReleaseAPI, ReleaseDL string
	// BinBase is the GitHub-style releases root for MethodBin packages
	// (tests). Empty means https://github.com/<repo>/releases/download.
	BinBase string
	// GOARCH selects the MethodBin asset (tests). Empty uses runtime.GOARCH.
	GOARCH string
	// BinSHA256 overrides catalog hashes keyed by asset name (tests).
	BinSHA256 map[string]string

	// PanelEtc is /etc/loft-panel (net.json, channels).
	PanelEtc string

	mu     sync.Mutex // one writer for network.conf and members.keys
	netMu  sync.Mutex // net.json and the held change
	chanMu sync.Mutex // channels.json and what it writes
	mailMu sync.Mutex // mail.json and the mail server's files
	sysMu  sync.Mutex // Linux accounts
	recMu  sync.Mutex // one Reconcile at a time
	extMu  sync.Mutex
	ext    map[string]extIP
	jobMu  sync.Mutex // job.json between start and status
}

// Defaults is the Debian layout from server/debian/install.sh.
func Defaults() *Ops {
	return &Ops{Dir: "/var/lib/loft", Unit: "loft-node.service", NodeBin: "/usr/bin/loft-node", UID: -1, GID: -1,
		Systemctl: runSystemctl, Settle: 3 * time.Second, NginxEnabled: "/etc/nginx/sites-enabled/loft-panel.conf"}
}

func runSystemctl(ctx context.Context, args ...string) (string, error) {
	out, err := exec.CommandContext(ctx, "systemctl", args...).Output()
	return strings.TrimSpace(string(out)), err
}

func (o *Ops) conf() string { return filepath.Join(o.Dir, "network.conf") }
func (o *Ops) keys() string { return filepath.Join(o.Dir, "members.keys") }

func sum(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// NetworkGet returns network.conf, or legacy network.yaml read-only.
func (o *Ops) NetworkGet() (admproto.Network, error) {
	b, err := os.ReadFile(o.conf())
	if errors.Is(err, os.ErrNotExist) {
		y, yerr := os.ReadFile(filepath.Join(o.Dir, "network.yaml"))
		if yerr == nil {
			// Shown as switch-style so the editor has one syntax; the first
			// apply writes network.conf, which the node prefers.
			n, perr := netconf.Parse(y)
			if perr != nil {
				return admproto.Network{}, fmt.Errorf("network.yaml: %w", perr)
			}
			return admproto.Network{Text: netconf.RenderCommands(*n), SHA256: sum(y), Legacy: true}, nil
		}
		return admproto.Network{}, nil
	}
	if err != nil {
		return admproto.Network{}, err
	}
	return admproto.Network{Text: string(b), SHA256: sum(b)}, nil
}

// current is the hash an apply must quote: network.conf, else network.yaml, else "".
func (o *Ops) current() (string, error) {
	cur, err := o.NetworkGet()
	return cur.SHA256, err
}

// NetworkApply validates text with the node's own parser, replaces
// network.conf (keeping network.conf.prev), drops keys of removed members,
// restarts the node, and puts the old file back if the node does not stay up.
func (o *Ops) NetworkApply(ctx context.Context, req admproto.NetworkApply) (admproto.Network, error) {
	if len(req.Text) > MaxNetworkConf {
		return admproto.Network{}, fmt.Errorf("network.conf: larger than %d KiB", MaxNetworkConf>>10)
	}
	n, err := netconf.ParseCommands(req.Text)
	if err != nil {
		return admproto.Network{}, fmt.Errorf("network.conf: %w", err)
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	have, err := o.current()
	if err != nil {
		return admproto.Network{}, err
	}
	if have != req.Expect {
		return admproto.Network{}, errors.New("network.conf changed since it was opened: reload and apply again")
	}
	prev, prevErr := os.ReadFile(o.conf())
	hadPrev := prevErr == nil
	if hadPrev {
		if err := o.write(o.conf()+".prev", prev, 0o640); err != nil {
			return admproto.Network{}, err
		}
	}
	body := []byte(req.Text)
	if !strings.HasSuffix(req.Text, "\n") {
		body = append(body, '\n')
	}
	keysPrev, keysErr := os.ReadFile(o.keys())
	if err := o.write(o.conf(), body, 0o640); err != nil {
		return admproto.Network{}, err
	}
	if err := o.pruneKeys(n); err != nil {
		o.restore(hadPrev, prev, keysErr == nil, keysPrev)
		return admproto.Network{}, err
	}
	if err := o.applySettled(ctx); err != nil {
		// Roll back: the node must keep running the config and keys it had.
		rb := o.restore(hadPrev, prev, keysErr == nil, keysPrev)
		if rb == nil {
			rb = o.applySettled(ctx)
		}
		if rb != nil {
			return admproto.Network{}, fmt.Errorf("loft-node did not start with the new config (%v), and rollback failed: %v", err, rb)
		}
		return admproto.Network{}, fmt.Errorf("loft-node did not start with the new config, the previous one is back: %v", err)
	}
	return admproto.Network{Text: string(body), SHA256: sum(body)}, nil
}

// restore puts network.conf and members.keys back as they were before an apply.
func (o *Ops) restore(hadConf bool, conf []byte, hadKeys bool, keys []byte) error {
	var err error
	if hadConf {
		err = o.write(o.conf(), conf, 0o640)
	} else if rm := os.Remove(o.conf()); rm != nil && !errors.Is(rm, os.ErrNotExist) {
		err = rm
	}
	if hadKeys {
		if kerr := o.write(o.keys(), keys, 0o600); err == nil {
			err = kerr
		}
	}
	return err
}

// pruneKeys keeps members.keys entries whose member is still in n with the
// same key_sha256. Keys of new members were added by MemberKey already.
func (o *Ops) pruneKeys(n *netconf.Network) error {
	r, err := identity.Load(o.keys())
	if err != nil {
		return fmt.Errorf("members.keys: %w", err)
	}
	want := map[string]string{}
	for _, m := range n.Members {
		if m.KeySHA256 != "" {
			want[m.ID] = m.KeySHA256
		}
	}
	before := r.Len()
	r.Filter(want)
	if r.Len() == before {
		return nil
	}
	return o.saveRing(r)
}

var memberID = regexp.MustCompile(`^[A-Za-z0-9][A-Za-z0-9._-]{0,63}$`)

// MemberKey creates or replaces the key of member id in members.keys.
// network.conf is untouched until the panel applies the new key_sha256.
func (o *Ops) MemberKey(req admproto.MemberKey) (admproto.IssuedKey, error) {
	if !memberID.MatchString(req.ID) {
		return admproto.IssuedKey{}, fmt.Errorf("member id %q: latin letters, digits, . _ -; up to 64", req.ID)
	}
	plain, sha, err := identity.NewKey()
	if err != nil {
		return admproto.IssuedKey{}, err
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	r, err := identity.Load(o.keys())
	if err != nil {
		return admproto.IssuedKey{}, fmt.Errorf("members.keys: %w", err)
	}
	r.Put(req.ID, plain)
	if err := o.saveRing(r); err != nil {
		return admproto.IssuedKey{}, err
	}
	return admproto.IssuedKey{ID: req.ID, Key: plain, KeySHA256: sha}, nil
}

// saveRing writes members.keys through a temp file so a crash never leaves
// it half written (the node would refuse every member).
func (o *Ops) saveRing(r *identity.Ring) error {
	tmp := o.keys() + ".new"
	if err := r.Save(tmp); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("members.keys: %w", err)
	}
	b, err := os.ReadFile(tmp)
	os.Remove(tmp)
	if err != nil {
		return fmt.Errorf("members.keys: %w", err)
	}
	return o.write(o.keys(), b, 0o600)
}

// NodeStatus asks systemd about the unit.
func (o *Ops) NodeStatus(ctx context.Context) admproto.NodeStatus {
	st := admproto.NodeStatus{Active: "unknown"}
	if out, err := o.Systemctl(ctx, "show", o.Unit, "--property=ActiveState,ActiveEnterTimestamp"); err == nil {
		for _, ln := range strings.Split(out, "\n") {
			k, v, _ := strings.Cut(ln, "=")
			switch k {
			case "ActiveState":
				st.Active = v
			case "ActiveEnterTimestamp":
				st.Since = v
			}
		}
	}
	if o.NodeBin != "" {
		cctx, cancel := context.WithTimeout(ctx, 3*time.Second)
		defer cancel()
		if out, err := exec.CommandContext(cctx, o.NodeBin, "-version").Output(); err == nil {
			st.Version = strings.TrimSpace(strings.SplitN(string(out), "\n", 2)[0])
		}
	}
	return st
}

// NodeRestart restarts the unit and waits until it settles.
func (o *Ops) NodeRestart(ctx context.Context) (admproto.NodeStatus, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	err := o.restartSettled(ctx)
	return o.NodeStatus(ctx), err
}

// applySettled makes the node take network.conf and members.keys: a reload
// (SIGHUP, members whose entry did not change stay connected) when the unit
// has ExecReload, else a restart. Either way the node must be active after.
func (o *Ops) applySettled(ctx context.Context) error {
	out, _ := o.Systemctl(ctx, "show", o.Unit, "--property=CanReload", "--value")
	if strings.TrimSpace(out) != "yes" {
		return o.restartSettled(ctx)
	}
	if _, err := o.Systemctl(ctx, "reload", o.Unit); err != nil {
		return fmt.Errorf("systemctl reload %s: %w", o.Unit, err)
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(o.Settle / 3):
	}
	if st, _ := o.Systemctl(ctx, "is-active", o.Unit); st != "active" {
		return fmt.Errorf("%s is %s", o.Unit, firstNonEmpty(st, "not active"))
	}
	return nil
}

// restartSettled restarts the unit, then requires it to be active after Settle
// (a node that crashes on a bad config exits within a second).
func (o *Ops) restartSettled(ctx context.Context) error {
	if _, err := o.Systemctl(ctx, "restart", o.Unit); err != nil {
		return fmt.Errorf("systemctl restart %s: %w", o.Unit, err)
	}
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-time.After(o.Settle):
	}
	out, _ := o.Systemctl(ctx, "is-active", o.Unit)
	if out != "active" {
		return fmt.Errorf("%s is %s", o.Unit, firstNonEmpty(out, "not active"))
	}
	return nil
}

func firstNonEmpty(a, b string) string {
	if a != "" {
		return a
	}
	return b
}

// sys prefixes p with Root in tests so jobs write under a temp tree.
func (o *Ops) sys(p string) string {
	if o.Root == "" {
		return p
	}
	return filepath.Join(o.Root, p)
}

func (o *Ops) arch() string {
	if o.GOARCH != "" {
		return o.GOARCH
	}
	return runtime.GOARCH
}

var storeWrite = store.WriteAtomic

func (o *Ops) write(path string, b []byte, mode os.FileMode) error {
	if err := store.WriteAtomic(path, b, mode); err != nil {
		return err
	}
	return o.chown(path)
}

func (o *Ops) chown(path string) error {
	if o.UID < 0 && o.GID < 0 {
		return nil
	}
	return os.Chown(path, o.UID, o.GID)
}

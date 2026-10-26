package adm

import (
	"context"
	"crypto/rand"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"

	"github.com/a3app/loft/internal/config"
	"github.com/a3app/loft/internal/identity"
	"github.com/a3app/loft/internal/transport"
	"github.com/a3app/loft/server/panel/admproto"
)

// node.conf from the Node screen: the panel edits a fixed set of commands;
// the rest of the file (cert paths, canary, controller) stays as it is.

func (o *Ops) listsFile() string { return filepath.Join(o.Dir, "lists.conf") }
func (o *Ops) loftEnv() string   { return filepath.Join(o.etc(), "loft.env") }

func (o *Ops) nodeToken() string {
	b, _ := os.ReadFile(o.loftEnv())
	for _, ln := range strings.Split(string(b), "\n") {
		if v, ok := strings.CutPrefix(strings.TrimSpace(ln), "LOFT_TOKEN="); ok {
			return strings.Trim(v, `"'`)
		}
	}
	return ""
}

// maskPubB64 is the node's X25519 mask public as base64url. Derived from
// LOFT_TOKEN only (NewMask must not run here: it starts origin-cert refresh).
func (o *Ops) maskPubB64() string {
	tok := o.nodeToken()
	if tok == "" {
		return ""
	}
	pub := transport.NodeMaskPub([]byte(tok))
	if len(pub) != 32 {
		return ""
	}
	return base64.RawURLEncoding.EncodeToString(pub)
}

// NodeConfig is node.conf, lists.conf and the node's state.
func (o *Ops) NodeConfig(ctx context.Context) (admproto.NodeConfig, error) {
	c, err := config.LoadServer(o.nodeConf())
	if err != nil {
		return admproto.NodeConfig{}, fmt.Errorf("node.conf: %w", err)
	}
	v := admproto.NodeConfig{Role: firstNonEmpty(c.Role, "controller"), Listen: c.Listen, Advertise: c.Advertise, Fronts: c.EnabledFronts(),
		GetH2: max(c.GetH2, 1), Decoy: c.Decoy, Mask: c.Mask, CoverDir: c.CoverDir, GetPath: c.GetPath, WSPath: c.WSPath, H2Path: c.H2Path,
		RosterPath: c.RosterPath, ListsPath: c.ListsPath, AuthShared: c.AuthShared, Trace: c.Trace != "",
		CDNPeers: c.CDNPeers, Relays: []admproto.Relay{}}
	if v.Decoy == nil {
		v.Decoy = []string{}
	}
	if v.CDNPeers == nil {
		v.CDNPeers = []string{}
	}
	for _, r := range c.Relays {
		v.Relays = append(v.Relays, admproto.Relay{ID: r.ID, URL: r.URL, Fronts: r.Fronts, Weight: int(r.Weight), Drain: r.Drain})
	}
	v.MaskPub = o.maskPubB64()
	if in, err := o.NodeInfo(); err == nil {
		v.ControllerPub = in.ControllerPub
	}
	if ring, err := identity.Load(o.keys()); err == nil {
		v.MemberKeys = ring.Len()
	}
	if b, err := os.ReadFile(firstNonEmpty(c.CertFile, filepath.Join(o.tlsDir(), "fullchain.pem"))); err == nil {
		if blk, _ := pem.Decode(b); blk != nil {
			if crt, err := x509.ParseCertificate(blk.Bytes); err == nil {
				v.CertUntil, v.CertNames = crt.NotAfter, crt.DNSNames
			}
		}
	}
	v.Lists, _ = config.LoadLists(o.listsFile())
	if v.Lists == nil {
		v.Lists = map[string][]string{}
	}
	if b, err := os.ReadFile(filepath.Join(o.Dir, "node-status.json")); err == nil && len(b) > 0 {
		v.Status = b
	}
	v.Node = o.NodeStatus(ctx)
	return v, nil
}

var pathRe = regexp.MustCompile(`^/[A-Za-z0-9._~/-]{1,80}$`)

var knownFronts = []string{"get", "ws", "h2", "quic", "h3", "l3"}

// NodeSave writes the panel's commands into node.conf. A change of relays
// or enrol only reloads the node; anything else restarts it, and a node
// that does not come back gets the old file.
func (o *Ops) NodeSave(ctx context.Context, in admproto.NodeConfig) (admproto.NodeConfig, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	oldText, err := os.ReadFile(o.nodeConf())
	if err != nil {
		return admproto.NodeConfig{}, err
	}
	c, err := config.ParseCommands(string(oldText))
	if err != nil {
		return admproto.NodeConfig{}, fmt.Errorf("node.conf: %w", err)
	}
	prev := *c
	if _, _, err := net.SplitHostPort(in.Listen); err != nil {
		return admproto.NodeConfig{}, fmt.Errorf("listen %q: like :443 or 0.0.0.0:443", in.Listen)
	}
	c.Listen = in.Listen
	if len(in.Fronts) == 0 {
		return admproto.NodeConfig{}, errors.New("at least one front")
	}
	for _, f := range in.Fronts {
		if !slices.Contains(knownFronts, f) {
			return admproto.NodeConfig{}, fmt.Errorf("unknown front %q", f)
		}
	}
	if slices.Equal(in.Fronts, prev.EnabledFronts()) {
		c.Fronts = prev.Fronts
	} else {
		c.Fronts = in.Fronts
	}
	if in.GetH2 < 1 || in.GetH2 > 4 {
		return admproto.NodeConfig{}, errors.New("HTTP/2 connections for GET: 1–4")
	}
	c.GetH2 = in.GetH2
	// The panel's nginx stays the first decoy: the panel is reached through it.
	if len(prev.Decoy) > 0 && (len(in.Decoy) == 0 || in.Decoy[0] != prev.Decoy[0]) {
		return admproto.NodeConfig{}, fmt.Errorf("the first web server (%s) is the panel's own and stays first", prev.Decoy[0])
	}
	for _, d := range in.Decoy {
		if _, _, err := net.SplitHostPort(d); err != nil {
			return admproto.NodeConfig{}, fmt.Errorf("web server %q: host:port", d)
		}
	}
	c.Decoy = in.Decoy
	c.Mask = in.Mask
	if in.CoverDir != "" && !filepath.IsAbs(in.CoverDir) {
		return admproto.NodeConfig{}, errors.New("site directory: an absolute path")
	}
	c.CoverDir = in.CoverDir
	paths := map[string]*string{in.GetPath: &c.GetPath, in.WSPath: &c.WSPath, in.H2Path: &c.H2Path, in.RosterPath: &c.RosterPath, in.ListsPath: &c.ListsPath}
	if len(paths) != 5 {
		return admproto.NodeConfig{}, errors.New("request paths must differ")
	}
	for p, dst := range paths {
		if !pathRe.MatchString(p) {
			return admproto.NodeConfig{}, fmt.Errorf("path %q: starts with /, letters, digits, . _ ~ - /", p)
		}
		*dst = p
	}
	c.H3Path = c.H2Path
	c.AuthShared = in.AuthShared
	c.Trace = ""
	if in.Trace {
		c.Trace = filepath.Join(o.Dir, "trace.log")
	}
	c.Relays = nil
	seen := map[string]bool{}
	for _, r := range in.Relays {
		if !chanName.MatchString(r.ID) || seen[r.ID] {
			return admproto.NodeConfig{}, fmt.Errorf("relay %q: a latin letter, then letters, digits or -, unique", r.ID)
		}
		seen[r.ID] = true
		if r.URL != "" && !strings.HasPrefix(r.URL, "https://") {
			return admproto.NodeConfig{}, fmt.Errorf("relay %s: address https://host[:port]", r.ID)
		}
		for _, f := range r.Fronts {
			if !slices.Contains(knownFronts, f) {
				return admproto.NodeConfig{}, fmt.Errorf("relay %s: unknown front %q", r.ID, f)
			}
		}
		if r.Weight < 0 || r.Weight > 255 {
			return admproto.NodeConfig{}, fmt.Errorf("relay %s: weight 0–255", r.ID)
		}
		c.Relays = append(c.Relays, config.RelayEntry{ID: r.ID, URL: r.URL, Fronts: r.Fronts, Weight: byte(r.Weight), Drain: r.Drain})
	}
	c.CDNPeers = nil
	for _, p := range in.CDNPeers {
		pp, err := netip.ParsePrefix(strings.TrimSpace(p))
		if err != nil {
			return admproto.NodeConfig{}, fmt.Errorf("CDN range %q: a subnet", p)
		}
		c.CDNPeers = append(c.CDNPeers, pp.Masked().String())
	}
	text := "# loft-node config; loft-panel edits the commands it shows.\n" + config.RenderCommands(*c)
	if _, err := config.ParseCommands(text); err != nil {
		return admproto.NodeConfig{}, fmt.Errorf("node.conf: %w", err)
	}
	if string(oldText) == text {
		return o.NodeConfig(ctx)
	}
	onlyFleet := func() bool {
		a, b := prev, *c
		a.Relays, b.Relays = nil, nil
		return config.RenderCommands(a) == config.RenderCommands(b)
	}()
	if err := o.writeKeepMode(o.nodeConf(), []byte(text)); err != nil {
		return admproto.NodeConfig{}, err
	}
	settle := o.restartSettled
	if onlyFleet {
		settle = o.applySettled
	}
	if err := settle(ctx); err != nil {
		o.writeKeepMode(o.nodeConf(), oldText)
		rb := o.restartSettled(ctx)
		if rb != nil {
			return admproto.NodeConfig{}, fmt.Errorf("loft-node did not start with the new node.conf: %v; with the old one: %v", err, rb)
		}
		return admproto.NodeConfig{}, fmt.Errorf("loft-node did not start with the new node.conf, the old one is back: %v", err)
	}
	return o.NodeConfig(ctx)
}

var listName = regexp.MustCompile(`^[a-z0-9][a-z0-9._-]{0,31}$`)

// NodeLists replaces lists.conf and reloads the node (restarts it when the
// node had no lists at all: clients learn of lists from the start-up offer).
func (o *Ops) NodeLists(ctx context.Context, in admproto.NodeLists) (admproto.NodeConfig, error) {
	o.mu.Lock()
	n := 0
	for name, l := range in.Lists {
		if !listName.MatchString(name) {
			o.mu.Unlock()
			return admproto.NodeConfig{}, fmt.Errorf("list name %q: lower-case latin, digits, . _ -", name)
		}
		for _, e := range l {
			if e == "" || strings.ContainsAny(e, " \t#") || len(e) > 253 {
				o.mu.Unlock()
				return admproto.NodeConfig{}, fmt.Errorf("list %s: %q is not a domain, .suffix or subnet", name, e)
			}
		}
		n += len(l)
	}
	if n > 200000 {
		o.mu.Unlock()
		return admproto.NodeConfig{}, errors.New("at most 200 000 entries")
	}
	had, _ := config.LoadLists(o.listsFile())
	c, _ := config.LoadServer(o.nodeConf())
	hadAny := len(had) > 0 || (c != nil && len(c.Lists) > 0)
	err := o.writeOwned(o.listsFile(), []byte(config.RenderLists(in.Lists)), 0o640, o.UID, o.GID)
	if err == nil {
		if !hadAny && len(in.Lists) > 0 {
			err = o.restartSettled(ctx)
		} else {
			err = o.applySettled(ctx)
		}
	}
	o.mu.Unlock()
	if err != nil {
		return admproto.NodeConfig{}, err
	}
	return o.NodeConfig(ctx)
}

// NodeLog is the end of the node's journal.
func (o *Ops) NodeLog(ctx context.Context) (admproto.NodeLog, error) {
	out, err := o.host(ctx, "journalctl", "-u", o.Unit, "-n", "300", "--no-pager", "-o", "short-iso")
	if err != nil && out == "" {
		return admproto.NodeLog{}, err
	}
	return admproto.NodeLog{Text: out}, nil
}

// NodeTokenRotate gives the node a new LOFT_TOKEN: the mask key (pbk) and the
// relays' secret change; the controller key and member keys do not.
func (o *Ops) NodeTokenRotate(ctx context.Context) (admproto.NodeConfig, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	old, err := os.ReadFile(o.loftEnv())
	if err != nil {
		return admproto.NodeConfig{}, err
	}
	b := make([]byte, 32)
	rand.Read(b)
	tok := hex.EncodeToString(b)
	var lines []string
	set := false
	for _, ln := range strings.Split(strings.TrimRight(string(old), "\n"), "\n") {
		if strings.HasPrefix(strings.TrimSpace(ln), "LOFT_TOKEN=") {
			ln, set = "LOFT_TOKEN="+tok, true
		}
		lines = append(lines, ln)
	}
	if !set {
		lines = append(lines, "LOFT_TOKEN="+tok)
	}
	if err := o.writeKeepMode(o.loftEnv(), []byte(strings.Join(lines, "\n")+"\n")); err != nil {
		return admproto.NodeConfig{}, err
	}
	if err := o.restartSettled(ctx); err != nil {
		o.writeKeepMode(o.loftEnv(), old)
		o.restartSettled(ctx)
		return admproto.NodeConfig{}, fmt.Errorf("loft-node did not start with the new token, the old one is back: %v", err)
	}
	return o.NodeConfig(ctx)
}

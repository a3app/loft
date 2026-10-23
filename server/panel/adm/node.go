package adm

import (
	"context"
	"crypto/ed25519"
	"crypto/tls"
	"crypto/x509"
	"encoding/hex"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"time"

	"github.com/a3app/loft/internal/config"
	"github.com/a3app/loft/internal/startup"
	"github.com/a3app/loft/server/panel/admproto"
)

// Paths outside the node data directory, with Debian defaults.
func (o *Ops) etc() string      { return firstNonEmpty(o.Etc, "/etc/loft") }
func (o *Ops) nodeConf() string { return filepath.Join(o.etc(), "node.conf") }
func (o *Ops) tlsDir() string   { return filepath.Join(o.etc(), "tls") }
func (o *Ops) nginxSite() string {
	return firstNonEmpty(o.NginxSite, "/etc/nginx/sites-available/loft-panel.conf")
}
func (o *Ops) panelEnv() string { return firstNonEmpty(o.PanelEnv, "/etc/loft-panel/panel.env") }

func (o *Ops) nginxTest() error {
	if o.NginxTest != nil {
		return o.NginxTest()
	}
	out, err := exec.Command("nginx", "-t").CombinedOutput()
	if err != nil {
		return fmt.Errorf("nginx -t: %s", strings.TrimSpace(string(out)))
	}
	return nil
}

// NodeInfo reads node.conf and the controller public key.
func (o *Ops) NodeInfo() (admproto.NodeInfo, error) {
	c, err := config.LoadServer(o.nodeConf())
	if err != nil {
		return admproto.NodeInfo{}, fmt.Errorf("node.conf: %w", err)
	}
	in := admproto.NodeInfo{Advertise: c.Advertise, GetPath: c.GetPath, WSPath: c.WSPath, H2Path: c.H2Path, H3Path: c.H3Path, RosterPath: c.RosterPath,
		Fronts: c.EnabledFronts(), Decoy: c.Decoy, Mask: c.Mask}
	if u, err := url.Parse(c.Advertise); err == nil {
		in.Host = u.Hostname()
	}
	seed := filepath.Join(o.Dir, "controller.seed")
	if c.ControllerKey != "" {
		seed = c.ControllerKey
	}
	if b, err := os.ReadFile(seed); err == nil && len(b) >= ed25519.SeedSize {
		in.ControllerPub = hex.EncodeToString(ed25519.NewKeyFromSeed(b[:ed25519.SeedSize]).Public().(ed25519.PublicKey))
	}
	if b, err := os.ReadFile(o.panelEnv()); err == nil {
		for _, ln := range strings.Split(string(b), "\n") {
			if v, ok := strings.CutPrefix(strings.TrimSpace(ln), "PANEL_HOSTS="); ok {
				in.Panel = v
			}
		}
	}
	return in, nil
}

var dnsName = regexp.MustCompile(`^(?i)([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+[a-z]{2,63}$`)

// ValidDomain is a public DNS name with at least one dot (no IPs, no ports).
func ValidDomain(s string) bool {
	return len(s) <= 253 && dnsName.MatchString(s) && net.ParseIP(s) == nil
}

// DomainSet sets the node's name (advertise) and the panel's, then rewrites
// the nginx site for them. The node restarts; any failure puts every file back.
func (o *Ops) DomainSet(ctx context.Context, req admproto.DomainSet) (admproto.NodeInfo, error) {
	norm := func(s string) string { return strings.ToLower(strings.TrimSuffix(strings.TrimSpace(s), ".")) }
	name, panel := norm(req.Name), norm(req.Panel)
	if !ValidDomain(name) {
		return admproto.NodeInfo{}, fmt.Errorf("%q is not a DNS name like vpn.example.net", req.Name)
	}
	if panel == "" {
		panel = name
	}
	if !ValidDomain(panel) {
		return admproto.NodeInfo{}, fmt.Errorf("%q is not a DNS name like vpn.example.net", req.Panel)
	}
	o.mu.Lock()
	defer o.mu.Unlock()

	nodeOld, err := os.ReadFile(o.nodeConf())
	if err != nil {
		return admproto.NodeInfo{}, err
	}
	if startup.LooksYAML(string(nodeOld)) {
		return admproto.NodeInfo{}, errors.New("node.conf is YAML: set advertise there by hand")
	}
	nodeNew := setLine(string(nodeOld), "advertise", "advertise https://"+name)
	if _, err := config.ParseCommands(nodeNew); err != nil {
		return admproto.NodeInfo{}, fmt.Errorf("edited node.conf: %w", err)
	}
	envOld, envErr := os.ReadFile(o.panelEnv())
	siteOld, siteErr := os.ReadFile(o.nginxSite())
	undo := func() {
		o.writeKeepMode(o.nodeConf(), nodeOld)
		if envErr == nil {
			o.writeKeepMode(o.panelEnv(), envOld)
		}
		if siteErr == nil {
			o.writeOwned(o.nginxSite(), siteOld, 0o644, 0, 0)
		}
	}
	if err := o.writeKeepMode(o.nodeConf(), []byte(nodeNew)); err != nil {
		return admproto.NodeInfo{}, err
	}
	if envErr == nil {
		if err := o.writeKeepMode(o.panelEnv(), []byte(setLine(string(envOld), "PANEL_HOSTS=", "PANEL_HOSTS="+panel))); err != nil {
			undo()
			return admproto.NodeInfo{}, err
		}
	}
	if siteErr == nil {
		if _, err := o.WriteSite(); err != nil {
			undo()
			return admproto.NodeInfo{}, err
		}
	}
	if err := o.restartSettled(ctx); err != nil {
		undo()
		o.restartSettled(ctx)
		return admproto.NodeInfo{}, fmt.Errorf("loft-node did not start with advertise %s, the old name is back: %v", name, err)
	}
	if siteErr == nil {
		if err := o.reloadNginx(ctx); err != nil {
			return admproto.NodeInfo{}, err
		}
	}
	return o.NodeInfo()
}

// setLine replaces the first line starting with key (switch-style word or
// KEY=) by line, or appends it. Other lines and comments stay.
func setLine(body, key, line string) string {
	ls := strings.Split(body, "\n")
	for i, l := range ls {
		t := strings.TrimSpace(l)
		if t == key || strings.HasPrefix(t, key+" ") || (strings.HasSuffix(key, "=") && strings.HasPrefix(t, key)) {
			ls[i] = line
			return strings.Join(ls, "\n")
		}
	}
	if !strings.HasSuffix(body, "\n") && body != "" {
		body += "\n"
	}
	return body + line + "\n"
}

// CertInstall checks the PEM pair and installs it. Target node: the node
// certificate (nginx shares it), node restart with rollback. Target panel:
// the panel's own certificate, nginx site rewritten to use it, nginx reload.
func (o *Ops) CertInstall(ctx context.Context, req admproto.CertInstall) (admproto.NodeStatus, error) {
	pair, err := tls.X509KeyPair([]byte(req.Fullchain), []byte(req.Key))
	if err != nil {
		return admproto.NodeStatus{}, fmt.Errorf("certificate: %w", err)
	}
	leaf, err := x509.ParseCertificate(pair.Certificate[0])
	if err != nil {
		return admproto.NodeStatus{}, fmt.Errorf("certificate: %w", err)
	}
	if time.Now().After(leaf.NotAfter) {
		return admproto.NodeStatus{}, errors.New("certificate has expired")
	}
	if b, _ := pem.Decode([]byte(req.Key)); b == nil {
		return admproto.NodeStatus{}, errors.New("key: not PEM")
	}
	switch req.Target {
	case "", "node":
	case "panel":
		return o.panelCertInstall(ctx, req)
	default:
		return admproto.NodeStatus{}, fmt.Errorf("target %q: want node or panel", req.Target)
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	fc, kp := filepath.Join(o.tlsDir(), "fullchain.pem"), filepath.Join(o.tlsDir(), "privkey.pem")
	oldC, cErr := os.ReadFile(fc)
	oldK, kErr := os.ReadFile(kp)
	if cErr == nil && kErr == nil {
		o.writeKeepMode(fc+".prev", oldC)
		o.writeKeepMode(kp+".prev", oldK)
	}
	undo := func() {
		if cErr == nil && kErr == nil {
			o.writeKeepMode(fc, oldC)
			o.writeKeepMode(kp, oldK)
		}
	}
	if err := o.writeTLS(fc, []byte(req.Fullchain)); err != nil {
		return admproto.NodeStatus{}, err
	}
	if err := o.writeTLS(kp, []byte(req.Key)); err != nil {
		undo()
		return admproto.NodeStatus{}, err
	}
	if err := o.restartSettled(ctx); err != nil {
		undo()
		o.restartSettled(ctx)
		return admproto.NodeStatus{}, fmt.Errorf("loft-node did not start with the new certificate, the old one is back: %v", err)
	}
	if exists(o.nginxSite()) {
		if err := o.nginxTest(); err == nil {
			o.reloadNginx(ctx)
		}
	}
	return o.NodeStatus(ctx), nil
}

// panelCertInstall writes the panel's own pair (root, 0600 key) and points
// the nginx site at it. nginx -t failure puts the old pair back.
func (o *Ops) panelCertInstall(ctx context.Context, req admproto.CertInstall) (admproto.NodeStatus, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if err := os.MkdirAll(o.panelTLS(), 0o700); err != nil {
		return admproto.NodeStatus{}, err
	}
	fc, kp := filepath.Join(o.panelTLS(), "fullchain.pem"), filepath.Join(o.panelTLS(), "privkey.pem")
	oldC, cErr := os.ReadFile(fc)
	oldK, kErr := os.ReadFile(kp)
	undo := func() {
		if cErr == nil && kErr == nil {
			o.writeOwned(fc, oldC, 0o644, 0, 0)
			o.writeOwned(kp, oldK, 0o600, 0, 0)
		} else {
			os.Remove(fc)
			os.Remove(kp)
		}
	}
	if err := o.writeOwned(fc, []byte(req.Fullchain), 0o644, 0, 0); err != nil {
		return admproto.NodeStatus{}, err
	}
	if err := o.writeOwned(kp, []byte(req.Key), 0o600, 0, 0); err != nil {
		undo()
		return admproto.NodeStatus{}, err
	}
	if _, err := o.WriteSite(); err != nil {
		undo()
		return admproto.NodeStatus{}, err
	}
	if err := o.nginxTest(); err != nil {
		undo()
		o.WriteSite()
		return admproto.NodeStatus{}, err
	}
	if err := o.reloadNginx(ctx); err != nil {
		return admproto.NodeStatus{}, err
	}
	return o.NodeStatus(ctx), nil
}

// writeTLS writes root:loft 0640 like the installer (the node reads it as loft).
func (o *Ops) writeTLS(path string, b []byte) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o750); err != nil {
		return err
	}
	return o.writeOwned(path, b, 0o640, 0, o.GID)
}

// writeKeepMode replaces path atomically, keeping its mode and owner (0640
// root:root for a new file).
func (o *Ops) writeKeepMode(path string, b []byte) error {
	mode, uid, gid := os.FileMode(0o640), -1, -1
	if st, err := os.Stat(path); err == nil {
		mode = st.Mode().Perm()
		if u, g, ok := owner(st); ok {
			uid, gid = u, g
		}
	}
	return o.writeOwned(path, b, mode, uid, gid)
}

func (o *Ops) writeOwned(path string, b []byte, mode os.FileMode, uid, gid int) error {
	if err := storeWrite(path, b, mode); err != nil {
		return err
	}
	if o.UID < 0 && o.GID < 0 { // tests: files belong to the test user
		return nil
	}
	return os.Chown(path, uid, gid)
}

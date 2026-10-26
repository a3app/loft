package adm

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/a3app/loft/server/panel/admproto"
	"github.com/a3app/loft/server/panel/catalog"
)

const (
	mtprotoDir     = "/var/lib/loft-mtproto"
	mtprotoTOML    = "/var/lib/loft-mtproto/mtg.toml"
	mtprotoSetFile = "/var/lib/loft-mtproto/settings.json"
	mtprotoVerFile = "/var/lib/loft-mtproto/version"
	mtprotoPort    = 8448
	// mtprotoDomain is the fallback TLS domain when the node has no DNS name.
	// A foreign name on this server's IP is easy to spot (the SNI does not
	// match the address), so the node's own name is the default.
	mtprotoDomain = "cloudflare.com"
)

// Ports the MTProto proxy must not take: they already belong to the node,
// nginx or the panel.
var mtprotoTaken = map[int]string{
	80:   "nginx (ACME)",
	443:  "loft-node",
	8091: "loft-panel",
	8443: "nginx (panel)",
}

type mtprotoFile struct {
	Port   int    `json:"port"`
	Domain string `json:"domain"`
	Secret string `json:"secret"`
}

func (o *Ops) mtprotoInstalled() bool {
	p, ok := catalog.Find("mtproto")
	if !ok || p.Bin == nil {
		return false
	}
	_, err := os.Stat(o.sys(p.Bin.Path))
	return err == nil
}

func (r *runner) ensureMTProto() error {
	if err := os.MkdirAll(r.path(mtprotoDir), 0o755); err != nil {
		return err
	}
	s, err := r.o.readMTProto()
	if err != nil {
		domain := mtprotoDomain
		if in, e := r.o.NodeInfo(); e == nil && ValidDomain(in.Host) {
			domain = strings.ToLower(in.Host)
		}
		s, err = newMTProto(mtprotoPort, domain)
		if err != nil {
			return err
		}
	}
	return r.o.writeMTProto(s)
}

func newMTProto(port int, domain string) (mtprotoFile, error) {
	sec, err := mtprotoSecret(domain)
	if err != nil {
		return mtprotoFile{}, err
	}
	return mtprotoFile{Port: port, Domain: domain, Secret: sec}, nil
}

// mtprotoSecret is mtg FakeTLS: "ee" + 16 random bytes hex + hostname hex.
func mtprotoSecret(domain string) (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return "ee" + hex.EncodeToString(b) + hex.EncodeToString([]byte(strings.ToLower(domain))), nil
}

func (o *Ops) readMTProto() (mtprotoFile, error) {
	b, err := os.ReadFile(o.sys(mtprotoSetFile))
	if err != nil {
		return mtprotoFile{}, err
	}
	var s mtprotoFile
	if err := json.Unmarshal(b, &s); err != nil {
		return mtprotoFile{}, fmt.Errorf("mtproto settings: %w", err)
	}
	if s.Port == 0 || s.Secret == "" || s.Domain == "" {
		return mtprotoFile{}, fmt.Errorf("mtproto settings: incomplete")
	}
	return s, nil
}

func (o *Ops) writeMTProto(s mtprotoFile) error {
	if err := os.MkdirAll(o.sys(mtprotoDir), 0o755); err != nil {
		return err
	}
	body, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := storeWrite(o.sys(mtprotoSetFile), append(body, '\n'), 0o644); err != nil {
		return err
	}
	toml := fmt.Sprintf("# Written by loft-panel; edit through the panel.\nsecret = %q\nbind-to = %q\n",
		s.Secret, fmt.Sprintf("0.0.0.0:%d", s.Port))
	return storeWrite(o.sys(mtprotoTOML), []byte(toml), 0o644)
}

func (o *Ops) mtprotoView(s mtprotoFile) admproto.MTProto {
	out := admproto.MTProto{Installed: true, Port: s.Port, Domain: s.Domain, Secret: s.Secret}
	if b, err := os.ReadFile(o.sys(mtprotoVerFile)); err == nil {
		out.Version = strings.TrimSpace(string(b))
	}
	if in, err := o.NodeInfo(); err == nil {
		out.Host = in.Host
	}
	out.DomainMatch = sameServer(s.Domain, out.Host)
	if out.Host != "" && s.Secret != "" {
		q := url.Values{"server": {out.Host}, "port": {strconv.Itoa(s.Port)}, "secret": {s.Secret}}
		out.Link = "tg://proxy?" + q.Encode()
		out.LinkHTTPS = "https://t.me/proxy?" + q.Encode()
	}
	return out
}

// sameServer reports whether domain is this server: the node's own name, or a
// name that resolves to one of the node name's addresses.
func sameServer(domain, host string) bool {
	if domain == "" || host == "" {
		return false
	}
	if strings.EqualFold(domain, host) {
		return true
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	want, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return false
	}
	got, err := net.DefaultResolver.LookupIPAddr(ctx, domain)
	if err != nil {
		return false
	}
	for _, a := range got {
		for _, b := range want {
			if a.IP.Equal(b.IP) {
				return true
			}
		}
	}
	return false
}

// MTProtoGet is the proxy settings and share links, or installed=false.
func (o *Ops) MTProtoGet() (admproto.MTProto, error) {
	if !o.mtprotoInstalled() {
		return admproto.MTProto{Installed: false}, nil
	}
	s, err := o.readMTProto()
	if err != nil {
		return admproto.MTProto{}, err
	}
	return o.mtprotoView(s), nil
}

// MTProtoSet writes port/domain/secret and restarts the unit.
func (o *Ops) MTProtoSet(ctx context.Context, req admproto.MTProtoSet) (admproto.MTProto, error) {
	if !o.mtprotoInstalled() {
		return admproto.MTProto{}, fmt.Errorf("package %q is not installed", "mtproto")
	}
	s, err := o.readMTProto()
	if err != nil {
		return admproto.MTProto{}, err
	}
	changed := req.RotateSecret
	if req.Port != 0 && req.Port != s.Port {
		if err := checkMTProtoPort(req.Port); err != nil {
			return admproto.MTProto{}, err
		}
		s.Port = req.Port
		changed = true
	}
	if req.Domain != "" && !strings.EqualFold(req.Domain, s.Domain) {
		d := strings.ToLower(strings.TrimSuffix(strings.TrimSpace(req.Domain), "."))
		if !ValidDomain(d) {
			return admproto.MTProto{}, fmt.Errorf("%q is not a DNS name like cloudflare.com", req.Domain)
		}
		s.Domain = d
		req.RotateSecret = true // FakeTLS encodes the domain in the secret
		changed = true
	}
	if req.RotateSecret {
		sec, err := mtprotoSecret(s.Domain)
		if err != nil {
			return admproto.MTProto{}, err
		}
		s.Secret = sec
		changed = true
	}
	if !changed {
		return o.mtprotoView(s), nil
	}
	if err := o.writeMTProto(s); err != nil {
		return admproto.MTProto{}, err
	}
	if err := o.restartMTProto(ctx); err != nil {
		return admproto.MTProto{}, err
	}
	return o.mtprotoView(s), nil
}

func checkMTProtoPort(port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("port %d is not a TCP port", port)
	}
	if n, ok := mtprotoTaken[port]; ok {
		return fmt.Errorf("port %d is used by %s", port, n)
	}
	return nil
}

func (o *Ops) restartMTProto(ctx context.Context) error {
	p, _ := catalog.Find("mtproto")
	if o.Root != "" {
		// Tests: no real systemd.
		return nil
	}
	if out, err := o.exec(ctx, "systemd-run", "--wait", "--pipe", "--collect", "--quiet", "systemctl", "restart", p.Unit); err != nil {
		return fmt.Errorf("systemctl restart %s: %v %s", p.Unit, err, strings.TrimSpace(out))
	}
	return nil
}

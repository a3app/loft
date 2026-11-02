package adm

// This file manages the optional VLESS inbound: Xray-core listening with
// TCP + REALITY + xtls-rprx-vision. The catalog unit loft-vless.service waits
// on /etc/loft-panel/vless.json (ConditionPathExists). Reality keys live at
// package level; each member with proto vless gets a UUID. Listen is a
// dedicated high port (default 4433). 443 stays loft-node.

import (
	"bytes"
	"context"
	"crypto/ecdh"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"

	"github.com/a3app/loft/server/panel/admproto"
	"github.com/a3app/loft/server/panel/catalog"
)

const (
	vlessPort = 4433
	// vlessDest is the REALITY handshake target. The client SNI is this host;
	// the TCP dest Xray dials is the same host:443.
	vlessDest = "www.microsoft.com:443"
	vlessFlow = "xtls-rprx-vision"
)

// Ports the VLESS inbound must not take: they already belong to the node,
// nginx or the panel.
var vlessTaken = map[int]string{
	80:   "nginx (ACME)",
	443:  "loft-node",
	8091: "loft-panel",
	8443: "nginx (panel)",
}

var vlessUUID = regexp.MustCompile(`(?i)^[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}$`)

// vlessFile is vless-settings.json: listen port, REALITY dest and the
// X25519 pair. vless.json (what Xray loads) is written from this plus the
// member UUID map.
type vlessFile struct {
	Port       int    `json:"port"`
	Dest       string `json:"dest"`
	PrivateKey string `json:"private_key"`
	PublicKey  string `json:"public_key"`
	ShortID    string `json:"short_id"`
}

type vlessClientRow struct {
	ID   string `json:"id"`
	UUID string `json:"uuid"`
}

type vlessClientsDoc struct {
	Clients []vlessClientRow `json:"clients"`
}

func (o *Ops) vlessDir() string {
	return firstNonEmpty(o.PanelEtc, "/etc/loft-panel")
}

func (o *Ops) vlessConfFile() string    { return filepath.Join(o.vlessDir(), "vless.json") }
func (o *Ops) vlessSetFile() string     { return filepath.Join(o.vlessDir(), "vless-settings.json") }
func (o *Ops) vlessClientsFile() string { return filepath.Join(o.vlessDir(), "vless-clients.json") }

func (o *Ops) vlessInstalled() bool {
	p, ok := catalog.Find("vless")
	return ok && o.releaseInstalled(p)
}

// ensureVLESS runs during package install. Existing keys and member UUIDs
// stay (a reinstall must not break already-issued vless:// links).
func (r *runner) ensureVLESS() error {
	return r.o.ensureVLESS()
}

func (o *Ops) ensureVLESS() error {
	o.vlessMu.Lock()
	defer o.vlessMu.Unlock()
	s, err := o.readVLESS()
	if err != nil {
		s, err = newVLESS(vlessPort, vlessDest)
		if err != nil {
			return err
		}
	}
	return o.writeVLESSLocked(s, o.readVLESSClients())
}

func newVLESS(port int, dest string) (vlessFile, error) {
	priv, pub, err := realityKeyPair()
	if err != nil {
		return vlessFile{}, err
	}
	sid, err := newShortID()
	if err != nil {
		return vlessFile{}, err
	}
	return vlessFile{Port: port, Dest: dest, PrivateKey: priv, PublicKey: pub, ShortID: sid}, nil
}

func realityKeyPair() (priv, pub string, err error) {
	k, err := ecdh.X25519().GenerateKey(rand.Reader)
	if err != nil {
		return "", "", err
	}
	return base64.RawURLEncoding.EncodeToString(k.Bytes()),
		base64.RawURLEncoding.EncodeToString(k.PublicKey().Bytes()), nil
}

func newShortID() (string, error) {
	var b [4]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	return hex.EncodeToString(b[:]), nil
}

func newUUIDv4() (string, error) {
	var b [16]byte
	if _, err := rand.Read(b[:]); err != nil {
		return "", err
	}
	b[6] = (b[6] & 0x0f) | 0x40
	b[8] = (b[8] & 0x3f) | 0x80
	return fmt.Sprintf("%x-%x-%x-%x-%x", b[0:4], b[4:6], b[6:8], b[8:10], b[10:]), nil
}

func (o *Ops) readVLESS() (vlessFile, error) {
	b, err := os.ReadFile(o.vlessSetFile())
	if err != nil {
		return vlessFile{}, err
	}
	var s vlessFile
	if err := json.Unmarshal(b, &s); err != nil {
		return vlessFile{}, fmt.Errorf("vless settings: %w", err)
	}
	if s.Port == 0 || s.Dest == "" || s.PrivateKey == "" || s.PublicKey == "" || s.ShortID == "" {
		return vlessFile{}, fmt.Errorf("vless settings: incomplete")
	}
	return s, nil
}

func (o *Ops) readVLESSClients() map[string]string {
	out := map[string]string{}
	b, err := os.ReadFile(o.vlessClientsFile())
	if err != nil {
		return out
	}
	var doc vlessClientsDoc
	if json.Unmarshal(b, &doc) != nil {
		return out
	}
	for _, c := range doc.Clients {
		if memberID.MatchString(c.ID) && vlessUUID.MatchString(c.UUID) {
			out[c.ID] = strings.ToLower(c.UUID)
		}
	}
	return out
}

// writeVLESSLocked saves settings, the UUID map and vless.json. Caller holds
// vlessMu. It does not restart the unit.
func (o *Ops) writeVLESSLocked(s vlessFile, clients map[string]string) error {
	if err := os.MkdirAll(o.vlessDir(), 0o755); err != nil {
		return err
	}
	body, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return err
	}
	if err := storeWrite(o.vlessSetFile(), append(body, '\n'), 0o600); err != nil {
		return err
	}
	doc := vlessClientsDoc{}
	for _, id := range sortedKeys(clients) {
		doc.Clients = append(doc.Clients, vlessClientRow{ID: id, UUID: clients[id]})
	}
	cb, err := json.MarshalIndent(doc, "", "  ")
	if err != nil {
		return err
	}
	if err := storeWrite(o.vlessClientsFile(), append(cb, '\n'), 0o600); err != nil {
		return err
	}
	conf, err := vlessConfig(s, clients)
	if err != nil {
		return err
	}
	old, _ := os.ReadFile(o.vlessConfFile())
	if bytes.Equal(old, conf) {
		return nil
	}
	return storeWrite(o.vlessConfFile(), conf, 0o600)
}

// vlessConfig is the Xray inbound: TCP + REALITY + Vision. Transport is
// tcp only; ws/grpc/kcp/quic are not written.
func vlessConfig(s vlessFile, clients map[string]string) ([]byte, error) {
	sni := destHost(s.Dest)
	users := make([]any, 0, len(clients))
	for _, id := range sortedKeys(clients) {
		users = append(users, map[string]any{
			"id": clients[id], "flow": vlessFlow, "email": id,
		})
	}
	conf := map[string]any{
		"log": map[string]any{"loglevel": "warning"},
		"inbounds": []any{map[string]any{
			"tag":      "vless-in",
			"listen":   "0.0.0.0",
			"port":     s.Port,
			"protocol": "vless",
			"settings": map[string]any{
				"clients":    users,
				"decryption": "none",
			},
			"streamSettings": map[string]any{
				"network":  "tcp",
				"security": "reality",
				"realitySettings": map[string]any{
					"show":        false,
					"dest":        s.Dest,
					"xver":        0,
					"serverNames": []any{sni},
					"privateKey":  s.PrivateKey,
					"shortIds":    []any{s.ShortID},
				},
			},
			"sniffing": map[string]any{
				"enabled":      true,
				"destOverride": []any{"http", "tls"},
			},
		}},
		"outbounds": []any{map[string]any{"protocol": "freedom", "tag": "direct"}},
	}
	b, err := json.MarshalIndent(conf, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(b, '\n'), nil
}

func destHost(dest string) string {
	host, _, err := net.SplitHostPort(dest)
	if err != nil {
		return dest
	}
	return host
}

func parseVLESSDest(s string) (string, error) {
	s = strings.ToLower(strings.TrimSpace(strings.TrimSuffix(s, ".")))
	if s == "" {
		return "", fmt.Errorf("dest: host:port like %s", vlessDest)
	}
	host, port := s, 443
	if h, p, err := net.SplitHostPort(s); err == nil {
		n, err := strconv.Atoi(p)
		if err != nil || n < 1 || n > 65535 {
			return "", fmt.Errorf("dest port %q is not a TCP port", p)
		}
		host, port = h, n
	} else if strings.Contains(s, ":") {
		return "", fmt.Errorf("dest: host:port like %s", vlessDest)
	}
	host = strings.TrimSuffix(host, ".")
	if !ValidDomain(host) && net.ParseIP(host) == nil {
		return "", fmt.Errorf("%q is not a DNS name or address", host)
	}
	return net.JoinHostPort(host, strconv.Itoa(port)), nil
}

func (o *Ops) vlessView(s vlessFile, clients map[string]string) admproto.VLESS {
	out := admproto.VLESS{
		Installed: true,
		Port:      s.Port,
		Dest:      s.Dest,
		SNI:       destHost(s.Dest),
		PublicKey: s.PublicKey,
		ShortID:   s.ShortID,
		Clients:   len(clients),
	}
	if in, err := o.NodeInfo(); err == nil {
		out.Host = in.Host
	}
	return out
}

func vlessShare(s vlessFile, host, uuid, id string) string {
	if host == "" || uuid == "" {
		return ""
	}
	q := url.Values{}
	q.Set("encryption", "none")
	q.Set("flow", vlessFlow)
	q.Set("security", "reality")
	q.Set("sni", destHost(s.Dest))
	q.Set("fp", "chrome")
	q.Set("pbk", s.PublicKey)
	q.Set("sid", s.ShortID)
	q.Set("type", "tcp")
	return "vless://" + uuid + "@" + net.JoinHostPort(host, strconv.Itoa(s.Port)) + "?" + q.Encode() + "#" + url.PathEscape(id)
}

func (o *Ops) vlessShareOf(s vlessFile, id, uuid string) admproto.VLESSShare {
	out := admproto.VLESSShare{ID: id, UUID: uuid}
	if in, err := o.NodeInfo(); err == nil {
		out.Link = vlessShare(s, in.Host, uuid, id)
	}
	return out
}

// VLESSGet is the inbound settings, or installed=false.
func (o *Ops) VLESSGet() (admproto.VLESS, error) {
	if !o.vlessInstalled() {
		return admproto.VLESS{Installed: false}, nil
	}
	o.vlessMu.Lock()
	defer o.vlessMu.Unlock()
	s, err := o.readVLESS()
	if err != nil {
		if err := o.ensureVLESSLocked(); err != nil {
			return admproto.VLESS{}, err
		}
		s, err = o.readVLESS()
		if err != nil {
			return admproto.VLESS{}, err
		}
	}
	return o.vlessView(s, o.readVLESSClients()), nil
}

func (o *Ops) ensureVLESSLocked() error {
	s, err := o.readVLESS()
	if err != nil {
		s, err = newVLESS(vlessPort, vlessDest)
		if err != nil {
			return err
		}
	}
	return o.writeVLESSLocked(s, o.readVLESSClients())
}

// VLESSSet writes port/dest/keys and restarts the unit.
func (o *Ops) VLESSSet(ctx context.Context, req admproto.VLESSSet) (admproto.VLESS, error) {
	if !o.vlessInstalled() {
		return admproto.VLESS{}, fmt.Errorf("package %q is not installed", "vless")
	}
	o.vlessMu.Lock()
	defer o.vlessMu.Unlock()
	s, err := o.readVLESS()
	if err != nil {
		if err := o.ensureVLESSLocked(); err != nil {
			return admproto.VLESS{}, err
		}
		s, err = o.readVLESS()
		if err != nil {
			return admproto.VLESS{}, err
		}
	}
	changed := req.RotateKeys
	if req.Port != 0 && req.Port != s.Port {
		if err := checkVLESSPort(req.Port); err != nil {
			return admproto.VLESS{}, err
		}
		s.Port = req.Port
		changed = true
	}
	if req.Dest != "" {
		d, err := parseVLESSDest(req.Dest)
		if err != nil {
			return admproto.VLESS{}, err
		}
		if d != s.Dest {
			s.Dest = d
			changed = true
		}
	}
	if req.RotateKeys {
		priv, pub, err := realityKeyPair()
		if err != nil {
			return admproto.VLESS{}, err
		}
		sid, err := newShortID()
		if err != nil {
			return admproto.VLESS{}, err
		}
		s.PrivateKey, s.PublicKey, s.ShortID = priv, pub, sid
	}
	clients := o.readVLESSClients()
	if !changed {
		return o.vlessView(s, clients), nil
	}
	if err := o.writeVLESSLocked(s, clients); err != nil {
		return admproto.VLESS{}, err
	}
	if err := o.restartVLESS(ctx); err != nil {
		return admproto.VLESS{}, err
	}
	return o.vlessView(s, clients), nil
}

// VLESSClient mints or drops a member UUID and rewrites vless.json.
func (o *Ops) VLESSClient(ctx context.Context, req admproto.VLESSClient) (admproto.VLESSShare, error) {
	if !memberID.MatchString(req.ID) {
		return admproto.VLESSShare{}, fmt.Errorf("member id %q: latin letters, digits, . _ -; up to 64", req.ID)
	}
	if !o.vlessInstalled() {
		return admproto.VLESSShare{}, fmt.Errorf("package %q is not installed", "vless")
	}
	o.vlessMu.Lock()
	defer o.vlessMu.Unlock()
	s, err := o.readVLESS()
	if err != nil {
		if err := o.ensureVLESSLocked(); err != nil {
			return admproto.VLESSShare{}, err
		}
		s, err = o.readVLESS()
		if err != nil {
			return admproto.VLESSShare{}, err
		}
	}
	clients := o.readVLESSClients()
	changed := false
	if req.Drop {
		if _, ok := clients[req.ID]; ok {
			delete(clients, req.ID)
			changed = true
		}
	} else if req.Mint {
		if !vlessUUID.MatchString(clients[req.ID]) {
			u, err := newUUIDv4()
			if err != nil {
				return admproto.VLESSShare{}, err
			}
			clients[req.ID] = u
			changed = true
		}
	}
	if changed {
		if err := o.writeVLESSLocked(s, clients); err != nil {
			return admproto.VLESSShare{}, err
		}
		if err := o.restartVLESS(ctx); err != nil {
			return admproto.VLESSShare{}, err
		}
	}
	return o.vlessShareOf(s, req.ID, clients[req.ID]), nil
}

func checkVLESSPort(port int) error {
	if port < 1 || port > 65535 {
		return fmt.Errorf("port %d is not a TCP port", port)
	}
	if n, ok := vlessTaken[port]; ok {
		return fmt.Errorf("port %d is used by %s", port, n)
	}
	return nil
}

func (o *Ops) restartVLESS(ctx context.Context) error {
	p, _ := catalog.Find("vless")
	if o.Root != "" {
		return nil
	}
	if out, err := o.exec(ctx, "systemd-run", "--wait", "--pipe", "--collect", "--quiet", "systemctl", "restart", p.Unit); err != nil {
		return fmt.Errorf("systemctl restart %s: %v %s", p.Unit, err, strings.TrimSpace(out))
	}
	return nil
}

func (r *runner) purgeVLESS() {
	for _, f := range []string{r.o.vlessConfFile(), r.o.vlessSetFile(), r.o.vlessClientsFile()} {
		os.Remove(f)
	}
}

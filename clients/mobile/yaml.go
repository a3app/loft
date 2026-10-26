package mobile

import (
	"fmt"
	"net/url"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/a3app/loft/internal/config"
	"github.com/a3app/loft/internal/wire"
)

// yaml.go reads and writes the client YAML the apps store per connection.
// It is a superset of the desktop client.yaml: besides the loft fields it
// carries what SSTP needs (username, password, pap_chap, tls_check) and an
// optional list of several connections.

// fileExtra holds the YAML fields that have no place in
// config.ClientConfig: SSTP login details, the TLS check switch, a default
// SNI and the display name.
type fileExtra struct {
	username string
	password string
	pap      bool
	tlsCheck *bool
	sni      string
	name     string
}

// fileYAML is the on-disk shape of one connection. kind is loft, sstp,
// vless or trojan (empty means loft). connections, when present, holds
// several such documents in one file.
type fileYAML struct {
	Name        string              `yaml:"name"`
	Kind        string              `yaml:"kind,omitempty"`
	Token       string              `yaml:"token"`
	UUID        string              `yaml:"uuid,omitempty"`
	Username    string              `yaml:"username"`
	Password    string              `yaml:"password"`
	InstanceID  string              `yaml:"instance_id"`
	MemberID    string              `yaml:"member_id"`
	Select      string              `yaml:"select"`
	Fronts      []string            `yaml:"fronts"`
	Outbounds   []config.Outbound   `yaml:"outbounds"`
	Connections []fileYAML          `yaml:"connections,omitempty"`
	Use         map[string]string   `yaml:"use"`
	Default     string              `yaml:"default"`
	ListsURL    string              `yaml:"lists_url"`
	P2P         *bool               `yaml:"p2p"`
	Pause       string              `yaml:"pause,omitempty"`
	Listen      config.ListenConfig `yaml:"listen"`
	PAPCHAP     bool                `yaml:"pap_chap"`
	TLSCheck    *bool               `yaml:"tls_check"`
	SNI         string              `yaml:"sni"`
	Edge        string              `yaml:"edge,omitempty"`
	Inner       string              `yaml:"inner,omitempty"`
}

// decodeFile parses one connection YAML into a client config plus the
// extra fields, filling the same defaults the desktop client uses.
//
// A file with connections decodes only the first one; it may not also have
// a top-level token or outbounds, because then it would be unclear which
// one is meant. Each outbound gets a browser fingerprint (chrome), a 16 KiB
// header budget and 8 requests in flight unless set; an outbound without
// an SNI takes the top-level sni.
//
// The secret is looked up in order: token, uuid (Mac VLESS files), password,
// then the user part of a vless:// or trojan:// URL. Only an SSTP-only file
// may call it a password (hence the two error texts). An edge URL (a CDN
// origin) is turned into an extra GET outbound placed first, see
// config.PinEdgeGET.
func decodeFile(raw []byte) (*config.ClientConfig, fileExtra, error) {
	var f fileYAML
	if err := yaml.Unmarshal(raw, &f); err != nil {
		return nil, fileExtra{}, err
	}
	switch strings.ToLower(strings.TrimSpace(f.Kind)) {
	case "", "loft", "sstp", "vless", "trojan":
	default:
		return nil, fileExtra{}, fmt.Errorf("kind: loft")
	}
	if len(f.Connections) > 0 {
		if len(f.Outbounds) > 0 || strings.TrimSpace(f.Token) != "" || strings.TrimSpace(f.UUID) != "" {
			return nil, fileExtra{}, fmt.Errorf("connections with top-level token/outbounds")
		}
		b, err := yaml.Marshal(f.Connections[0])
		if err != nil {
			return nil, fileExtra{}, err
		}
		return decodeFile(b)
	}
	token := strings.TrimSpace(f.Token)
	if token == "" {
		token = strings.TrimSpace(f.UUID)
	}
	if token == "" {
		token = strings.TrimSpace(f.Password)
	}
	if f.Select == "" {
		f.Select = "auto"
	}
	if len(f.Outbounds) == 0 {
		return nil, fileExtra{}, fmt.Errorf("at least one outbound is required")
	}
	sstpOnly := true
	for i := range f.Outbounds {
		o := &f.Outbounds[i]
		if o.Fingerprint == "" {
			o.Fingerprint = "chrome"
		}
		if o.HeaderBudget <= 0 {
			o.HeaderBudget = 16 * 1024
		}
		if o.Inflight <= 0 {
			o.Inflight = 8
		}
		if o.Type == "" {
			return nil, fileExtra{}, fmt.Errorf("outbound %q: type is required", o.Name)
		}
		if o.URL == "" {
			return nil, fileExtra{}, fmt.Errorf("outbound %q: url is required", o.Name)
		}
		if strings.ToLower(o.Type) != "sstp" {
			sstpOnly = false
		}
		if o.SNI == "" {
			o.SNI = f.SNI
		}
	}
	if token == "" {
		token = credFromOutbounds(f.Outbounds)
	}
	if token == "" && !sstpOnly {
		return nil, fileExtra{}, fmt.Errorf("token is required")
	}
	if sstpOnly && token == "" {
		return nil, fileExtra{}, fmt.Errorf("password is required")
	}
	cfg := &config.ClientConfig{
		Listen:     f.Listen,
		Token:      token,
		MemberID:   f.MemberID,
		InstanceID: f.InstanceID,
		Select:     f.Select,
		Fronts:     f.Fronts,
		Outbounds:  f.Outbounds,
		Use:        f.Use,
		Default:    f.Default,
		ListsURL:   f.ListsURL,
		P2P:        f.P2P,
		Pause:      f.Pause,
		EdgeURL:    strings.TrimSpace(f.Edge),
		Inner:      strings.TrimSpace(f.Inner),
	}
	if err := config.PinEdgeGET(cfg, cfg.EdgeURL); err != nil {
		return nil, fileExtra{}, err
	}
	ex := fileExtra{username: f.Username, password: f.Password, pap: f.PAPCHAP, tlsCheck: f.TLSCheck, sni: f.SNI, name: f.Name}
	if ex.password == "" {
		ex.password = token
	}
	return cfg, ex, nil
}

// marshalClient is the reverse of decodeFile: it writes the YAML the app
// stores. kind follows the first selected outbound. A single outbound also
// names the connection. An SSTP file always carries a password, falling back
// to the token.
func marshalClient(cfg *config.ClientConfig, user, pass string, pap bool, tlsCheck *bool) (string, error) {
	kind := "loft"
	switch primaryType(cfg) {
	case "sstp":
		kind = "sstp"
	case "vless":
		kind = "vless"
	case "trojan":
		kind = "trojan"
	}
	f := fileYAML{
		Kind:       kind,
		Token:      cfg.Token,
		Username:   user,
		Password:   pass,
		InstanceID: cfg.InstanceID,
		MemberID:   cfg.MemberID,
		Select:     cfg.Select,
		Fronts:     cfg.Fronts,
		Outbounds:  cfg.Outbounds,
		Use:        cfg.Use,
		Default:    cfg.Default,
		ListsURL:   cfg.ListsURL,
		P2P:        cfg.P2P,
		Pause:      cfg.Pause,
		Listen:     cfg.Listen,
		PAPCHAP:    pap,
		TLSCheck:   tlsCheck,
		Edge:       cfg.EdgeURL,
		Inner:      cfg.Inner,
	}
	if len(cfg.Outbounds) == 1 {
		f.Name = cfg.Outbounds[0].Name
	}
	if kind == "vless" && f.UUID == "" {
		f.UUID = cfg.Token
	}
	if f.Password == "" && primaryType(cfg) == "sstp" {
		f.Password = cfg.Token
	}
	b, err := yaml.Marshal(&f)
	if err != nil {
		return "", err
	}
	return string(b), nil
}

// primaryType is the lower-case type of the first selected outbound (or of
// the first outbound at all). The engine runs that kind of session.
func primaryType(cfg *config.ClientConfig) string {
	list := cfg.Pick(cfg.Select)
	if len(list) == 0 && len(cfg.Outbounds) > 0 {
		list = cfg.Outbounds
	}
	if len(list) == 0 {
		return ""
	}
	return strings.ToLower(list[0].Type)
}

// credFromOutbounds finds a secret in the user part of a vless:// or
// trojan:// outbound URL, for files that keep it only there.
func credFromOutbounds(outs []config.Outbound) string {
	for _, o := range outs {
		t := strings.ToLower(o.Type)
		if t != "vless" && t != "trojan" {
			continue
		}
		u, err := url.Parse(strings.TrimSpace(o.URL))
		if err != nil || u.User == nil {
			continue
		}
		if s := u.User.Username(); s != "" {
			return s
		}
	}
	return ""
}

// loftOnly copies cfg with only the loft fronts (get, ws, h2, quic, h3,
// l3) as outbounds, for transport.SuperviseOn.
//
// When the file names no fronts and has a GET outbound, Hello offers every
// mux front this engine can attach. Attach (0x04) joins one more front,
// such as WebSocket, to the live GET session without a new handshake; the
// node answers with the paths it really serves. An l3-only file offers
// nothing extra. P2P (direct member-to-member links) is always off here:
// all traffic goes through the node.
func loftOnly(cfg *config.ClientConfig) *config.ClientConfig {
	c := *cfg
	var out []config.Outbound
	hasGET := false
	onlyL3 := true
	for _, o := range cfg.Outbounds {
		t := strings.ToLower(o.Type)
		if !loftTypes[t] {
			continue
		}
		out = append(out, o)
		if t == "get" {
			hasGET = true
		}
		if t != "l3" {
			onlyL3 = false
		}
	}
	c.Outbounds = out
	if len(c.Fronts) == 0 && hasGET && !onlyL3 {
		// Hello offers every mux front this engine can 0x04-attach; Status paths win.
		c.Fronts = append([]string(nil), wire.FrontOrder...)
	}
	off := false
	c.P2P = &off
	return &c
}

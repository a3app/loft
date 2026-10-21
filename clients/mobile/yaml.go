package mobile

import (
	"fmt"
	"net/url"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/a3app/loft/internal/config"
)

type fileExtra struct {
	username string
	password string
	pap      bool
	tlsCheck *bool
	sni      string
	name     string
}

type fileYAML struct {
	Name        string              `yaml:"name"`
	Kind        string              `yaml:"kind,omitempty"`
	Token       string              `yaml:"token"`
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
	Listen      config.ListenConfig `yaml:"listen"`
	PAPCHAP     bool                `yaml:"pap_chap"`
	TLSCheck    *bool               `yaml:"tls_check"`
	SNI         string              `yaml:"sni"`
}

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
		if len(f.Outbounds) > 0 || strings.TrimSpace(f.Token) != "" {
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
			o.Inflight = 48
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
	}
	ex := fileExtra{username: f.Username, password: f.Password, pap: f.PAPCHAP, tlsCheck: f.TLSCheck, sni: f.SNI, name: f.Name}
	if ex.password == "" {
		ex.password = token
	}
	return cfg, ex, nil
}

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
		Listen:     cfg.Listen,
		PAPCHAP:    pap,
		TLSCheck:   tlsCheck,
	}
	if len(cfg.Outbounds) == 1 {
		f.Name = cfg.Outbounds[0].Name
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

func loftOnly(cfg *config.ClientConfig) *config.ClientConfig {
	c := *cfg
	var out []config.Outbound
	for _, o := range cfg.Outbounds {
		if loftTypes[strings.ToLower(o.Type)] {
			out = append(out, o)
		}
	}
	c.Outbounds = out
	c.Fronts = nil
	off := false
	c.P2P = &off
	return &c
}

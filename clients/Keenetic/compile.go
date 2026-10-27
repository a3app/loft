package keenetic

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/a3app/loft/clients/mobile"
	"github.com/a3app/loft/internal/config"
	"github.com/a3app/loft/internal/trojan"
	"github.com/a3app/loft/internal/vless"
	"github.com/a3app/loft/internal/wire"
)

// Runtime is what a child process dials. Loft is set for kind loft.
// Proxy is the vless:// or trojan:// URL. Tun is the Linux device name.
type Runtime struct {
	Kind  string
	Loft  *config.ClientConfig
	Proxy string
	Tun   string
	NDM   string
	IP    string
	MTU   int
	Index int
	Hosts []string
}

// Runtime builds the dial config for one connection. Profile YAML wins
// until the editor saves over it.
func (c Conn) Runtime() (Runtime, error) {
	ndm, tun, err := Names(c.Opkg)
	if err != nil {
		return Runtime{}, err
	}
	rt := Runtime{Kind: c.Kind, Tun: tun, NDM: ndm, MTU: 1280, Index: c.Opkg}
	if strings.TrimSpace(c.Profile) != "" {
		built, err := runtimeProfile(c, rt)
		if err == nil {
			return built, nil
		}
	}
	switch c.Kind {
	case "loft":
		return runtimeLoft(c, rt)
	case "vless", "trojan":
		return runtimeProxy(c, rt)
	default:
		return Runtime{}, fmt.Errorf("kind")
	}
}

func runtimeProfile(c Conn, rt Runtime) (Runtime, error) {
	cfg, err := loadClientYAML([]byte(c.Profile))
	if err != nil {
		return Runtime{}, err
	}
	kind := c.Kind
	if kind == "" && len(cfg.Outbounds) > 0 {
		kind = strings.ToLower(cfg.Outbounds[0].Type)
	}
	if kind != "loft" && kind != "vless" && kind != "trojan" {
		kind = "loft"
	}
	if len(cfg.Outbounds) > 0 {
		t := strings.ToLower(cfg.Outbounds[0].Type)
		if t == "vless" || t == "trojan" {
			kind = t
		}
	}
	rt.Kind = kind
	switch kind {
	case "vless", "trojan":
		if len(cfg.Outbounds) == 0 || cfg.Outbounds[0].URL == "" {
			return Runtime{}, fmt.Errorf("profile url")
		}
		raw := cfg.Outbounds[0].URL
		if kind == "vless" {
			if _, err := vless.ParseURL(raw, cfg.Token); err != nil {
				return Runtime{}, err
			}
		} else if _, err := trojan.ParseURL(raw, cfg.Token); err != nil {
			return Runtime{}, err
		}
		rt.Proxy = raw
		rt.IP = proxyIP(c.Opkg)
		rt.Hosts = hostsOfURL(raw)
		return rt, nil
	default:
		prepareLoft(cfg)
		rt.Loft = cfg
		rt.Hosts = hostsOfOutbounds(cfg.Outbounds)
		return rt, nil
	}
}

func runtimeLoft(c Conn, rt Runtime) (Runtime, error) {
	if c.Mode == "link" {
		if strings.TrimSpace(c.Link) == "" {
			return Runtime{}, fmt.Errorf("link")
		}
		y, err := mobile.ParseShare(c.Link)
		if err != nil {
			return Runtime{}, err
		}
		c2 := c
		c2.Profile = y
		c2.Kind = "loft"
		return runtimeProfile(c2, rt)
	}
	host := strings.TrimSpace(c.Host)
	if host == "" || strings.ContainsAny(host, " /") {
		return Runtime{}, fmt.Errorf("host")
	}
	if strings.TrimSpace(c.Token) == "" {
		return Runtime{}, fmt.Errorf("token")
	}
	if c.Port < 0 || c.Port > 65535 {
		return Runtime{}, fmt.Errorf("port")
	}
	fronts := normFronts(c.Fronts)
	if len(fronts) == 0 {
		one := c.Connect
		if one == "" {
			one = "get"
		}
		fronts = []string{one}
	}
	var outs []config.Outbound
	for _, f := range fronts {
		if !loftFront(f) {
			return Runtime{}, fmt.Errorf("front %s", f)
		}
		outs = append(outs, config.Outbound{
			Name: f,
			Type: f,
			URL:  frontURL(f, host, c.Port),
			Path: frontPath(f, c.Path),
		})
	}
	cfg := &config.ClientConfig{
		Token:      strings.TrimSpace(c.Token),
		Select:     "auto",
		Outbounds:  outs,
		CC:         c.CC,
		CCUpMbps:   uint32(c.CCUp),
		CCDownMbps: uint32(c.CCDown),
	}
	if _, err := wire.ParseCCName(c.CC); err != nil {
		return Runtime{}, err
	}
	prepareLoft(cfg)
	rt.Loft = cfg
	rt.Hosts = hostsOfOutbounds(outs)
	return rt, nil
}

func runtimeProxy(c Conn, rt Runtime) (Runtime, error) {
	raw := strings.TrimSpace(c.Link)
	if c.Mode != "link" {
		var err error
		raw, err = proxyURL(c)
		if err != nil {
			return Runtime{}, err
		}
	}
	if raw == "" {
		return Runtime{}, fmt.Errorf("link")
	}
	var err error
	if c.Kind == "vless" {
		_, err = vless.ParseURL(raw, c.UUID)
	} else {
		_, err = trojan.ParseURL(raw, c.Password)
	}
	if err != nil {
		return Runtime{}, err
	}
	rt.Proxy = raw
	rt.IP = proxyIP(c.Opkg)
	rt.Hosts = hostsOfURL(raw)
	return rt, nil
}

func proxyURL(c Conn) (string, error) {
	host := strings.TrimSpace(c.Host)
	if host == "" || strings.ContainsAny(host, " /") {
		return "", fmt.Errorf("host")
	}
	port := c.Port
	if port == 0 {
		port = 443
	}
	if port < 1 || port > 65535 {
		return "", fmt.Errorf("port")
	}
	sec := strings.ToLower(strings.TrimSpace(c.Security))
	if sec == "" {
		sec = "tls"
	}
	switch sec {
	case "tls", "reality", "none":
	default:
		return "", fmt.Errorf("security")
	}
	netw := strings.ToLower(strings.TrimSpace(c.Network))
	if netw == "" {
		netw = "tcp"
	}
	switch netw {
	case "tcp", "xhttp":
	default:
		return "", fmt.Errorf("network")
	}
	q := url.Values{}
	q.Set("security", sec)
	q.Set("type", netw)
	if c.SNI != "" {
		q.Set("sni", c.SNI)
	}
	fp := c.FP
	if fp == "" {
		fp = "chrome"
	}
	q.Set("fp", fp)
	if c.PBK != "" {
		q.Set("pbk", c.PBK)
	}
	if c.SID != "" {
		q.Set("sid", c.SID)
	}
	if c.Path != "" {
		q.Set("path", c.Path)
	}
	var u url.URL
	u.Host = net.JoinHostPort(host, strconv.Itoa(port))
	u.RawQuery = q.Encode()
	switch c.Kind {
	case "vless":
		if strings.TrimSpace(c.UUID) == "" {
			return "", fmt.Errorf("uuid")
		}
		u.Scheme = "vless"
		u.User = url.User(c.UUID)
		if c.Vision && netw == "tcp" {
			q.Set("flow", "xtls-rprx-vision")
			u.RawQuery = q.Encode()
		}
	case "trojan":
		if strings.TrimSpace(c.Password) == "" {
			return "", fmt.Errorf("password")
		}
		u.Scheme = "trojan"
		u.User = url.User(c.Password)
	default:
		return "", fmt.Errorf("kind")
	}
	return u.String(), nil
}

func prepareLoft(cfg *config.ClientConfig) {
	off := false
	cfg.P2P = &off
	cfg.Use = nil
	cfg.Default = ""
	cfg.ListsURL = ""
	cfg.Listen.KillSwitch = false
	cfg.Listen.HTTP = ""
}

func loftFront(s string) bool {
	switch s {
	case "get", "ws", "h2", "quic", "h3", "l3":
		return true
	default:
		return false
	}
}

var frontOrder = []string{"get", "ws", "h2", "quic", "h3", "l3"}

func normFronts(in []string) []string {
	seen := map[string]bool{}
	for _, f := range in {
		f = strings.ToLower(strings.TrimSpace(f))
		if loftFront(f) {
			seen[f] = true
		}
	}
	var out []string
	for _, f := range frontOrder {
		if seen[f] {
			out = append(out, f)
		}
	}
	return out
}

func frontURL(kind, host string, port int) string {
	scheme := "https"
	if kind == "ws" {
		scheme = "wss"
	}
	if port == 0 || port == 443 {
		return scheme + "://" + host
	}
	return scheme + "://" + net.JoinHostPort(host, strconv.Itoa(port))
}

func frontPath(kind, user string) string {
	user = strings.TrimSpace(user)
	switch kind {
	case "ws":
		if user == "" || user == config.DefaultGETPath {
			return config.DefaultWSPath
		}
		return user
	case "h2", "h3":
		if user == "" || user == config.DefaultGETPath {
			return config.DefaultH2Path
		}
		return user
	case "quic", "l3":
		return ""
	default:
		if user == "" {
			return config.DefaultGETPath
		}
		return user
	}
}

func proxyIP(index int) string {
	if index < 0 {
		index = 0
	}
	if index > 254 {
		index = 254
	}
	return fmt.Sprintf("198.18.%d.1", index)
}

func hostsOfOutbounds(outs []config.Outbound) []string {
	var hosts []string
	seen := map[string]bool{}
	for _, o := range outs {
		for _, h := range hostsOfURL(o.URL) {
			if !seen[h] {
				seen[h] = true
				hosts = append(hosts, h)
			}
		}
	}
	return hosts
}

func hostsOfURL(raw string) []string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return nil
	}
	h := u.Hostname()
	if h == "" {
		return nil
	}
	return []string{h}
}

func loadClientYAML(b []byte) (*config.ClientConfig, error) {
	f, err := os.CreateTemp("", "loft-keenetic-*.yaml")
	if err != nil {
		return nil, err
	}
	name := f.Name()
	defer os.Remove(name)
	if _, err := f.Write(b); err != nil {
		f.Close()
		return nil, err
	}
	if err := f.Close(); err != nil {
		return nil, err
	}
	return config.LoadClient(name)
}

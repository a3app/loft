package trojan

// Parse of trojan:// password URLs and host:port.

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"github.com/a3app/loft/internal/xtls"
)

// Endpoint is the server address and TLS options extracted from a URL.
type Endpoint struct {
	Addr        string // host:port
	Password    string
	SNI         string
	TLS         bool
	Reality     bool
	Insecure    bool
	Network     string // tcp, xhttp, …
	Fingerprint string
	PublicKey   string
	ShortID     string
	Path        string
	Host        string
	Mode        string
	PadFrom     int
	PadTo       int
}

// ParseURL reads trojan://password@host:port?…, https://host:port, or host:port.
// The query follows the Xray share-link format, as for vless://, plus
// allowInsecure/insecure to skip the certificate check. The password may
// sit in either part of the user info. password is used when the URL has
// none; the SNI defaults to the host unless that is an IP.
func ParseURL(raw, password string) (Endpoint, error) {
	var e Endpoint
	e.TLS = true
	e.Network = "tcp"
	s := strings.TrimSpace(raw)
	if s == "" {
		return e, fmt.Errorf("trojan: empty url")
	}
	low := strings.ToLower(s)
	switch {
	case strings.HasPrefix(low, "trojan://"):
		u, err := url.Parse(s)
		if err != nil {
			return e, fmt.Errorf("trojan: url")
		}
		if u.User != nil {
			e.Password = u.User.Username()
			if p, ok := u.User.Password(); ok && p != "" && e.Password == "" {
				e.Password = p
			}
		}
		host := u.Hostname()
		port := u.Port()
		if port == "" {
			port = "443"
		}
		if host == "" {
			return e, fmt.Errorf("trojan: empty url")
		}
		e.Addr = net.JoinHostPort(host, port)
		q := u.Query()
		if v := q.Get("sni"); v != "" {
			e.SNI = v
		}
		if v := q.Get("security"); v != "" {
			sec := strings.ToLower(v)
			e.TLS = sec == "tls" || sec == "xtls" || sec == "reality"
			e.Reality = sec == "reality"
		}
		if v := q.Get("type"); v != "" {
			e.Network = strings.ToLower(v)
			if e.Network == "splithttp" {
				e.Network = "xhttp"
			}
		}
		e.Fingerprint = strings.ToLower(q.Get("fp"))
		e.PublicKey = q.Get("pbk")
		e.ShortID = q.Get("sid")
		e.Path = q.Get("path")
		e.Host = q.Get("host")
		e.Mode, e.PadFrom, e.PadTo = xtls.XShare(q)
		if e.PublicKey != "" || e.ShortID != "" {
			e.Reality = true
			e.TLS = true
		}
		e.Insecure = truthy(q.Get("allowInsecure")) || truthy(q.Get("allowinsecure")) || truthy(q.Get("insecure"))
	case strings.HasPrefix(low, "https://") || strings.HasPrefix(low, "http://") || strings.HasPrefix(low, "tls://"):
		u, err := url.Parse(s)
		if err != nil {
			return e, fmt.Errorf("trojan: url")
		}
		host := u.Hostname()
		port := u.Port()
		if port == "" {
			port = "443"
		}
		if host == "" {
			return e, fmt.Errorf("trojan: empty url")
		}
		e.Addr = net.JoinHostPort(host, port)
		e.TLS = !strings.HasPrefix(low, "http://")
	default:
		host, port, err := splitHostPort(s)
		if err != nil {
			return e, err
		}
		e.Addr = net.JoinHostPort(host, port)
	}
	if e.Password == "" {
		e.Password = strings.TrimSpace(password)
	}
	if e.SNI == "" {
		h, _, _ := net.SplitHostPort(e.Addr)
		if net.ParseIP(h) == nil {
			e.SNI = h
		}
	}
	return e, nil
}

// splitHostPort reads "host" or "host:port"; port 443 by default.
func splitHostPort(s string) (string, string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", "", fmt.Errorf("trojan: empty url")
	}
	if _, _, err := net.SplitHostPort(s); err == nil {
		h, p, _ := net.SplitHostPort(s)
		return h, p, nil
	}
	if strings.Count(s, ":") == 0 {
		return s, "443", nil
	}
	if _, err := strconv.Atoi(s); err == nil {
		return "", "", fmt.Errorf("trojan: empty url")
	}
	return "", "", fmt.Errorf("trojan: url")
}

func (e Endpoint) xhttp() bool {
	return e.Network == "xhttp"
}

func (e Endpoint) reality() xtls.Reality {
	return xtls.Reality{SNI: e.SNI, Fingerprint: e.Fingerprint, PublicKey: e.PublicKey, ShortID: e.ShortID}
}

func (e Endpoint) xhttpCfg() xtls.XHTTP {
	return xtls.XHTTP{
		Host: e.Host, SNI: e.SNI, Addr: e.Addr, Path: e.Path,
		Mode: e.Mode, PadFrom: e.PadFrom, PadTo: e.PadTo,
	}
}

// truthy reads a yes/no query value the way share links write it.
func truthy(s string) bool {
	switch strings.ToLower(strings.TrimSpace(s)) {
	case "1", "true", "yes", "on":
		return true
	}
	return false
}

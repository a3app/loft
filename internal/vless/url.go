package vless

// Parse of vless:// UUID URLs and host:port.

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
	UUID        string
	SNI         string
	TLS         bool
	Reality     bool
	Vision      bool
	Network     string // tcp, xhttp, …
	Fingerprint string
	PublicKey   string // Reality pbk (base64url or hex)
	ShortID     string // Reality sid (hex)
	Path        string // XHTTP path
	Host        string // XHTTP Host header
	Mode        string // XHTTP mode (auto, stream-one, …)
}

// ParseURL reads vless://uuid@host:port?…, https://host:port, or host:port.
func ParseURL(raw, uuid string) (Endpoint, error) {
	var e Endpoint
	e.TLS = true
	e.Network = "tcp"
	s := strings.TrimSpace(raw)
	if s == "" {
		return e, fmt.Errorf("vless: empty url")
	}
	low := strings.ToLower(s)
	switch {
	case strings.HasPrefix(low, "vless://"):
		u, err := url.Parse(s)
		if err != nil {
			return e, fmt.Errorf("vless: url")
		}
		if u.User != nil {
			e.UUID = u.User.Username()
		}
		host := u.Hostname()
		port := u.Port()
		if port == "" {
			port = "443"
		}
		if host == "" {
			return e, fmt.Errorf("vless: empty url")
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
		if v := q.Get("flow"); strings.Contains(strings.ToLower(v), "vision") {
			e.Vision = true
		}
		e.Fingerprint = strings.ToLower(q.Get("fp"))
		e.PublicKey = q.Get("pbk")
		e.ShortID = q.Get("sid")
		e.Path = q.Get("path")
		e.Host = q.Get("host")
		e.Mode = strings.ToLower(q.Get("mode"))
		if e.PublicKey != "" || e.ShortID != "" {
			e.Reality = true
		}
	case strings.HasPrefix(low, "https://") || strings.HasPrefix(low, "http://") || strings.HasPrefix(low, "tls://"):
		u, err := url.Parse(s)
		if err != nil {
			return e, fmt.Errorf("vless: url")
		}
		host := u.Hostname()
		port := u.Port()
		if port == "" {
			port = "443"
		}
		if host == "" {
			return e, fmt.Errorf("vless: empty url")
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
	if e.UUID == "" {
		e.UUID = strings.TrimSpace(uuid)
	}
	if e.SNI == "" {
		h, _, _ := net.SplitHostPort(e.Addr)
		if net.ParseIP(h) == nil {
			e.SNI = h
		}
	}
	return e, nil
}

func splitHostPort(s string) (string, string, error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return "", "", fmt.Errorf("vless: empty url")
	}
	if _, _, err := net.SplitHostPort(s); err == nil {
		h, p, _ := net.SplitHostPort(s)
		return h, p, nil
	}
	if strings.Count(s, ":") == 0 {
		return s, "443", nil
	}
	if _, err := strconv.Atoi(s); err == nil {
		return "", "", fmt.Errorf("vless: empty url")
	}
	return "", "", fmt.Errorf("vless: url")
}

func (e Endpoint) xhttp() bool {
	return e.Network == "xhttp"
}

func (e Endpoint) reality() xtls.Reality {
	return xtls.Reality{SNI: e.SNI, Fingerprint: e.Fingerprint, PublicKey: e.PublicKey, ShortID: e.ShortID}
}

func (e Endpoint) xhttpCfg() xtls.XHTTP {
	return xtls.XHTTP{Host: e.Host, SNI: e.SNI, Addr: e.Addr, Path: e.Path}
}

func decodePbk(s string) ([]byte, error) { return xtls.DecodePbk(s) }
func decodeSid(s string) ([]byte, error) { return xtls.DecodeSid(s) }

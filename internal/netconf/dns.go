package netconf

// DNS upstream syntax: DoH (https://), DoT (tls://), DoQ (quic://, reserved),
// or a plain host/IP with optional :port.

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// CheckUpstream accepts DoH (https://), DoT (tls://), DoQ (quic://, reserved),
// or a plain DNS address (IP or host, optional :port).
func CheckUpstream(s string) error {
	s = strings.TrimSpace(s)
	if s == "" {
		return fmt.Errorf("empty")
	}
	switch {
	case strings.HasPrefix(s, "https://"):
		u, err := url.Parse(s)
		if err != nil || u.Host == "" {
			return fmt.Errorf("bad DoH URL")
		}
		return nil
	case strings.HasPrefix(s, "tls://"), strings.HasPrefix(s, "quic://"):
		u, err := url.Parse(s)
		if err != nil || u.Host == "" {
			return fmt.Errorf("bad URL")
		}
		return nil
	case strings.Contains(s, "://"):
		return fmt.Errorf("want https://, tls://, quic:// or an address")
	}
	host := s
	if h, _, err := net.SplitHostPort(s); err == nil {
		host = h
	}
	if net.ParseIP(host) == nil && !hostnameOK(host) {
		return fmt.Errorf("bad address")
	}
	return nil
}

// hostnameOK is a DNS hostname: ASCII alphanumerics, dot, hyphen; length 1–253.
func hostnameOK(s string) bool {
	if s == "" || len(s) > 253 {
		return false
	}
	for _, c := range s {
		if c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '-' {
			continue
		}
		return false
	}
	return true
}

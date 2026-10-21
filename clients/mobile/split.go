package mobile

import (
	"net"
	"strings"

	"github.com/a3app/loft/internal/config"
)

// FrontHosts are outbound hostnames or literal IPs to exclude from the tunnel.
func FrontHosts(cfg *config.ClientConfig) []string {
	seen := map[string]bool{}
	var out []string
	for _, o := range cfg.Outbounds {
		h := hostOf(o.URL)
		if h == "" || seen[h] {
			continue
		}
		seen[h] = true
		out = append(out, h)
	}
	return out
}

// ResolveFrontIPs looks up FrontHosts. Literal IPs pass through. Failures skip.
func ResolveFrontIPs(hosts []string) []string {
	seen := map[string]bool{}
	var out []string
	add := func(s string) {
		if s == "" || seen[s] {
			return
		}
		seen[s] = true
		out = append(out, s)
	}
	for _, h := range hosts {
		if ip := net.ParseIP(h); ip != nil {
			add(ip.String())
			continue
		}
		addrs, err := net.LookupIP(h)
		if err != nil {
			continue
		}
		for _, a := range addrs {
			if v4 := a.To4(); v4 != nil {
				add(v4.String())
			}
		}
	}
	return out
}

func hostOf(raw string) string {
	s := strings.TrimSpace(raw)
	if s == "" {
		return ""
	}
	if strings.Contains(s, "://") {
		s = s[strings.Index(s, "://")+3:]
	}
	if i := strings.LastIndex(s, "@"); i >= 0 {
		s = s[i+1:]
	}
	if i := strings.IndexAny(s, "/?"); i >= 0 {
		s = s[:i]
	}
	host, _, err := net.SplitHostPort(s)
	if err == nil {
		return host
	}
	return strings.Trim(s, "[]")
}

func joinComma(ss []string) string {
	return strings.Join(ss, ",")
}

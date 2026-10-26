package mobile

import (
	"net"
	"strings"

	"github.com/a3app/loft/internal/config"
)

// split.go works out which server addresses must stay outside the tunnel.
// The tunnel takes the default route; if traffic to the server itself went
// into the tunnel, the session would loop into itself and die.

// FrontHosts are outbound hostnames or literal IPs to exclude from the tunnel.
// EdgeURL is the CDN origin: 0/1 must not swallow the first GET.
// ("0/1" is the full-tunnel default route. If the CDN host were not
// excluded from it, the very first GET would be routed into the tunnel it
// is trying to bring up.)
func FrontHosts(cfg *config.ClientConfig) []string {
	seen := map[string]bool{}
	var out []string
	add := func(h string) {
		if h == "" || seen[h] {
			return
		}
		seen[h] = true
		out = append(out, h)
	}
	if cfg == nil {
		return out
	}
	add(hostOf(cfg.EdgeURL))
	for _, o := range cfg.Outbounds {
		add(hostOf(o.URL))
	}
	return out
}

// ResolveFrontIPs looks up FrontHosts. Literal IPs pass through. Failures skip.
// Only IPv4 results are kept.
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

// hostOf extracts the host from a URL or a bare host[:port], dropping the
// scheme, user info, path and port.
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

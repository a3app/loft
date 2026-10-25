package config

import (
	"fmt"
	"net/url"
	"strings"
)

// ParseEdgeURL is the optional CDN address from loft://?edge= or client yaml `edge`.
// Empty raw is empty. `plain` and `tight` are the node Status preset, not a URL.
// A host without a scheme is https. The origin omits port 443/80. Path is empty
// when the URL path is missing or `/`.
func ParseEdgeURL(raw string) (origin, path string, err error) {
	s := strings.TrimSpace(raw)
	if s == "" {
		return "", "", nil
	}
	switch strings.ToLower(s) {
	case "plain", "tight":
		return "", "", fmt.Errorf("edge: %q is a node preset, not a URL", s)
	}
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil || u.Hostname() == "" {
		return "", "", fmt.Errorf("edge: %q", raw)
	}
	switch strings.ToLower(u.Scheme) {
	case "https", "http":
	default:
		return "", "", fmt.Errorf("edge: %q", raw)
	}
	u.Scheme = strings.ToLower(u.Scheme)
	if u.Path != "" && u.Path != "/" {
		path = u.Path
	}
	return originOf(u), path, nil
}

// PinEdgeGET inserts one GET whose URL is the CDN origin, ahead of the node
// GET. Other fronts keep their URLs. No GET in the list: the edge is unused.
// A GET that already uses this origin is moved first, not copied.
func PinEdgeGET(cfg *ClientConfig, edge string) error {
	if cfg == nil {
		return nil
	}
	origin, path, err := ParseEdgeURL(edge)
	if err != nil || origin == "" {
		return err
	}
	var base *Outbound
	for i := range cfg.Outbounds {
		if strings.EqualFold(cfg.Outbounds[i].Type, "get") {
			b := cfg.Outbounds[i]
			base = &b
			break
		}
	}
	if base == nil {
		return nil
	}
	var nodeGET []Outbound
	var rest []Outbound
	var edged *Outbound
	for _, o := range cfg.Outbounds {
		if !strings.EqualFold(o.Type, "get") {
			rest = append(rest, o)
			continue
		}
		if sameOrigin(o.URL, origin) {
			if edged == nil {
				c := o
				if path != "" {
					c.Path = path
				} else if c.Path == "" {
					c.Path = base.Path
				}
				edged = &c
			}
			continue
		}
		nodeGET = append(nodeGET, o)
	}
	if edged == nil {
		c := *base
		c.URL = origin
		if path != "" {
			c.Path = path
		} else if c.Path == "" {
			c.Path = DefaultGETPath
		}
		if base.Mux != nil {
			v := *base.Mux
			c.Mux = &v
		}
		edged = &c
	}
	out := append([]Outbound{*edged}, nodeGET...)
	cfg.Outbounds = append(out, rest...)
	if path != "" {
		cfg.EdgeURL = origin + path
	} else {
		cfg.EdgeURL = origin
	}
	return nil
}

func originOf(u *url.URL) string {
	host := u.Hostname()
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	scheme := strings.ToLower(u.Scheme)
	port := u.Port()
	if port != "" && !((scheme == "https" && port == "443") || (scheme == "http" && port == "80")) {
		host += ":" + port
	}
	return scheme + "://" + host
}

func sameOrigin(raw, origin string) bool {
	s := strings.TrimSpace(raw)
	if s == "" {
		return false
	}
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil || u.Hostname() == "" {
		return false
	}
	o, err := url.Parse(origin)
	if err != nil || o.Hostname() == "" {
		return false
	}
	return originOf(u) == originOf(o)
}

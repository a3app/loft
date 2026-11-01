// parse.go turns loft-probe CLI targets into proto/host/port specs.
package main

import (
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
)

// spec is one probe target: proto, host, optional port/path, and the raw CLI string.
type spec struct {
	proto string // icmp tcp udp http https dns
	host  string
	port  int
	path  string
	raw   string
}

// parseSpec accepts host, host:port, or a URL. protoFlag overrides auto guess.
func parseSpec(raw, protoFlag string) (spec, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return spec{}, fmt.Errorf("empty target")
	}
	protoFlag = strings.ToLower(strings.TrimSpace(protoFlag))
	if protoFlag == "auto" {
		protoFlag = ""
	}

	if i := strings.Index(raw, "://"); i > 0 {
		return parseURL(raw, protoFlag)
	}
	if host, portStr, err := net.SplitHostPort(raw); err == nil {
		p, err := strconv.Atoi(portStr)
		if err != nil || p < 0 || p > 65535 {
			return spec{}, fmt.Errorf("bad port in %q", raw)
		}
		sp := spec{host: host, port: p, path: "/", raw: raw}
		sp.proto = pickProto(protoFlag, protoFromPort(p), host)
		if sp.proto == "http" || sp.proto == "https" {
			if sp.port == 0 {
				sp.port = defaultPort(sp.proto)
			}
		}
		return sp, nil
	}
	if strings.Contains(raw, "/") && !strings.Contains(raw, "://") {
		return spec{}, fmt.Errorf("want host, host:port, or URL: %s", raw)
	}
	sp := spec{host: raw, path: "/", raw: raw}
	sp.proto = pickProto(protoFlag, "", raw)
	if sp.port == 0 {
		sp.port = defaultPort(sp.proto)
	}
	return sp, nil
}

// parseURL reads icmp/tcp/udp/dns/http/https URLs into a spec.
func parseURL(raw, protoFlag string) (spec, error) {
	u, err := url.Parse(raw)
	if err != nil {
		return spec{}, err
	}
	scheme := strings.ToLower(u.Scheme)
	host := u.Hostname()
	if host == "" {
		return spec{}, fmt.Errorf("no host in %q", raw)
	}
	port := 0
	if u.Port() != "" {
		port, err = strconv.Atoi(u.Port())
		if err != nil {
			return spec{}, fmt.Errorf("bad port in %q", raw)
		}
	}
	path := u.RequestURI()
	if u.Scheme == "icmp" || u.Scheme == "tcp" || u.Scheme == "udp" || u.Scheme == "dns" {
		path = "/"
	}
	if path == "" {
		path = "/"
	}
	sp := spec{host: host, port: port, path: path, raw: raw}
	base := scheme
	switch scheme {
	case "icmp", "tcp", "udp", "http", "https", "dns":
	default:
		return spec{}, fmt.Errorf("unknown scheme %q", scheme)
	}
	sp.proto = pickProto(protoFlag, base, host)
	if sp.port == 0 {
		sp.port = defaultPort(sp.proto)
	}
	return sp, nil
}

// pickProto prefers -proto, then a guessed scheme/port, then icmp for IPs else https.
func pickProto(flag, guessed, host string) string {
	if flag != "" && flag != "all" {
		return flag
	}
	if guessed != "" {
		return guessed
	}
	if net.ParseIP(host) != nil {
		return "icmp"
	}
	return "https"
}

// protoFromPort maps 53/80/443 to dns/http/https; any other port is tcp.
func protoFromPort(p int) string {
	switch p {
	case 53:
		return "dns"
	case 80:
		return "http"
	case 443:
		return "https"
	default:
		return "tcp"
	}
}

// defaultPort is 80 for tcp/http, 443 for https, 53 for udp/dns, else 0.
func defaultPort(proto string) int {
	switch proto {
	case "tcp", "http":
		return 80
	case "https":
		return 443
	case "udp", "dns":
		return 53
	default:
		return 0
	}
}

// expandAll turns one host into icmp, tcp:80, tcp:443, dns, http, and https specs.
func expandAll(host string) []spec {
	host = strings.TrimSpace(host)
	if host == "" {
		host = "8.8.8.8"
	}
	if h, _, err := net.SplitHostPort(host); err == nil {
		host = h
	}
	out := []spec{
		{proto: "icmp", host: host, raw: host},
		{proto: "tcp", host: host, port: 80, raw: net.JoinHostPort(host, "80")},
		{proto: "tcp", host: host, port: 443, raw: net.JoinHostPort(host, "443")},
		{proto: "dns", host: host, port: 53, path: "/", raw: net.JoinHostPort(host, "53")},
		{proto: "http", host: host, port: 80, path: "/", raw: "http://" + host + "/"},
		{proto: "https", host: host, port: 443, path: "/", raw: "https://" + host + "/"},
	}
	return out
}

// defaultSuite is the no-args probe list (icmp, tcp, dns, http, https).
func defaultSuite() []spec {
	return []spec{
		{proto: "icmp", host: "8.8.8.8", raw: "8.8.8.8"},
		{proto: "tcp", host: "1.1.1.1", port: 80, raw: "1.1.1.1:80"},
		{proto: "tcp", host: "1.1.1.1", port: 443, raw: "1.1.1.1:443"},
		{proto: "dns", host: "8.8.8.8", port: 53, path: "/", raw: "8.8.8.8:53"},
		{proto: "http", host: "example.com", port: 80, path: "/", raw: "http://example.com/"},
		{proto: "https", host: "ifconfig.me", port: 443, path: "/ip", raw: "https://ifconfig.me/ip"},
	}
}

// displayTarget is the result-table target column for a spec.
func displayTarget(sp spec) string {
	switch sp.proto {
	case "icmp":
		return sp.host
	case "http", "https":
		if strings.Contains(sp.raw, "://") {
			return sp.raw
		}
		return sp.proto + "://" + net.JoinHostPort(sp.host, strconv.Itoa(sp.port)) + sp.path
	default:
		if sp.port > 0 {
			return net.JoinHostPort(sp.host, strconv.Itoa(sp.port))
		}
		return sp.host
	}
}

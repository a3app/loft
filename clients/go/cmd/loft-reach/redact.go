package main

import (
	"os"
	"regexp"
	"strings"
)

// Logs and the report may be shared, so addresses and bridge secrets are
// replaced before anything is written. A private bridge leaks through its
// address, its fingerprint, or its cert, so all three go.
var (
	ipv4Re   = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`)
	ipv6Re   = regexp.MustCompile(`\[?\b(?:[0-9A-Fa-f]{1,4}:){4,7}[0-9A-Fa-f]{1,4}\b\]?|\[?(?:[0-9A-Fa-f]{1,4}:)+:(?:[0-9A-Fa-f]{1,4}:?)*\]?`)
	fpRe     = regexp.MustCompile(`\b[0-9A-Fa-f]{40}\b`)
	secretRe = regexp.MustCompile(`\b(cert|url|front|fronts|ice|utls-imitate|addr)=\S+`)
)

// redact replaces IP addresses, relay and bridge fingerprints, transport
// arguments that name a bridge, and the home folder.
func redact(s string) string {
	s = secretRe.ReplaceAllString(s, "$1=[redacted]")
	s = fpRe.ReplaceAllString(s, "[fingerprint]")
	s = ipv6Re.ReplaceAllStringFunc(s, func(m string) string {
		if strings.Count(m, ":") < 2 {
			return m
		}
		return "[ipv6]"
	})
	s = ipv4Re.ReplaceAllStringFunc(s, func(m string) string {
		if strings.HasPrefix(m, "127.") {
			return m
		}
		return "[ip]"
	})
	return redactPath(s)
}

// redactPath writes the home folder as ~.
func redactPath(s string) string {
	if home, err := os.UserHomeDir(); err == nil && home != "" && home != "/" {
		s = strings.ReplaceAll(s, home, "~")
	}
	return s
}

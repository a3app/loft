// Package startup scans loft node and overlay config written as
// switch-style commands (Keenetic / D-Link / Cisco extended ACL).
//
// This is only the tokenizer shared by internal/config (node config) and
// internal/netconf (network.conf): it splits a file into lines and words,
// drops # comments, and records indentation, which marks lines that belong
// to the block above (for example the lines under "ipif overlay").
package startup

import (
	"strings"
	"unicode"
)

// Line is one config command: N is the 1-based line number in the file
// (for error messages), Indent the leading spaces, Tok the words, and Raw
// the line without its comment.
type Line struct {
	N      int
	Indent int
	Tok    []string
	Raw    string
}

// LooksYAML reports a YAML mapping (legacy node.yaml / network.yaml).
func LooksYAML(body string) bool {
	for _, raw := range strings.Split(body, "\n") {
		t := strings.TrimSpace(strings.TrimRight(raw, "\r"))
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		if t == "---" {
			return true
		}
		i := strings.IndexByte(t, ':')
		if i > 0 && !strings.Contains(t[:i], " ") {
			return true
		}
		return false
	}
	return false
}

// Lines splits a command config into tokens. Quotes keep spaces.
func Lines(body string) []Line {
	var out []Line
	n := 0
	for _, raw := range strings.Split(body, "\n") {
		n++
		line := strings.TrimRight(raw, "\r")
		trim := strings.TrimSpace(line)
		if trim == "" || strings.HasPrefix(trim, "#") {
			continue
		}
		if i := strings.IndexByte(trim, '#'); i >= 0 {
			pre := strings.TrimSpace(trim[:i])
			if pre == "" {
				continue
			}
			trim = pre
		}
		out = append(out, Line{N: n, Indent: indent(line), Tok: Split(trim), Raw: trim})
	}
	return out
}

// indent counts leading spaces; a tab counts as two.
func indent(line string) int {
	n := 0
	for _, r := range line {
		switch r {
		case ' ':
			n++
		case '\t':
			n += 2
		default:
			return n
		}
	}
	return n
}

// Split tokenizes a command. Double quotes keep spaces.
func Split(s string) []string {
	var out []string
	var b strings.Builder
	q := false
	for _, r := range s {
		switch {
		case r == '"':
			q = !q
		case unicode.IsSpace(r) && !q:
			if b.Len() > 0 {
				out = append(out, b.String())
				b.Reset()
			}
		default:
			b.WriteRune(r)
		}
	}
	if b.Len() > 0 {
		out = append(out, b.String())
	}
	return out
}

// OnOff parses enable/disable/on/off/true/false.
func OnOff(s string) (bool, bool) {
	switch strings.ToLower(s) {
	case "enable", "enabled", "on", "true", "yes":
		return true, true
	case "disable", "disabled", "off", "false", "no":
		return false, true
	}
	return false, false
}

// Word lowercases a command keyword.
func Word(s string) string { return strings.ToLower(s) }

package xtls

// Xray share-link fields that sit beside mode=: padding range and the
// extra JSON some panels append. Query wins. extra fills the holes.

import (
	"encoding/json"
	"net/url"
	"strconv"
	"strings"
)

// XShare reads mode and xPaddingBytes from a vless/trojan query.
// from,to of 0,0 means the caller should use the Xray default 100-1000.
func XShare(q url.Values) (mode string, from, to int) {
	mode = strings.ToLower(strings.TrimSpace(q.Get("mode")))
	pad := q.Get("x_padding_bytes")
	if pad == "" {
		pad = q.Get("xPaddingBytes")
	}
	var extra struct {
		Mode string `json:"mode"`
		Pad  string `json:"xPaddingBytes"`
	}
	if raw := q.Get("extra"); raw != "" && json.Unmarshal([]byte(raw), &extra) == nil {
		if mode == "" {
			mode = strings.ToLower(strings.TrimSpace(extra.Mode))
		}
		if pad == "" {
			pad = extra.Pad
		}
	}
	from, to = padRange(pad)
	return mode, from, to
}

// SplitALPN reads the alpn= share-link field: comma or plus separated.
func SplitALPN(s string) []string {
	s = strings.TrimSpace(s)
	if s == "" {
		return nil
	}
	s = strings.ReplaceAll(s, "+", ",")
	var out []string
	for _, p := range strings.Split(s, ",") {
		p = strings.TrimSpace(p)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// padRange parses "100-1000" or a single "100". A bad string is 0,0.
func padRange(s string) (int, int) {
	s = strings.TrimSpace(s)
	if s == "" {
		return 0, 0
	}
	a, b, two := strings.Cut(s, "-")
	from, err1 := strconv.Atoi(strings.TrimSpace(a))
	if !two {
		if err1 != nil || from < 0 {
			return 0, 0
		}
		return from, from
	}
	to, err2 := strconv.Atoi(strings.TrimSpace(b))
	if err1 != nil || err2 != nil || from < 0 || to < from {
		return 0, 0
	}
	return from, to
}

package ndms

import (
	"context"
	"encoding/json"
	"strings"
	"time"
)

// tabPage is the screen name in the browser tab. The stock admin uses the
// same shape: system caption, two spaces, an en dash, the screen name.
const tabPage = "Loft"

const tabSep = "  \u2013 "

// TabTitle is the document title Keenetic would build for this screen.
// An empty caption, or NDW_HIDE_TITLE_PREFIX, leaves only the screen name.
// NDW_TITLE_REVERSE_PREFIX_SUFFIX swaps the two sides.
func TabTitle(caption string, hide, reverse bool) string {
	caption = cleanCaption(caption)
	if caption == "" || hide {
		return tabPage
	}
	if reverse {
		return tabPage + tabSep + caption
	}
	return caption + tabSep + tabPage
}

func cleanCaption(s string) string {
	s = strings.TrimSpace(s)
	s = strings.Map(func(r rune) rune {
		if r < 0x20 || r == 0x7f {
			return -1
		}
		return r
	}, s)
	rs := []rune(s)
	if len(rs) > 128 {
		rs = rs[:128]
	}
	return strings.TrimSpace(string(rs))
}

// captionValue reads show/system/caption. RCI JSON has "value" already
// resolved (model, hostname, description, NDNS or SSID). ndmc text uses
// the same field name after a colon.
func captionValue(b []byte) string {
	s := strings.TrimSpace(string(b))
	if strings.HasPrefix(s, "{") {
		var doc struct {
			Value string `json:"value"`
		}
		if json.Unmarshal(b, &doc) == nil {
			return doc.Value
		}
	}
	return ndmcField(s, "value")
}

// envOn reads one NDW_* flag. RCI show/environment is a JSON object
// (or a list of name/value rows). Text is "key: 1" lines. Only "1" is on.
func envOn(b []byte, key string) bool {
	s := strings.TrimSpace(string(b))
	switch {
	case strings.HasPrefix(s, "{"):
		var obj map[string]any
		if json.Unmarshal(b, &obj) == nil {
			return truthy(obj[key])
		}
	case strings.HasPrefix(s, "["):
		var rows []map[string]any
		if json.Unmarshal(b, &rows) == nil {
			for _, row := range rows {
				name, _ := row["name"].(string)
				if name == "" {
					name, _ = row["key"].(string)
				}
				if name == key {
					return truthy(row["value"])
				}
			}
		}
	}
	for _, line := range strings.Split(s, "\n") {
		k, v, ok := splitKV(line)
		if ok && k == key {
			return truthy(v)
		}
	}
	return false
}

func ndmcField(s, key string) string {
	for _, line := range strings.Split(s, "\n") {
		k, v, ok := splitKV(line)
		if ok && k == key {
			return v
		}
	}
	return ""
}

func splitKV(line string) (string, string, bool) {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return "", "", false
	}
	i := strings.IndexAny(line, ":=")
	if i <= 0 {
		return "", "", false
	}
	k := strings.TrimSpace(line[:i])
	v := strings.TrimSpace(line[i+1:])
	v = strings.Trim(v, `"'`)
	return k, v, k != ""
}

func truthy(v any) bool {
	switch t := v.(type) {
	case string:
		return strings.Trim(strings.TrimSpace(t), `"'`) == "1"
	case float64:
		return t == 1
	case bool:
		return t
	default:
		return false
	}
}

// readTabTitle asks the router once. RCI is first, ndmc text if that
// body is missing. A miss leaves the screen name alone.
func readTabTitle(ctx context.Context, rci string) string {
	b := rciJSON(ctx, rci, "show/system/caption")
	if len(b) == 0 {
		if text, err := ndmcText(ctx, Bin{}, "show system caption"); err == nil {
			b = []byte(text)
		}
	}
	env := rciJSON(ctx, rci, "show/environment")
	if len(env) == 0 {
		if text, err := ndmcText(ctx, Bin{}, "show environment"); err == nil {
			env = []byte(text)
		}
	}
	return TabTitle(captionValue(b), envOn(env, "NDW_HIDE_TITLE_PREFIX"), envOn(env, "NDW_TITLE_REVERSE_PREFIX_SUFFIX"))
}

// tabTitle caches the browser title. Status is polled every 3s; the
// caption does not need a fresh RCI read that often.
func (p *Panel) tabTitle(ctx context.Context, rci string) string {
	p.capMu.Lock()
	fresh := !p.capAt.IsZero() && time.Since(p.capAt) < 30*time.Second
	title := p.capTitle
	p.capMu.Unlock()
	if fresh {
		return title
	}
	title = readTabTitle(ctx, rci)
	p.capMu.Lock()
	p.capAt = time.Now()
	p.capTitle = title
	p.capMu.Unlock()
	return title
}

package keenetic

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"os/exec"
	"strings"
	"time"
)

// ParseRoutes reads an RCI or ndmc JSON route table and returns the
// interface of the IPv4 default route.
func ParseRoutes(body []byte) (string, bool) {
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		return "", false
	}
	return walkRoutes(v)
}

func walkRoutes(v any) (string, bool) {
	switch t := v.(type) {
	case map[string]any:
		if s, ok := defaultIface(t); ok {
			return s, true
		}
		for _, c := range t {
			if s, ok := walkRoutes(c); ok {
				return s, true
			}
		}
	case []any:
		for _, c := range t {
			if s, ok := walkRoutes(c); ok {
				return s, true
			}
		}
	}
	return "", false
}

func defaultIface(t map[string]any) (string, bool) {
	d := firstString(t, "destination", "dst", "network", "prefix")
	if d != "0.0.0.0/0" && d != "default" && d != "0.0.0.0" {
		return "", false
	}
	if s := firstString(t, "interface", "dev", "if"); s != "" && ispRe.MatchString(s) {
		return s, true
	}
	if g, ok := t["gateway"].(map[string]any); ok {
		if s := firstString(g, "interface", "dev", "if"); s != "" && ispRe.MatchString(s) {
			return s, true
		}
	}
	return "", false
}

func firstString(t map[string]any, keys ...string) string {
	for _, k := range keys {
		if s, ok := t[k].(string); ok {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

// ParseRouteText reads "show ip route". The last field of the default
// line is the interface, when it looks like one.
func ParseRouteText(s string) (string, bool) {
	for _, line := range strings.Split(s, "\n") {
		low := strings.ToLower(line)
		if !strings.Contains(line, "0.0.0.0/0") && !strings.Contains(low, "default") {
			continue
		}
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		last := f[len(f)-1]
		if ispRe.MatchString(last) && last != "0.0.0.0/0" {
			return last, true
		}
	}
	return "", false
}

// DiscoverISP reads the default route from RCI, then from ndmc text.
// An empty result is not an error: the caller skips host pins.
func DiscoverISP(ctx context.Context, rci string, run Runner) (string, error) {
	if strings.TrimSpace(rci) != "" {
		if err := Loopback(rci); err != nil {
			return "", err
		}
		if s, ok := fetchRoutes(ctx, rci); ok {
			return s, nil
		}
	}
	text, err := ndmcText(ctx, run, "show ip route")
	if err != nil || text == "" {
		return "", nil
	}
	s, _ := ParseRouteText(text)
	return s, nil
}

// fetchRoutes GETs the RCI path. Some firmware want POST /rci/ instead.
func fetchRoutes(ctx context.Context, rci string) (string, bool) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if s, ok := routesFrom(ctx, http.MethodGet, rci, nil); ok {
		return s, true
	}
	body := strings.NewReader(`{"show":{"ip":{"route":{}}}}`)
	base := rci
	if i := strings.Index(base, "/rci/"); i >= 0 {
		base = base[:i+len("/rci/")]
	}
	return routesFrom(ctx, http.MethodPost, base, body)
}

func routesFrom(ctx context.Context, method, raw string, body io.Reader) (string, bool) {
	req, err := http.NewRequestWithContext(ctx, method, raw, body)
	if err != nil {
		return "", false
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", false
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode != 200 {
		return "", false
	}
	return ParseRoutes(b)
}

// ndmcText runs one ndmc command and keeps combined output. Bin.Run hides
// stdout on success, so the default route is read here.
func ndmcText(ctx context.Context, run Runner, line string) (string, error) {
	b, ok := run.(Bin)
	if !ok {
		return "", nil
	}
	bin := b.Path
	if bin == "" {
		bin = "ndmc"
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bin, "-c", line)
	out, err := cmd.CombinedOutput()
	if err != nil && len(out) == 0 {
		return "", err
	}
	return string(out), nil
}

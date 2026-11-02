package ndms

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
// interface and next hop of the IPv4 default route. The next hop is empty
// when the firmware reports none or 0.0.0.0 (a point-to-point WAN).
func ParseRoutes(body []byte) (iface, via string, ok bool) {
	var v any
	if err := json.Unmarshal(body, &v); err != nil {
		return "", "", false
	}
	return walkRoutes(v)
}

// walkRoutes returns the first object that looks like an IPv4 default route.
func walkRoutes(v any) (string, string, bool) {
	switch t := v.(type) {
	case map[string]any:
		if iface, via, ok := defaultIface(t); ok {
			return iface, via, true
		}
		for _, c := range t {
			if iface, via, ok := walkRoutes(c); ok {
				return iface, via, true
			}
		}
	case []any:
		for _, c := range t {
			if iface, via, ok := walkRoutes(c); ok {
				return iface, via, true
			}
		}
	}
	return "", "", false
}

// defaultIface accepts destination 0.0.0.0/0, "default", or 0.0.0.0.
// The interface may sit on the route or inside a nested gateway object.
// The name must match ispRe so a garbage field is not passed to ndmc.
// A string gateway is the next hop. 0.0.0.0 is not one.
func defaultIface(t map[string]any) (string, string, bool) {
	d := firstString(t, "destination", "dst", "network", "prefix")
	if d != "0.0.0.0/0" && d != "default" && d != "0.0.0.0" {
		return "", "", false
	}
	iface := firstString(t, "interface", "dev", "if")
	if iface == "" || !ispRe.MatchString(iface) {
		if g, ok := t["gateway"].(map[string]any); ok {
			iface = firstString(g, "interface", "dev", "if")
		}
	}
	if iface == "" || !ispRe.MatchString(iface) {
		return "", "", false
	}
	return iface, hopOf(t), true
}

// hopOf reads a next hop. A string gateway is the address. A nested gateway
// object may carry it as address or ip. 0.0.0.0 and non-addresses are empty.
func hopOf(t map[string]any) string {
	if s := firstString(t, "gateway", "gw", "via"); viaHop(s) != "" {
		return viaHop(s)
	}
	g, ok := t["gateway"].(map[string]any)
	if !ok {
		return ""
	}
	return viaHop(firstString(g, "address", "ip", "gateway"))
}

// firstString returns the first present string among keys.
func firstString(t map[string]any, keys ...string) string {
	for _, k := range keys {
		if s, ok := t[k].(string); ok {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

// ParseRouteText reads "show ip route". It knows the columnar table
// (destination, gateway, interface, flags, metric), a "via" line, and a
// line whose last field is the interface. The next hop is empty when the
// table says 0.0.0.0.
func ParseRouteText(s string) (iface, via string, ok bool) {
	for _, line := range strings.Split(s, "\n") {
		low := strings.ToLower(line)
		if !strings.Contains(line, "0.0.0.0/0") && !strings.Contains(low, "default") {
			continue
		}
		f := strings.Fields(line)
		if len(f) == 0 {
			continue
		}
		for i, w := range f {
			if w == "via" && i+2 < len(f) && viaHop(f[i+1]) != "" && ispRe.MatchString(f[i+2]) {
				return f[i+2], viaHop(f[i+1]), true
			}
		}
		if (f[0] == "0.0.0.0/0" || strings.EqualFold(f[0], "default")) && len(f) >= 3 && ispRe.MatchString(f[2]) {
			return f[2], viaHop(f[1]), true
		}
		last := f[len(f)-1]
		if ispRe.MatchString(last) && last != "0.0.0.0/0" {
			return last, "", true
		}
	}
	return "", "", false
}

// DiscoverISP reads the default route from RCI, then from ndmc text.
// An empty interface is not an error: the caller skips host pins.
// via is the next hop, or empty on a point-to-point WAN.
func DiscoverISP(ctx context.Context, rci string, run Runner) (iface, via string, err error) {
	if strings.TrimSpace(rci) != "" {
		if err := Loopback(rci); err != nil {
			return "", "", err
		}
		if iface, via, ok := fetchRoutes(ctx, rci); ok {
			return iface, via, nil
		}
	}
	text, err := ndmcText(ctx, run, "show ip route")
	if err != nil || text == "" {
		return "", "", nil
	}
	iface, via, _ = ParseRouteText(text)
	return iface, via, nil
}

// fetchRoutes GETs the RCI path. Some firmware want POST /rci/ instead.
func fetchRoutes(ctx context.Context, rci string) (string, string, bool) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	if iface, via, ok := routesFrom(ctx, http.MethodGet, rci, nil); ok {
		return iface, via, true
	}
	body := strings.NewReader(`{"show":{"ip":{"route":{}}}}`)
	base := rci
	if i := strings.Index(base, "/rci/"); i >= 0 {
		base = base[:i+len("/rci/")]
	}
	return routesFrom(ctx, http.MethodPost, base, body)
}

// routesFrom performs one RCI request and parses a 200 body.
// Any other status, or a body that is not a route table, is a miss.
func routesFrom(ctx context.Context, method, raw string, body io.Reader) (string, string, bool) {
	req, err := http.NewRequestWithContext(ctx, method, raw, body)
	if err != nil {
		return "", "", false
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", false
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode != 200 {
		return "", "", false
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

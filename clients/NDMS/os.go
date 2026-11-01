package ndms

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"
	"time"
)

// osLabel reads the first release, title or version token from ndmc text.
// "NDMS release: 4.3" and "version: 3" are the shapes TooOld already locked.
var osLabel = regexp.MustCompile(`(?i)(?:release|title|version|ndms?):\s*([0-9][0-9A-Za-z._-]*)`)

// osLead is major and an optional minor at the start of a release token.
// "5.01.C.8.0-2" is major 5, minor 1. "5.1.8" is major 5, minor 1.
var osLead = regexp.MustCompile(`^(\d+)(?:\.(\d+))?`)

// OS is one KeeneticOS release. Known is false when nothing parsed:
// a firmware we cannot read is not treated as 5.x.
type OS struct {
	Major   int
	Minor   int
	Release string
	Title   string
	Known   bool
}

// ParseOS reads RCI JSON ({"release","title"}) or "show version" text.
// A bare release token ("5.01.C.8.0-2") is accepted. The release token
// wins over the title when both are present.
func ParseOS(text string) OS {
	text = strings.TrimSpace(text)
	if text == "" {
		return OS{}
	}
	if strings.HasPrefix(text, "{") {
		var doc struct {
			Release string `json:"release"`
			Title   string `json:"title"`
		}
		if json.Unmarshal([]byte(text), &doc) == nil && (doc.Release != "" || doc.Title != "") {
			return osFrom(doc.Release, doc.Title)
		}
	}
	rel, title, other := "", "", ""
	for _, m := range osLabel.FindAllStringSubmatch(text, -1) {
		label := strings.ToLower(m[0])
		if i := strings.IndexByte(label, ':'); i >= 0 {
			label = label[:i]
		}
		switch label {
		case "release":
			if rel == "" {
				rel = m[1]
			}
		case "title":
			if title == "" {
				title = m[1]
			}
		default:
			if other == "" {
				other = m[1]
			}
		}
	}
	if rel == "" {
		rel = other
	}
	if rel == "" && title == "" && text[0] >= '0' && text[0] <= '9' {
		rel = text
	}
	return osFrom(rel, title)
}

// osFrom fills Major from the release token, then from the title.
func osFrom(release, title string) OS {
	o := OS{Release: strings.TrimSpace(release), Title: strings.TrimSpace(title)}
	src := o.Release
	if src == "" {
		src = o.Title
	}
	if !o.fill(src) && o.Title != "" && o.Title != src {
		o.fill(o.Title)
	}
	return o
}

func (o *OS) fill(token string) bool {
	m := osLead.FindStringSubmatch(strings.TrimSpace(token))
	if m == nil {
		return false
	}
	maj, err := atoi(m[1])
	if err != nil || maj <= 0 {
		return false
	}
	o.Major = maj
	o.Minor = 0
	if m[2] != "" {
		n, err := atoi(m[2])
		if err != nil {
			return false
		}
		o.Minor = n
	}
	o.Known = true
	return true
}

func atoi(s string) (int, error) {
	n := 0
	for _, c := range s {
		if c < '0' || c > '9' {
			return 0, fmt.Errorf("num")
		}
		n = n*10 + int(c-'0')
	}
	return n, nil
}

// LANAuth is true when /auth is served on a private-interface address
// and 127.0.0.1:80 is the catch-all stub (X-Detail
// "0x1210, insufficient security level").
//
// CorePack of KeeneticOS 5.00.C.12.0-0 already contains that stub text
// and the nginx comments "catch-all HTTP stub" / "default HTTP server",
// with scgi_pass auth only on the private server. 5.01.C.8.0-2 was
// measured: GET 127.0.0.1/auth is 403, GET of the Bridge0 address is the
// real /auth. A major below 5, or a version that did not parse, is not
// that layout: the loopback URL stays.
func LANAuth(o OS) bool {
	return o.Known && o.Major >= 5
}

// SessionURL is the /auth URL for this firmware.
// KeeneticOS 5 uses http://<lanIP>/auth. lanIP must be a private IPv4
// the caller discovered on this host (security-level private, or the
// local address list). The host is never taken from the request and
// never from an arbitrary YAML host: loopbackURL is used only when
// LANAuth is false, and then it must be loopback.
func SessionURL(o OS, loopbackURL, lanIP string) (string, error) {
	if !LANAuth(o) {
		if err := Loopback(loopbackURL); err != nil {
			return "", err
		}
		return strings.TrimSpace(loopbackURL), nil
	}
	ip := net.ParseIP(strings.TrimSpace(lanIP))
	if ip == nil {
		return "", fmt.Errorf("keenetic auth address")
	}
	ip4 := ip.To4()
	if ip4 == nil || !ip4.IsPrivate() {
		return "", fmt.Errorf("keenetic auth address")
	}
	return "http://" + ip4.String() + authPath(loopbackURL), nil
}

// authPath keeps a single absolute path from the configured URL.
// Anything that could rebind the host becomes /auth.
func authPath(raw string) string {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.Path == "" || u.Path == "/" {
		return "/auth"
	}
	if !strings.HasPrefix(u.Path, "/") || strings.Contains(u.Path, "//") || strings.ContainsAny(u.Path, "\\@?") {
		return "/auth"
	}
	return u.Path
}

// LocalAuth allows loopback, or a private IPv4 that is in private
// (the addresses currently on this host). A name, a public address,
// or a private address that is not local is refused, so client.yaml
// cannot send the session cookie off the router.
func LocalAuth(raw string, private []string) error {
	if err := Loopback(raw); err == nil {
		return nil
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || u.User != nil {
		return fmt.Errorf("url")
	}
	if u.Scheme != "http" && u.Scheme != "https" {
		return fmt.Errorf("url scheme")
	}
	host := u.Hostname()
	ip := net.ParseIP(host)
	if ip == nil {
		return fmt.Errorf("url host %q", host)
	}
	ip4 := ip.To4()
	if ip4 == nil || !ip4.IsPrivate() {
		return fmt.Errorf("url host %q", host)
	}
	s := ip4.String()
	for _, p := range private {
		if p == s {
			return nil
		}
	}
	return fmt.Errorf("url host %q", host)
}

// PrivateAuthIPs lists IPv4 addresses of objects whose security-level
// is private. A nested address (IPv6 on the same interface, or a guest
// segment marked protected) is not included. Order is sorted.
func PrivateAuthIPs(body []byte) []string {
	var v any
	if json.Unmarshal(body, &v) != nil {
		return nil
	}
	seen := map[string]bool{}
	var out []string
	walkPrivate(v, func(ip string) {
		if seen[ip] {
			return
		}
		seen[ip] = true
		out = append(out, ip)
	})
	sort.Strings(out)
	return out
}

func walkPrivate(v any, fn func(string)) {
	switch t := v.(type) {
	case map[string]any:
		if ip, ok := privateV4(t); ok {
			fn(ip)
			return
		}
		for _, c := range t {
			walkPrivate(c, fn)
		}
	case []any:
		for _, c := range t {
			walkPrivate(c, fn)
		}
	}
}

// privateV4 reads security-level private and an IPv4 on the same object,
// or one level down under ip / ipv4 / address.
func privateV4(t map[string]any) (string, bool) {
	sl, _ := t["security-level"].(string)
	if !strings.EqualFold(strings.TrimSpace(sl), "private") {
		return "", false
	}
	if ip, ok := ipv4Field(t["address"]); ok {
		return ip, true
	}
	for _, k := range []string{"ip", "ipv4", "address"} {
		m, ok := t[k].(map[string]any)
		if !ok {
			continue
		}
		if ip, ok := ipv4Field(m["address"]); ok {
			return ip, true
		}
	}
	return "", false
}

func ipv4Field(v any) (string, bool) {
	s, ok := v.(string)
	if !ok {
		return "", false
	}
	ip := net.ParseIP(strings.TrimSpace(s))
	if ip == nil {
		return "", false
	}
	ip4 := ip.To4()
	if ip4 == nil || !ip4.IsPrivate() {
		return "", false
	}
	return ip4.String(), true
}

// routerHTTP does not use the environment proxy and does not follow a
// redirect. The session cookie and the RCI read stay on the router.
var routerHTTP = &http.Client{
	Timeout: 3 * time.Second,
	Transport: &http.Transport{
		Proxy: nil,
	},
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse
	},
}

// ReadOS loads show/version from the loopback RCI, then from ndmc.
// An unreachable router is an unknown OS, not an error: the caller
// keeps the loopback /auth URL.
func ReadOS(ctx context.Context, rci string) OS {
	if b := rciJSON(ctx, rci, "show/version"); len(b) > 0 {
		if o := ParseOS(string(b)); o.Known {
			return o
		}
	}
	text, err := ndmcText(ctx, Bin{}, "show version")
	if err != nil || text == "" {
		return OS{}
	}
	return ParseOS(text)
}

// AuthIPv4 is the address whose /auth is the real admin on KeeneticOS 5.
// RCI show/interface is preferred, then ndmc text. A protected guest
// segment is also a private IPv4 and its listener is the stub, so the
// security-level is what selects the address. The local address list is
// only the last resort, and a 192.168/16 wins over 10/8 so a CGNAT or
// slirp WAN does not take the check.
func AuthIPv4(ctx context.Context, rci string) string {
	if b := rciJSON(ctx, rci, "show/interface"); len(b) > 0 {
		if ips := PrivateAuthIPs(b); len(ips) > 0 {
			return ips[0]
		}
	}
	if text, err := ndmcText(ctx, Bin{}, "show interface"); err == nil {
		if ips := PrivateAuthText(text); len(ips) > 0 {
			return ips[0]
		}
	}
	return preferLAN(PrivateIPv4())
}

// PrivateAuthText reads "show interface". The IPv4 on the same object as
// security-level private is kept. A later IPv6 address is not.
func PrivateAuthText(s string) []string {
	seen := map[string]bool{}
	var out []string
	pending := ""
	for _, line := range strings.Split(s, "\n") {
		f := strings.Fields(line)
		if len(f) != 2 {
			continue
		}
		key := strings.TrimSuffix(f[0], ":")
		if key == "address" {
			if ip, ok := ipv4Field(f[1]); ok {
				pending = ip
			}
			continue
		}
		if key == "security-level" {
			if strings.EqualFold(f[1], "private") && pending != "" && !seen[pending] {
				seen[pending] = true
				out = append(out, pending)
			}
			pending = ""
		}
	}
	sort.Strings(out)
	return out
}

// preferLAN picks one local private address. 192.168/16, then 172.16/12,
// then anything else PrivateIPv4 returned.
func preferLAN(ips []string) string {
	in := append([]string(nil), ips...)
	sort.Strings(in)
	best := ""
	bestRank := 9
	for _, s := range in {
		ip := net.ParseIP(s)
		if ip == nil {
			continue
		}
		ip = ip.To4()
		if ip == nil || !ip.IsPrivate() {
			continue
		}
		rank := 3
		switch {
		case ip[0] == 192 && ip[1] == 168:
			rank = 0
		case ip[0] == 172 && ip[1] >= 16 && ip[1] <= 31:
			rank = 1
		}
		if rank < bestRank || (rank == bestRank && (best == "" || s < best)) {
			best = s
			bestRank = rank
		}
	}
	return best
}

// rciJSON GETs <rci-base><leaf>, then POSTs {} if that was not JSON.
// The base is taken from the configured RCI URL and must be loopback.
func rciJSON(ctx context.Context, rci, leaf string) []byte {
	base, err := rciBase(rci)
	if err != nil {
		return nil
	}
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	raw := base + strings.TrimPrefix(leaf, "/")
	if b, ok := rciDo(ctx, http.MethodGet, raw, nil); ok {
		return b
	}
	b, ok := rciDo(ctx, http.MethodPost, raw, strings.NewReader(`{}`))
	if !ok {
		return nil
	}
	return b
}

func rciBase(raw string) (string, error) {
	if err := Loopback(raw); err != nil {
		return "", err
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", err
	}
	i := strings.Index(u.Path, "/rci/")
	if i < 0 {
		return "", fmt.Errorf("rci")
	}
	u.Path = u.Path[:i+len("/rci/")]
	u.RawQuery, u.Fragment = "", ""
	return u.String(), nil
}

// rciDo returns a JSON body. Any other status or a non-JSON body is a miss.
func rciDo(ctx context.Context, method, raw string, body io.Reader) ([]byte, bool) {
	req, err := http.NewRequestWithContext(ctx, method, raw, body)
	if err != nil {
		return nil, false
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	res, err := routerHTTP.Do(req)
	if err != nil {
		return nil, false
	}
	defer res.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode != http.StatusOK {
		return nil, false
	}
	s := strings.TrimSpace(string(b))
	if !strings.HasPrefix(s, "{") && !strings.HasPrefix(s, "[") {
		return nil, false
	}
	return b, true
}

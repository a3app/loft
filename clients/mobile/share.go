package mobile

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/a3app/loft/internal/config"
	"github.com/a3app/loft/internal/trojan"
	"github.com/a3app/loft/internal/vless"
)

// share.go is the import detector. Whatever the user scans, pastes or
// opens (a loft://, vless://, trojan:// or sstp:// link, a YAML or .loft
// file, a small VPN JSON, base64 of any of these, or several links one per
// line) is turned into the client YAML the engine runs. Both apps call it,
// so a QR code imports the same way on iOS and Android.

// loftTypes are overlay transports (get/ws/h2/quic/h3/l3). YAML kind is loft.
var loftTypes = map[string]bool{
	"get": true, "ws": true, "h2": true, "quic": true, "h3": true, "l3": true,
}

// ParseShare turns a QR / paste / file body into client YAML (docs/share.md).
// A .loft with several connections yields the first; use ParseShareAll for every one.
func ParseShare(raw string) (string, error) {
	docs, err := parseAll(raw)
	if err != nil {
		return "", err
	}
	return docs[0], nil
}

// ParseShareAll returns every connection as YAML documents joined by "\n---\n".
func ParseShareAll(raw string) (string, error) {
	docs, err := parseAll(raw)
	if err != nil {
		return "", err
	}
	return strings.Join(docs, "\n---\n"), nil
}

// parseAll tries the formats from most to least specific: a YAML with
// several connections, one share of any kind, one share per line, and last
// a base64 blob (decoded and parsed again). A BOM is stripped first. With
// several lines, bad lines are skipped as long as one parses.
func parseAll(raw string) ([]string, error) {
	s := strings.TrimSpace(strings.TrimPrefix(raw, "\ufeff"))
	if s == "" {
		return nil, fmt.Errorf("empty")
	}
	if docs := yamlConnections(s); len(docs) > 0 {
		return docs, nil
	}
	if y, err := parseOne(s); err == nil {
		return []string{y}, nil
	}
	lines := strings.Split(s, "\n")
	if len(lines) > 1 {
		var out []string
		var last error
		for _, line := range lines {
			line = strings.TrimSpace(line)
			if line == "" {
				continue
			}
			y, err := parseOne(line)
			if err != nil {
				last = err
				continue
			}
			out = append(out, y)
		}
		if len(out) > 0 {
			return out, nil
		}
		if last != nil {
			return nil, last
		}
	}
	if decoded := unwrapBase64(s); decoded != "" {
		return parseAll(decoded)
	}
	return nil, fmt.Errorf("unknown share")
}

// yamlConnections splits a .loft file with a connections list into one
// normalised YAML per connection. Nil when s is not such a file or any
// entry is invalid.
func yamlConnections(s string) []string {
	var f fileYAML
	if err := yaml.Unmarshal([]byte(s), &f); err != nil {
		return nil
	}
	if len(f.Connections) == 0 {
		return nil
	}
	var out []string
	for i := range f.Connections {
		b, err := yaml.Marshal(&f.Connections[i])
		if err != nil {
			return nil
		}
		y, err := yamlFile(string(b))
		if err != nil {
			return nil
		}
		out = append(out, y)
	}
	return out
}

// parseOne parses one share by its URL scheme, else as YAML, else as VPN
// JSON.
func parseOne(s string) (string, error) {
	low := strings.ToLower(s)
	switch {
	case strings.HasPrefix(low, "loft://"):
		return loftYAML(s)
	case strings.HasPrefix(low, "vless://"):
		return vlessYAML(s)
	case strings.HasPrefix(low, "trojan://"):
		return trojanYAML(s)
	case strings.HasPrefix(low, "sstp://"):
		return sstpYAML(s)
	}
	if y, err := yamlFile(s); err == nil {
		return y, nil
	}
	if y, err := vpnJSON(s); err == nil {
		return y, nil
	}
	return "", fmt.Errorf("unknown share")
}

// loftYAML turns a loft:// link into client YAML. The link looks like
//
//	loft://TOKEN@host[:port][/path]?type=get&fp=chrome&cdn=…&pbk=…&edge=…#name
//
// The user part is the member key (lt_…). Port defaults to 443, fp (the TLS
// fingerprint to imitate) to chrome. cdn names the CDN in front of the
// node, pbk is the node's mask public key (mask is a mode where the node
// shows a certificate shaped like its decoy site's), edge is a CDN origin
// to try first, and scheme forces http/https/ws/wss.
//
// There are two shapes. Old links have one front in type= (default get)
// and its path in the URL path. Newer links list fronts as query keys,
// each with its own path, e.g. ?get=/g&ws=/w&h3; an empty value means the
// default path for that front. Each listed front becomes one outbound; the
// first keeps the name, the others get "-type" appended.
//
// When fronts are listed, Hello offers exactly those, not the full
// wire.FrontOrder: with GET and no fronts set, loftOnly would offer every
// mux front.
func loftYAML(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !strings.EqualFold(u.Scheme, "loft") {
		return "", fmt.Errorf("loft://")
	}
	token := ""
	if u.User != nil {
		token, _ = url.QueryUnescape(u.User.Username())
	}
	host := u.Hostname()
	if token == "" || host == "" {
		return "", fmt.Errorf("loft://")
	}
	port := u.Port()
	if port == "" {
		port = "443"
	}
	path := u.Path
	if path == "/" {
		path = ""
	}
	q := u.Query()
	fp := q.Get("fp")
	if fp == "" {
		fp = "chrome"
	}
	cdn := q.Get("cdn")
	pbk := q.Get("pbk")
	name := u.Fragment
	if name == "" {
		name = host
	}
	scheme := strings.ToLower(q.Get("scheme"))
	portN, _ := strconv.Atoi(port)
	keys := []string{"get", "ws", "h2", "h3", "quic"}
	var listed []string
	for _, k := range keys {
		if _, ok := q[k]; ok {
			listed = append(listed, k)
		}
	}
	var outs []config.Outbound
	if len(listed) > 0 {
		for _, typ := range listed {
			pth := q.Get(typ)
			if pth == "" {
				switch typ {
				case "get":
					pth = config.DefaultGETPath
				case "ws":
					pth = config.DefaultWSPath
				case "h2", "h3":
					pth = config.DefaultH2Path
				}
			}
			ob := loftOutbound(name, typ, host, port, portN, scheme, pth, fp, cdn, pbk)
			if ob.Type == "" {
				return "", fmt.Errorf("loft:// type")
			}
			outs = append(outs, ob)
		}
	} else {
		typ := strings.ToLower(q.Get("type"))
		if typ == "" {
			typ = "get"
		}
		if !loftTypes[typ] {
			return "", fmt.Errorf("loft:// type")
		}
		outs = []config.Outbound{loftOutbound(name, typ, host, port, portN, scheme, path, fp, cdn, pbk)}
	}
	if len(outs) > 1 {
		for i := range outs {
			if i == 0 {
				continue
			}
			outs[i].Name = name + "-" + outs[i].Type
		}
	}
	cfg := &config.ClientConfig{
		Token:     token,
		Select:    "auto",
		Outbounds: outs,
	}
	// Hello the query fronts, not FrontOrder. Empty Fronts + GET would fan every mux.
	if len(listed) > 0 {
		cfg.Fronts = append([]string(nil), listed...)
	}
	if err := config.PinEdgeGET(cfg, q.Get("edge")); err != nil {
		return "", err
	}
	return marshalClient(cfg, "", "", false, nil)
}

// loftOutbound builds one front's outbound. ws uses ws/wss and puts the
// path into the URL; the other fronts use http/https with a separate Path,
// and quic takes no path. Port 443 is left out of https/wss URLs. An
// unknown type gives an empty Outbound.
func loftOutbound(name, typ, host, port string, portN int, scheme, path, fp, cdn, pbk string) config.Outbound {
	if !loftTypes[typ] {
		return config.Outbound{}
	}
	sch := scheme
	if sch == "" {
		if typ == "ws" {
			sch = "wss"
		} else {
			sch = "https"
		}
	}
	if typ == "ws" && sch != "ws" && sch != "wss" {
		sch = "wss"
	}
	if typ != "ws" && sch != "http" && sch != "https" {
		sch = "https"
	}
	omit := portN == 443 && (sch == "https" || sch == "wss")
	hostPart := hostPort(host, port, omit)
	frontURL := sch + "://" + hostPart
	pth := path
	if pth == "/" {
		pth = ""
	}
	switch typ {
	case "ws":
		if pth != "" {
			frontURL += pth
			pth = ""
		}
	case "quic":
		pth = ""
	}
	return config.Outbound{
		Name:        name,
		Type:        typ,
		CDN:         cdn,
		URL:         frontURL,
		Path:        pth,
		Fingerprint: fp,
		MaskPub:     pbk,
	}
}

// vlessYAML keeps a vless:// link whole as the outbound URL (the vless
// package parses it again when dialing); the UUID becomes the token and
// the #fragment the name.
func vlessYAML(raw string) (string, error) {
	ep, err := vless.ParseURL(raw, "")
	if err != nil || ep.UUID == "" {
		return "", fmt.Errorf("vless://")
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", fmt.Errorf("vless://")
	}
	name := u.Fragment
	if name == "" {
		name = u.Hostname()
	}
	sni := ep.SNI
	cfg := &config.ClientConfig{
		Token:  ep.UUID,
		Select: "auto",
		Outbounds: []config.Outbound{{
			Name: name,
			Type: "vless",
			URL:  strings.TrimSpace(raw),
			SNI:  sni,
		}},
	}
	return marshalClient(cfg, "", "", false, nil)
}

// trojanYAML is vlessYAML for trojan://; the password becomes the token.
func trojanYAML(raw string) (string, error) {
	ep, err := trojan.ParseURL(raw, "")
	if err != nil || ep.Password == "" {
		return "", fmt.Errorf("trojan://")
	}
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", fmt.Errorf("trojan://")
	}
	name := u.Fragment
	if name == "" {
		name = u.Hostname()
	}
	cfg := &config.ClientConfig{
		Token:  ep.Password,
		Select: "auto",
		Outbounds: []config.Outbound{{
			Name: name,
			Type: "trojan",
			URL:  strings.TrimSpace(raw),
			SNI:  ep.SNI,
		}},
	}
	return marshalClient(cfg, "", "", false, nil)
}

// sstpYAML turns sstp://user:pass@host[:port][/path]?sni=…&pap=1#name into
// an SSTP outbound with an https URL. By default only MSCHAPv2 login is
// allowed; pap=1 also allows the weaker PAP and CHAP-MD5.
func sstpYAML(raw string) (string, error) {
	u, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || !strings.EqualFold(u.Scheme, "sstp") {
		return "", fmt.Errorf("sstp://")
	}
	user, pass := "", ""
	if u.User != nil {
		user, _ = url.QueryUnescape(u.User.Username())
		p, _ := u.User.Password()
		pass, _ = url.QueryUnescape(p)
	}
	host := u.Hostname()
	if host == "" || pass == "" {
		return "", fmt.Errorf("sstp://")
	}
	port := u.Port()
	if port == "" {
		port = "443"
	}
	omit := port == "443"
	https := "https://" + hostPort(host, port, omit)
	if p := u.Path; p != "" && p != "/" {
		https += p
	}
	name := u.Fragment
	if name == "" {
		name = host
	}
	q := u.Query()
	sni := q.Get("sni")
	pap := q.Get("pap") == "1"
	cfg := &config.ClientConfig{
		Token:  pass,
		Select: "auto",
		Outbounds: []config.Outbound{{
			Name: name,
			Type: "sstp",
			URL:  https,
			SNI:  sni,
		}},
	}
	return marshalClient(cfg, user, pass, pap, nil)
}

// yamlFile accepts a pasted YAML connection and normalises it through
// decodeFile and marshalClient. Any YAML without token, password,
// outbounds or connections is refused, so ordinary text that happens to
// parse as YAML is not taken for a connection.
func yamlFile(s string) (string, error) {
	var probe map[string]any
	if err := yaml.Unmarshal([]byte(s), &probe); err != nil {
		return "", err
	}
	_, tok := probe["token"]
	_, pass := probe["password"]
	_, outs := probe["outbounds"]
	_, cons := probe["connections"]
	if !tok && !pass && !outs && !cons {
		return "", fmt.Errorf("yaml")
	}
	cfg, extra, err := decodeFile([]byte(s))
	if err != nil {
		return "", err
	}
	return marshalClient(cfg, extra.username, extra.password, extra.pap, extra.tlsCheck)
}

// vpnJSON imports a small JSON object {host|server, username|user,
// password, name, sni} as an SSTP connection.
func vpnJSON(s string) (string, error) {
	s = strings.TrimSpace(s)
	if !strings.HasPrefix(s, "{") {
		return "", fmt.Errorf("json")
	}
	var j struct {
		Host     string `json:"host"`
		Server   string `json:"server"`
		Username string `json:"username"`
		User     string `json:"user"`
		Password string `json:"password"`
		Name     string `json:"name"`
		SNI      string `json:"sni"`
	}
	if err := json.Unmarshal([]byte(s), &j); err != nil {
		return "", err
	}
	host := j.Host
	if host == "" {
		host = j.Server
	}
	user := j.Username
	if user == "" {
		user = j.User
	}
	if host == "" || j.Password == "" {
		return "", fmt.Errorf("vpn json")
	}
	name := j.Name
	if name == "" {
		name = host
	}
	url := host
	if !strings.Contains(url, "://") {
		url = "https://" + url
	}
	cfg := &config.ClientConfig{
		Token: j.Password,
		Outbounds: []config.Outbound{{
			Name: name,
			Type: "sstp",
			URL:  url,
			SNI:  j.SNI,
		}},
	}
	return marshalClient(cfg, user, j.Password, false, nil)
}

// hostPort formats host and port for a URL, bracketing IPv6. omit drops
// the port (the default 443).
func hostPort(host, port string, omit bool) string {
	if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	if omit {
		return host
	}
	return net.JoinHostPort(strings.Trim(host, "[]"), port)
}

// unwrapBase64 decodes a base64 (standard or URL-safe, padding optional)
// subscription-style blob. It returns "" unless the result contains a
// "://" link, so random text is not mistaken for base64.
func unwrapBase64(s string) string {
	if strings.Contains(s, "://") {
		return ""
	}
	compact := strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == ' ' {
			return -1
		}
		return r
	}, s)
	if len(compact) < 16 || len(compact) > 8192 {
		return ""
	}
	b64 := strings.NewReplacer("-", "+", "_", "/").Replace(compact)
	for len(b64)%4 != 0 {
		b64 += "="
	}
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return ""
	}
	out := strings.TrimSpace(string(raw))
	if strings.Contains(out, "://") {
		return out
	}
	return ""
}

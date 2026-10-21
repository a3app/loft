package adm

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"

	"github.com/a3app/loft/server/panel/admproto"
)

// Xray channels: one local SOCKS inbound per channel (the node dials it as a
// socks5 channel), one outbound from the channel's link, chained through
// another channel when Via is set: a kernel channel by interface, a proxy
// channel by an extra outbound (sockopt.dialerProxy).

// XrayFile is the config the loft-panel-xray unit loads as a credential.
const XrayFile = "/etc/loft-panel/xray.json"

// XrayUnit runs Xray-core for the channels.
const XrayUnit = "loft-panel-xray.service"

// XrayLink is what a vless://, trojan:// or ss:// link says.
type XrayLink struct {
	Protocol  string `json:"protocol"` // vless | trojan | shadowsocks
	Host      string `json:"host"`
	Port      int    `json:"port"`
	Security  string `json:"security,omitempty"` // none | tls | reality
	Transport string `json:"transport,omitempty"`
	Flow      string `json:"flow,omitempty"`
	Name      string `json:"name,omitempty"`
	out       map[string]any
}

// ParseXrayLink reads a share link into an Xray outbound.
func ParseXrayLink(link string) (XrayLink, error) {
	link = strings.TrimSpace(link)
	scheme, _, ok := strings.Cut(link, "://")
	if !ok || len(link) > 4096 {
		return XrayLink{}, errors.New("link: vless://, trojan:// or ss://")
	}
	switch strings.ToLower(scheme) {
	case "vless", "trojan":
		return parseVlessTrojan(link, strings.ToLower(scheme))
	case "ss":
		return parseSS(link)
	}
	return XrayLink{}, errors.New("link: vless://, trojan:// or ss://")
}

func linkPort(h, p string) (string, int, error) {
	n, err := strconv.Atoi(p)
	if err != nil || n < 1 || n > 65535 || h == "" || strings.ContainsAny(h, " /\"") {
		return "", 0, errors.New("link: no server host:port")
	}
	return h, n, nil
}

func parseVlessTrojan(link, proto string) (XrayLink, error) {
	u, err := url.Parse(link)
	if err != nil || u.User == nil {
		return XrayLink{}, fmt.Errorf("link: %s://ID@host:port?…", proto)
	}
	host, port, err := linkPort(u.Hostname(), u.Port())
	if err != nil {
		return XrayLink{}, err
	}
	q := u.Query()
	l := XrayLink{Protocol: proto, Host: host, Port: port, Security: firstNonEmpty(q.Get("security"), "none"),
		Transport: firstNonEmpty(q.Get("type"), "tcp"), Flow: q.Get("flow"), Name: u.Fragment}
	id := u.User.Username()
	if id == "" {
		return XrayLink{}, errors.New("link: no user id or password")
	}
	var settings map[string]any
	if proto == "vless" {
		user := map[string]any{"id": id, "encryption": firstNonEmpty(q.Get("encryption"), "none")}
		if l.Flow != "" {
			user["flow"] = l.Flow
		}
		settings = map[string]any{"vnext": []any{map[string]any{"address": host, "port": port, "users": []any{user}}}}
	} else {
		settings = map[string]any{"servers": []any{map[string]any{"address": host, "port": port, "password": id}}}
		if l.Security == "none" && q.Get("security") == "" {
			l.Security = "tls" // trojan is TLS unless told otherwise
		}
	}
	stream := map[string]any{"network": l.Transport, "security": l.Security}
	sni := firstNonEmpty(q.Get("sni"), q.Get("peer"))
	fp := q.Get("fp")
	switch l.Security {
	case "tls":
		t := map[string]any{"serverName": firstNonEmpty(sni, host)}
		if fp != "" {
			t["fingerprint"] = fp
		}
		if a := q.Get("alpn"); a != "" {
			t["alpn"] = strings.Split(a, ",")
		}
		if q.Get("allowInsecure") == "1" {
			t["allowInsecure"] = true
		}
		stream["tlsSettings"] = t
	case "reality":
		if q.Get("pbk") == "" {
			return XrayLink{}, errors.New("link: REALITY needs pbk")
		}
		stream["realitySettings"] = map[string]any{"serverName": sni, "fingerprint": firstNonEmpty(fp, "chrome"),
			"publicKey": q.Get("pbk"), "shortId": q.Get("sid"), "spiderX": q.Get("spx")}
	case "none":
	default:
		return XrayLink{}, fmt.Errorf("link: security %q (none, tls, reality)", l.Security)
	}
	switch l.Transport {
	case "tcp", "raw":
		if q.Get("headerType") == "http" {
			stream["tcpSettings"] = map[string]any{"header": map[string]any{"type": "http"}}
		}
	case "ws":
		ws := map[string]any{"path": firstNonEmpty(q.Get("path"), "/")}
		if h := q.Get("host"); h != "" {
			ws["headers"] = map[string]any{"Host": h}
		}
		stream["wsSettings"] = ws
	case "grpc":
		stream["grpcSettings"] = map[string]any{"serviceName": q.Get("serviceName")}
	case "httpupgrade":
		stream["httpupgradeSettings"] = map[string]any{"path": firstNonEmpty(q.Get("path"), "/"), "host": q.Get("host")}
	case "xhttp", "splithttp":
		stream["network"] = "xhttp"
		stream["xhttpSettings"] = map[string]any{"path": firstNonEmpty(q.Get("path"), "/"), "host": q.Get("host"), "mode": firstNonEmpty(q.Get("mode"), "auto")}
	default:
		return XrayLink{}, fmt.Errorf("link: transport %q (tcp, ws, grpc, httpupgrade, xhttp)", l.Transport)
	}
	l.out = map[string]any{"protocol": proto, "settings": settings, "streamSettings": stream}
	return l, nil
}

func b64(s string) ([]byte, error) {
	s = strings.TrimRight(s, "=")
	for _, enc := range []*base64.Encoding{base64.RawURLEncoding, base64.RawStdEncoding} {
		if b, err := enc.DecodeString(s); err == nil {
			return b, nil
		}
	}
	return nil, errors.New("bad base64")
}

func parseSS(link string) (XrayLink, error) {
	rest := strings.TrimPrefix(link[2:], "://")
	name := ""
	if i := strings.IndexByte(rest, '#'); i >= 0 {
		name, _ = url.PathUnescape(rest[i+1:])
		rest = rest[:i]
	}
	if i := strings.IndexByte(rest, '?'); i >= 0 {
		rest = rest[:i] // plugin options are not supported
	}
	var userinfo, hostport string
	if at := strings.LastIndexByte(rest, '@'); at >= 0 {
		userinfo, hostport = rest[:at], rest[at+1:]
		if b, err := b64(userinfo); err == nil && strings.Contains(string(b), ":") {
			userinfo = string(b)
		} else if u, err := url.PathUnescape(userinfo); err == nil {
			userinfo = u
		}
	} else { // ss://base64(method:password@host:port)
		b, err := b64(rest)
		if err != nil {
			return XrayLink{}, errors.New("link: ss://METHOD:PASSWORD@host:port")
		}
		at := strings.LastIndexByte(string(b), '@')
		if at < 0 {
			return XrayLink{}, errors.New("link: ss://METHOD:PASSWORD@host:port")
		}
		userinfo, hostport = string(b[:at]), string(b[at+1:])
	}
	method, pass, ok := strings.Cut(userinfo, ":")
	h, p, err := net.SplitHostPort(hostport)
	if !ok || err != nil {
		return XrayLink{}, errors.New("link: ss://METHOD:PASSWORD@host:port")
	}
	host, port, err := linkPort(h, p)
	if err != nil {
		return XrayLink{}, err
	}
	l := XrayLink{Protocol: "shadowsocks", Host: host, Port: port, Security: "none", Transport: "tcp", Name: name}
	l.out = map[string]any{"protocol": "shadowsocks", "settings": map[string]any{"servers": []any{
		map[string]any{"address": host, "port": port, "method": method, "password": pass}}}}
	return l, nil
}

// XrayConfig is the whole config for the xr channels in s; nil when there are none.
func XrayConfig(s chanStore) ([]byte, error) {
	var in, out, rules []any
	extra := map[string]bool{}
	for _, c := range s.Channels {
		if c.Type != admproto.ChanXR || !c.On {
			continue
		}
		l, err := ParseXrayLink(c.XR.Link)
		if err != nil {
			return nil, fmt.Errorf("%s: %v", c.Name, err)
		}
		ob := l.out
		ob["tag"] = "out-" + c.Name
		if c.Via != "" {
			v := s.Channels[find(s, c.Via)]
			stream, _ := ob["streamSettings"].(map[string]any)
			if stream == nil {
				stream = map[string]any{}
				ob["streamSettings"] = stream
			}
			switch v.Type {
			case admproto.ChanWG, admproto.ChanOVPN, admproto.ChanZT:
				stream["sockopt"] = map[string]any{"interface": v.Dev}
			case admproto.ChanPX:
				stream["sockopt"] = map[string]any{"dialerProxy": "via-" + v.Name}
				if !extra[v.Name] {
					extra[v.Name] = true
					out = append(out, proxyOutbound(v))
				}
			default:
				return nil, fmt.Errorf("%s: Xray connects through a WireGuard, OpenVPN, ZeroTier or proxy channel", c.Name)
			}
		}
		in = append(in, map[string]any{"tag": "in-" + c.Name, "protocol": "socks", "listen": "127.0.0.1", "port": c.XR.Port,
			"settings": map[string]any{"auth": "noauth", "udp": true, "ip": "127.0.0.1"}})
		out = append(out, ob)
		rules = append(rules, map[string]any{"type": "field", "inboundTag": []string{"in-" + c.Name}, "outboundTag": "out-" + c.Name})
	}
	if len(in) == 0 {
		return nil, nil
	}
	out = append(out, map[string]any{"tag": "block", "protocol": "blackhole"})
	return json.MarshalIndent(map[string]any{
		"log":       map[string]any{"loglevel": "warning"},
		"inbounds":  in,
		"outbounds": out,
		"routing":   map[string]any{"rules": rules},
	}, "", "  ")
}

func proxyOutbound(v admproto.Channel) map[string]any {
	host, p, _ := net.SplitHostPort(v.PX.Addr)
	port, _ := strconv.Atoi(p)
	srv := map[string]any{"address": host, "port": port}
	if v.PX.User != "" {
		srv["users"] = []any{map[string]any{"user": v.PX.User, "pass": v.PX.Password}}
	}
	ob := map[string]any{"tag": "via-" + v.Name, "protocol": "socks", "settings": map[string]any{"servers": []any{srv}}}
	if v.PX.Kind != "socks5" {
		ob["protocol"] = "http"
		if v.PX.Kind == "https" {
			ob["streamSettings"] = map[string]any{"security": "tls", "tlsSettings": map[string]any{"serverName": host}}
		}
	}
	return ob
}

// writeXray writes the config and starts, restarts or stops the unit.
func (o *Ops) writeXray(ctx context.Context, s chanStore) error {
	conf, err := XrayConfig(s)
	if err != nil {
		return err
	}
	path := XrayFile
	if o.PanelEtc != "" {
		path = o.PanelEtc + "/xray.json"
	}
	if conf == nil {
		os.Remove(path)
		o.Systemctl(ctx, "stop", XrayUnit)
		return nil
	}
	old, _ := os.ReadFile(path)
	if string(old) == string(conf) {
		return nil
	}
	if err := storeWrite(path, conf, 0o600); err != nil {
		return err
	}
	if out, err := o.Systemctl(ctx, "restart", XrayUnit); err != nil {
		return fmt.Errorf("Xray did not start (is the Xray client package installed?): %v %s", err, out)
	}
	return nil
}

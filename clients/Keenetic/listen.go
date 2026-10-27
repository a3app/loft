package keenetic

import (
	"net"
	"strings"
)

// ListenAddrs is where the panel binds. 0.0.0.0 and an empty host mean
// 127.0.0.1 plus each private address the caller found (LAN, not WAN, not
// OpkgTun). A specific host is used as given, plus loopback when it differs.
func ListenAddrs(field string, private []string) []string {
	host, port := splitListen(field)
	if port == "" {
		port = "8090"
	}
	add := func(h string, out []string) []string {
		if h == "" {
			return out
		}
		s := net.JoinHostPort(h, port)
		for _, e := range out {
			if e == s {
				return out
			}
		}
		return append(out, s)
	}
	var out []string
	switch host {
	case "", "0.0.0.0", "::", "[::]":
		out = add("127.0.0.1", out)
		for _, p := range private {
			out = add(p, out)
		}
	case "127.0.0.1", "localhost", "::1":
		out = add(host, out)
	default:
		out = add(host, out)
		out = add("127.0.0.1", out)
	}
	return out
}

func splitListen(field string) (host, port string) {
	field = strings.TrimSpace(field)
	if field == "" {
		return "", "8090"
	}
	if strings.HasPrefix(field, ":") {
		return "", strings.TrimPrefix(field, ":")
	}
	h, p, err := net.SplitHostPort(field)
	if err != nil {
		return field, "8090"
	}
	return h, p
}

// PrivateIPv4 lists global-unicast private addresses, skipping tun devices
// and the 198.18.0.0/15 range used as a stand-in overlay for VLESS/Trojan.
func PrivateIPv4() []string {
	ifs, err := net.Interfaces()
	if err != nil {
		return nil
	}
	var out []string
	seen := map[string]bool{}
	for _, ifi := range ifs {
		name := strings.ToLower(ifi.Name)
		if strings.HasPrefix(name, "opkgtun") || strings.HasPrefix(name, "tun") || strings.HasPrefix(name, "utun") {
			continue
		}
		addrs, err := ifi.Addrs()
		if err != nil {
			continue
		}
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok {
				continue
			}
			ip := ipn.IP.To4()
			if ip == nil || !ip.IsPrivate() {
				continue
			}
			if ip[0] == 198 && (ip[1] == 18 || ip[1] == 19) {
				continue
			}
			s := ip.String()
			if seen[s] {
				continue
			}
			seen[s] = true
			out = append(out, s)
		}
	}
	return out
}

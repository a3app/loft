package netconf

// Cisco-style extended ACL parse and render for overlay Rule lines.
// permit/accept and deny/drop; proto ip/any is a wildcard.

import (
	"fmt"
	"net"
	"strconv"
	"strings"

	"github.com/a3app/loft/internal/startup"
)

// ParseACLLine reads one Cisco extended ACL line into a Rule.
// permit/accept → accept, deny/drop → drop.
// Proto ip/any is a wildcard. eq and range apply to dest ports; range width ≤ 64.
// vlan SRC [DST] is overlay VLAN ids, not 802.1Q on the wire.
func ParseACLLine(tok []string) (Rule, error) {
	var r Rule
	if len(tok) < 2 {
		return r, fmt.Errorf("acl: want permit|deny PROTO SRC DST")
	}
	switch startup.Word(tok[0]) {
	case "permit", "accept":
		r.Action = "accept"
	case "deny", "drop":
		r.Action = "drop"
	default:
		return r, fmt.Errorf("acl: want permit or deny")
	}
	i := 1
	proto := startup.Word(tok[i])
	switch proto {
	case "ip", "any":
		r.Proto = ""
		i++
	case "tcp", "udp", "icmp":
		r.Proto = proto
		i++
	default:
		return r, fmt.Errorf("acl: unknown proto %q", tok[i])
	}
	if i >= len(tok) {
		return r, fmt.Errorf("acl: missing source")
	}
	src, n, err := takeAddr(tok[i:])
	if err != nil {
		return r, err
	}
	r.Src = src.CIDR
	r.SrcTag = src.Tag
	i += n
	if i >= len(tok) {
		return r, fmt.Errorf("acl: missing destination")
	}
	dst, n, err := takeAddr(tok[i:])
	if err != nil {
		return r, err
	}
	r.Dst = dst.CIDR
	r.DstTag = dst.Tag
	i += n
	for i < len(tok) {
		switch startup.Word(tok[i]) {
		case "eq":
			i++
			for i < len(tok) {
				w := startup.Word(tok[i])
				if w == "vlan" || w == "range" || w == "eq" {
					break
				}
				p, err := strconv.Atoi(tok[i])
				if err != nil || p <= 0 || p > 65535 {
					return r, fmt.Errorf("acl: bad port %q", tok[i])
				}
				r.Dport = append(r.Dport, p)
				i++
			}
		case "range":
			if i+2 >= len(tok) {
				return r, fmt.Errorf("acl: range wants two ports")
			}
			a, err1 := strconv.Atoi(tok[i+1])
			b, err2 := strconv.Atoi(tok[i+2])
			if err1 != nil || err2 != nil || a <= 0 || b <= 0 || a > 65535 || b > 65535 || b < a {
				return r, fmt.Errorf("acl: bad range")
			}
			if b-a > 64 {
				return r, fmt.Errorf("acl: range too wide")
			}
			for p := a; p <= b; p++ {
				r.Dport = append(r.Dport, p)
			}
			i += 3
		case "vlan":
			if i+1 >= len(tok) {
				return r, fmt.Errorf("acl: vlan wants an id")
			}
			sv, err := strconv.Atoi(tok[i+1])
			if err != nil || sv <= 0 {
				return r, fmt.Errorf("acl: bad vlan")
			}
			r.SrcVLAN = &sv
			i += 2
			if i < len(tok) {
				if dv, err := strconv.Atoi(tok[i]); err == nil && dv > 0 {
					r.DstVLAN = &dv
					i++
				}
			}
		default:
			return r, fmt.Errorf("acl: unexpected %q", tok[i])
		}
	}
	return r, nil
}

// aclAddr is one ACL address: CIDR/host, a member tag, or any (both empty).
type aclAddr struct {
	CIDR string
	Tag  string
}

// takeAddr reads any (wildcard), host <IP>, tag|group NAME, a CIDR, or a bare IP.
func takeAddr(tok []string) (aclAddr, int, error) {
	var a aclAddr
	if len(tok) == 0 {
		return a, 0, fmt.Errorf("acl: missing address")
	}
	w := startup.Word(tok[0])
	if w == "any" {
		return a, 1, nil
	}
	if w == "host" {
		if len(tok) < 2 {
			return a, 0, fmt.Errorf("acl: host wants an address")
		}
		if net.ParseIP(tok[1]) == nil {
			return a, 0, fmt.Errorf("acl: bad host %q", tok[1])
		}
		a.CIDR = tok[1]
		return a, 2, nil
	}
	if w == "tag" || w == "group" {
		if len(tok) < 2 {
			return a, 0, fmt.Errorf("acl: tag wants a name")
		}
		if !ValidTag(tok[1]) {
			return a, 0, fmt.Errorf("acl: bad tag %q", tok[1])
		}
		a.Tag = strings.ToLower(tok[1])
		return a, 2, nil
	}
	if _, _, err := net.ParseCIDR(tok[0]); err == nil {
		a.CIDR = tok[0]
		return a, 1, nil
	}
	if net.ParseIP(tok[0]) != nil {
		a.CIDR = tok[0]
		return a, 1, nil
	}
	return a, 0, fmt.Errorf("acl: bad address %q", tok[0])
}

// RenderACLLine writes a Cisco extended ACL line.
// drop/deny → deny, empty proto → ip, empty addr → any; dest ports as eq.
func RenderACLLine(r Rule) string {
	act := "permit"
	if r.Action == "drop" || r.Action == "deny" {
		act = "deny"
	}
	proto := r.Proto
	if proto == "" {
		proto = "ip"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "%s %s %s %s", act, proto, renderACLAddr(r.Src, r.SrcTag), renderACLAddr(r.Dst, r.DstTag))
	if len(r.Dport) > 0 {
		b.WriteString(" eq")
		for _, p := range r.Dport {
			fmt.Fprintf(&b, " %d", p)
		}
	}
	if r.SrcVLAN != nil {
		fmt.Fprintf(&b, " vlan %d", *r.SrcVLAN)
		if r.DstVLAN != nil {
			fmt.Fprintf(&b, " %d", *r.DstVLAN)
		}
	}
	return b.String()
}

// renderACLAddr writes tag NAME, any, a CIDR, or host <IP> for RenderACLLine.
func renderACLAddr(cidr, tag string) string {
	if tag != "" {
		return "tag " + tag
	}
	return renderAddr(cidr)
}

// renderAddr writes any, a CIDR, or host <IP> for RenderACLLine.
func renderAddr(s string) string {
	if s == "" || s == "any" {
		return "any"
	}
	if strings.Contains(s, "/") {
		return s
	}
	return "host " + s
}

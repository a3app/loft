package netconf

// Switch-style network.conf parse and render (Keenetic / D-Link commands).
// Indented lines belong to ipif overlay or ip access-list overlay.

import (
	"fmt"
	"strconv"
	"strings"

	"github.com/a3app/loft/internal/startup"
)

// ParseCommands reads overlay config as switch-style commands.
// Defaults Enabled, MTU 1280, PeersHub. remark lines under ACL are skipped.
func ParseCommands(body string) (*Network, error) {
	n := &Network{Enabled: true, MTU: 1280, Peers: PeersHub}
	dnsOn := true
	ctx := ""
	order := []string{}
	mem := map[string]*Member{}
	for _, ln := range startup.Lines(body) {
		if len(ln.Tok) == 0 {
			continue
		}
		if ln.Indent == 0 {
			ctx = ""
		}
		var err error
		switch {
		case ctx == "ipif" && ln.Indent > 0:
			err = applyIPIF(n, &dnsOn, ln.Tok)
		case ctx == "acl" && ln.Indent > 0:
			if startup.Word(ln.Tok[0]) == "remark" {
				break
			}
			if startup.Word(ln.Tok[0]) == "default" {
				err = applyACLDefault(n, ln.Tok)
				break
			}
			var r Rule
			r, err = ParseACLLine(ln.Tok)
			if err == nil {
				n.Rules = append(n.Rules, r)
			}
		case ln.Indent > 0:
			err = fmt.Errorf("unexpected indent")
		default:
			err = applyNet(n, mem, &order, &ctx, &dnsOn, ln.Tok)
		}
		if err != nil {
			return nil, fmt.Errorf("line %d: %w", ln.N, err)
		}
	}
	for _, id := range order {
		n.Members = append(n.Members, *mem[id])
	}
	if !dnsOn {
		f := false
		n.DNS.Enabled = &f
	} else if len(n.DNS.Servers) > 0 || len(n.DNS.Upstream) > 0 {
		t := true
		n.DNS.Enabled = &t
	}
	if err := n.Validate(); err != nil {
		return nil, err
	}
	return n, nil
}

// applyNet handles a top-level command and may set ctx to ipif or acl.
func applyNet(n *Network, mem map[string]*Member, order *[]string, ctx *string, dnsOn *bool, tok []string) error {
	cmd := startup.Word(tok[0])
	switch cmd {
	case "network":
		if len(tok) < 2 {
			return fmt.Errorf("network wants an id")
		}
		n.ID = tok[1]
	case "name":
		if len(tok) < 2 {
			return fmt.Errorf("name wants a value")
		}
		n.Name = strings.Join(tok[1:], " ")
	case "enable":
		if len(tok) == 1 {
			n.Enabled = true
			return nil
		}
		return applyEnable(n, mem, order, tok)
	case "disable":
		if len(tok) == 1 {
			n.Enabled = false
			return nil
		}
		return applyEnable(n, mem, order, tok)
	case "ipif":
		if len(tok) < 3 || startup.Word(tok[1]) != "overlay" {
			return fmt.Errorf("want ipif overlay CIDR")
		}
		n.CIDR = tok[2]
		*ctx = "ipif"
	case "create":
		return applyCreate(n, mem, order, tok)
	case "config":
		return applyConfig(n, mem, order, tok)
	case "ip":
		return applyIP(n, ctx, tok)
	case "session":
		return applySession(&n.Session, tok)
	default:
		return fmt.Errorf("unknown command %q", tok[0])
	}
	return nil
}

// applyEnable sets member Authorized from enable|disable member ID.
func applyEnable(n *Network, mem map[string]*Member, order *[]string, tok []string) error {
	on := startup.Word(tok[0]) == "enable"
	if len(tok) < 3 || startup.Word(tok[1]) != "member" {
		return fmt.Errorf("want enable|disable member ID")
	}
	m := ensureMember(mem, order, tok[2])
	m.Authorized = on
	return nil
}

// applyCreate is create vlan ID [name …] or create member ID.
func applyCreate(n *Network, mem map[string]*Member, order *[]string, tok []string) error {
	if len(tok) < 3 {
		return fmt.Errorf("create wants a kind and an id")
	}
	switch startup.Word(tok[1]) {
	case "vlan":
		id, err := strconv.Atoi(tok[2])
		if err != nil || id <= 0 {
			return fmt.Errorf("bad vlan id")
		}
		v := VLAN{ID: id}
		if len(tok) >= 5 && startup.Word(tok[3]) == "name" {
			v.Name = strings.Join(tok[4:], " ")
		}
		n.VLANs = append(n.VLANs, v)
	case "member":
		ensureMember(mem, order, tok[2])
	default:
		return fmt.Errorf("create %s: unknown", tok[1])
	}
	return nil
}

// applyConfig is config vlan|member ID KEY VALUE. Unknown vlan ids are created.
func applyConfig(n *Network, mem map[string]*Member, order *[]string, tok []string) error {
	if len(tok) < 4 {
		return fmt.Errorf("config wants an object")
	}
	switch startup.Word(tok[1]) {
	case "vlan":
		id, err := strconv.Atoi(tok[2])
		if err != nil {
			return fmt.Errorf("bad vlan id")
		}
		v := n.VLANByID(id)
		if v == nil {
			n.VLANs = append(n.VLANs, VLAN{ID: id})
			v = n.VLANByID(id)
		}
		switch startup.Word(tok[3]) {
		case "name":
			v.Name = strings.Join(tok[4:], " ")
		case "cidr":
			if len(tok) < 5 {
				return fmt.Errorf("vlan cidr wants a network")
			}
			v.CIDR = tok[4]
		case "gateway":
			if len(tok) < 5 {
				return fmt.Errorf("vlan gateway wants an IP")
			}
			v.Gateway = tok[4]
		case "pool":
			if len(tok) < 6 {
				return fmt.Errorf("vlan pool wants FROM TO")
			}
			v.PoolFrom, v.PoolTo = tok[4], tok[5]
		case "dns":
			if len(tok) < 5 {
				return fmt.Errorf("vlan dns wants a value")
			}
			if on, ok := startup.OnOff(tok[4]); ok {
				v.DNS.Enabled = &on
				return nil
			}
			dummy := true
			return applyDNS(&v.DNS, &dummy, tok[3:])
		case "peers":
			if len(tok) < 5 {
				return fmt.Errorf("vlan peers wants hub|mesh")
			}
			v.Peers = startup.Word(tok[4])
		default:
			return fmt.Errorf("config vlan: unknown %q", tok[3])
		}
	case "member":
		m := ensureMember(mem, order, tok[2])
		switch startup.Word(tok[3]) {
		case "name":
			m.Name = strings.Join(tok[4:], " ")
		case "ipaddress", "ip":
			if len(tok) < 5 {
				return fmt.Errorf("member ipaddress wants an IP")
			}
			m.IP = tok[4]
		case "vlan":
			if len(tok) < 5 {
				return fmt.Errorf("member vlan wants an id")
			}
			m.VLAN, _ = strconv.Atoi(tok[4])
		case "key_sha256":
			if len(tok) < 5 {
				return fmt.Errorf("member key_sha256 wants a hash")
			}
			m.KeySHA256 = tok[4]
		case "pubkey":
			if len(tok) < 5 {
				return fmt.Errorf("member pubkey wants a key")
			}
			m.PubKey = tok[4]
		case "multi":
			if len(tok) < 5 {
				return fmt.Errorf("member multi wants enable|disable")
			}
			on, ok := startup.OnOff(tok[4])
			if !ok {
				return fmt.Errorf("member multi: want enable|disable")
			}
			m.Multi = on
			if on && len(tok) >= 6 {
				m.MultiLimit, _ = strconv.Atoi(tok[5])
			}
		case "tags":
			m.Tags = append([]string(nil), tok[4:]...)
		case "rate_bps":
			if len(tok) < 5 {
				return fmt.Errorf("member rate_bps wants a number")
			}
			v, err := strconv.Atoi(tok[4])
			if err != nil || v < 0 {
				return fmt.Errorf("member rate_bps: bad number")
			}
			m.Session.RateBPS = v
		default:
			return fmt.Errorf("config member: unknown %q", tok[3])
		}
	default:
		return fmt.Errorf("config %s: unknown", tok[1])
	}
	return nil
}

// applyACLDefault is "default deny|drop|permit|accept" under ip access-list overlay.
func applyACLDefault(n *Network, tok []string) error {
	if len(tok) < 2 {
		return fmt.Errorf("acl default wants deny or permit")
	}
	switch startup.Word(tok[1]) {
	case "deny", "drop":
		n.ACLDefault = "drop"
	case "permit", "accept":
		n.ACLDefault = ""
	default:
		return fmt.Errorf("acl default: want deny or permit")
	}
	return nil
}

// applyIP is ip route DST [VIA|member ID|tag NAME] [description …] or ip access-list (ctx acl).
func applyIP(n *Network, ctx *string, tok []string) error {
	if len(tok) < 2 {
		return fmt.Errorf("ip wants route or access-list")
	}
	switch startup.Word(tok[1]) {
	case "route":
		if len(tok) < 3 {
			return fmt.Errorf("ip route wants a destination")
		}
		r := Route{Dst: tok[2]}
		if len(tok) >= 4 && startup.Word(tok[3]) != "description" {
			switch startup.Word(tok[3]) {
			case "member":
				if len(tok) < 5 {
					return fmt.Errorf("ip route member wants an id")
				}
				r.Member = tok[4]
			case "tag":
				if len(tok) < 5 {
					return fmt.Errorf("ip route tag wants a name")
				}
				r.Tag = tok[4]
			default:
				r.Via = tok[3]
			}
		}
		for i, t := range tok {
			if startup.Word(t) == "description" {
				r.Note = strings.Join(tok[i+1:], " ")
				break
			}
		}
		n.Routes = append(n.Routes, r)
	case "access-list":
		*ctx = "acl"
	default:
		return fmt.Errorf("ip %s: unknown", tok[1])
	}
	return nil
}

// applyIPIF is an indented line under ipif overlay: gateway, pool, mtu, peers, p2p, dns, listen.
func applyIPIF(n *Network, dnsOn *bool, tok []string) error {
	switch startup.Word(tok[0]) {
	case "gateway":
		if len(tok) < 2 {
			return fmt.Errorf("gateway wants an IP")
		}
		n.Gateway = tok[1]
	case "pool":
		if len(tok) < 3 {
			return fmt.Errorf("pool wants FROM TO")
		}
		n.PoolFrom, n.PoolTo = tok[1], tok[2]
	case "mtu":
		if len(tok) < 2 {
			return fmt.Errorf("mtu wants a number")
		}
		mtu, err := strconv.Atoi(tok[1])
		if err != nil || mtu <= 0 {
			return fmt.Errorf("bad mtu")
		}
		n.MTU = mtu
	case "peers":
		if len(tok) < 2 {
			return fmt.Errorf("peers wants hub|mesh|vlan")
		}
		n.Peers = startup.Word(tok[1])
	case "p2p":
		if len(tok) < 2 {
			return fmt.Errorf("p2p wants enable|disable")
		}
		on, ok := startup.OnOff(tok[1])
		if !ok {
			return fmt.Errorf("p2p: want enable|disable")
		}
		n.P2P = &on
	case "dns":
		return applyDNS(&n.DNS, dnsOn, tok)
	case "listen":
		l, err := parseListen(tok)
		if err != nil {
			return err
		}
		n.Listen = append(n.Listen, l)
	default:
		return fmt.Errorf("ipif: unknown %q", tok[0])
	}
	return nil
}

// applyDNS is dns enable|disable, dns upstream URL, or dns SERVER.
func applyDNS(d *DNS, dnsOn *bool, tok []string) error {
	if len(tok) < 2 {
		return fmt.Errorf("dns wants a value")
	}
	if on, ok := startup.OnOff(tok[1]); ok {
		*dnsOn = on
		return nil
	}
	if startup.Word(tok[1]) == "upstream" {
		if len(tok) < 3 {
			return fmt.Errorf("dns upstream wants a URL")
		}
		d.Upstream = append(d.Upstream, tok[2])
		return nil
	}
	d.Servers = append(d.Servers, tok[1])
	return nil
}

// parseListen is listen IP PORT [front NAME] [domain NAME] [sni NAME].
func parseListen(tok []string) (Listen, error) {
	var l Listen
	if len(tok) < 3 {
		return l, fmt.Errorf("listen wants IP PORT")
	}
	l.IP, l.Port = tok[1], atoi(tok[2])
	if l.Port <= 0 {
		return l, fmt.Errorf("listen: bad port")
	}
	for i := 3; i < len(tok); i++ {
		switch startup.Word(tok[i]) {
		case "front":
			i++
			if i >= len(tok) {
				return l, fmt.Errorf("listen front wants a name")
			}
			l.Front = tok[i]
		case "domain":
			i++
			if i >= len(tok) {
				return l, fmt.Errorf("listen domain wants a name")
			}
			l.Domain = tok[i]
		case "sni":
			i++
			if i >= len(tok) {
				return l, fmt.Errorf("listen sni wants a name")
			}
			l.SNI = tok[i]
		default:
			return l, fmt.Errorf("listen: unexpected %q", tok[i])
		}
	}
	return l, nil
}

// applySession is session KEY MS for SessionPolicy fields (handshake_ms, idle_ms, …).
func applySession(s *SessionPolicy, tok []string) error {
	if len(tok) < 3 {
		return fmt.Errorf("session wants KEY VALUE")
	}
	v, err := strconv.Atoi(tok[2])
	if err != nil || v < 0 {
		return fmt.Errorf("session: bad number")
	}
	switch startup.Word(tok[1]) {
	case "handshake_ms":
		s.HandshakeMS = v
	case "idle_ms":
		s.IdleMS = v
	case "keepalive_ms":
		s.KeepaliveMS = v
	case "lease_hold_ms":
		s.LeaseHoldMS = v
	case "reconnect_min_ms":
		s.ReconnectMinMS = v
	case "reconnect_max_ms":
		s.ReconnectMaxMS = v
	case "reconnect_fast_n":
		s.ReconnectFastN = v
	case "reconnect_fast_for_ms":
		s.ReconnectFastForMS = v
	case "gap_hold_ms":
		s.GapHoldMS = v
	case "ttl_ms":
		s.TTLMS = v
	case "rate_bps":
		s.RateBPS = v
	default:
		return fmt.Errorf("session: unknown %q", tok[1])
	}
	return nil
}

// ensureMember inserts a member on first mention, preserving config order.
func ensureMember(mem map[string]*Member, order *[]string, id string) *Member {
	if m := mem[id]; m != nil {
		return m
	}
	m := &Member{ID: id, Name: id}
	mem[id] = m
	*order = append(*order, id)
	return m
}

// atoi parses a port or count, treating garbage as 0.
func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}

// RenderCommands writes overlay config as switch-style commands.
// Empty ID is loft; MTU ≤ 0 becomes 1280; empty Peers is hub.
func RenderCommands(n Network) string {
	if n.ID == "" {
		n.ID = "loft"
	}
	if n.MTU <= 0 {
		n.MTU = 1280
	}
	if n.Peers == "" {
		n.Peers = PeersHub
	}
	var b strings.Builder
	fmt.Fprintf(&b, "network %s\n", n.ID)
	if n.Name != "" && n.Name != n.ID {
		fmt.Fprintf(&b, "name %s\n", n.Name)
	}
	if n.Enabled {
		b.WriteString("enable\n")
	} else {
		b.WriteString("disable\n")
	}
	fmt.Fprintf(&b, "ipif overlay %s\n", n.CIDR)
	if n.Gateway != "" {
		fmt.Fprintf(&b, "  gateway %s\n", n.Gateway)
	}
	if n.PoolFrom != "" && n.PoolTo != "" {
		fmt.Fprintf(&b, "  pool %s %s\n", n.PoolFrom, n.PoolTo)
	}
	fmt.Fprintf(&b, "  mtu %d\n", n.MTU)
	fmt.Fprintf(&b, "  peers %s\n", n.Peers)
	if n.P2P != nil {
		fmt.Fprintf(&b, "  p2p %s\n", onOff(*n.P2P))
	}
	if n.DNS.Enabled != nil {
		fmt.Fprintf(&b, "  dns %s\n", onOff(*n.DNS.Enabled))
	}
	for _, s := range n.DNS.Servers {
		fmt.Fprintf(&b, "  dns %s\n", s)
	}
	for _, s := range n.DNS.Upstream {
		fmt.Fprintf(&b, "  dns upstream %s\n", s)
	}
	for _, l := range n.Listen {
		fmt.Fprintf(&b, "  listen %s %d", emptyIP(l.IP), l.Port)
		if l.Front != "" {
			fmt.Fprintf(&b, " front %s", l.Front)
		}
		if l.Domain != "" {
			fmt.Fprintf(&b, " domain %s", l.Domain)
		}
		if l.SNI != "" {
			fmt.Fprintf(&b, " sni %s", l.SNI)
		}
		b.WriteByte('\n')
	}
	for _, v := range n.VLANs {
		if v.Name != "" {
			fmt.Fprintf(&b, "create vlan %d name %s\n", v.ID, v.Name)
		} else {
			fmt.Fprintf(&b, "create vlan %d\n", v.ID)
		}
		if v.CIDR != "" {
			fmt.Fprintf(&b, "config vlan %d cidr %s\n", v.ID, v.CIDR)
		}
		if v.Gateway != "" {
			fmt.Fprintf(&b, "config vlan %d gateway %s\n", v.ID, v.Gateway)
		}
		if v.PoolFrom != "" && v.PoolTo != "" {
			fmt.Fprintf(&b, "config vlan %d pool %s %s\n", v.ID, v.PoolFrom, v.PoolTo)
		}
		if v.DNS.Enabled != nil {
			fmt.Fprintf(&b, "config vlan %d dns %s\n", v.ID, onOff(*v.DNS.Enabled))
		}
		for _, s := range v.DNS.Servers {
			fmt.Fprintf(&b, "config vlan %d dns %s\n", v.ID, s)
		}
		for _, s := range v.DNS.Upstream {
			fmt.Fprintf(&b, "config vlan %d dns upstream %s\n", v.ID, s)
		}
		if v.Peers != "" {
			fmt.Fprintf(&b, "config vlan %d peers %s\n", v.ID, v.Peers)
		}
	}
	for _, m := range n.Members {
		fmt.Fprintf(&b, "create member %s\n", m.ID)
		if m.Name != "" && m.Name != m.ID {
			fmt.Fprintf(&b, "config member %s name %s\n", m.ID, m.Name)
		}
		if m.IP != "" {
			fmt.Fprintf(&b, "config member %s ipaddress %s\n", m.ID, m.IP)
		}
		if m.VLAN != 0 {
			fmt.Fprintf(&b, "config member %s vlan %d\n", m.ID, m.VLAN)
		}
		if m.KeySHA256 != "" {
			fmt.Fprintf(&b, "config member %s key_sha256 %s\n", m.ID, m.KeySHA256)
		}
		if m.PubKey != "" {
			fmt.Fprintf(&b, "config member %s pubkey %s\n", m.ID, m.PubKey)
		}
		if m.Multi {
			if m.MultiLimit > 0 {
				fmt.Fprintf(&b, "config member %s multi enable %d\n", m.ID, m.MultiLimit)
			} else {
				fmt.Fprintf(&b, "config member %s multi enable\n", m.ID)
			}
		}
		if len(m.Tags) > 0 {
			fmt.Fprintf(&b, "config member %s tags %s\n", m.ID, strings.Join(m.Tags, " "))
		}
		if m.Session.RateBPS != 0 {
			fmt.Fprintf(&b, "config member %s rate_bps %d\n", m.ID, m.Session.RateBPS)
		}
		if m.Authorized {
			fmt.Fprintf(&b, "enable member %s\n", m.ID)
		} else {
			fmt.Fprintf(&b, "disable member %s\n", m.ID)
		}
	}
	for _, r := range n.Routes {
		fmt.Fprintf(&b, "ip route %s", r.Dst)
		switch {
		case r.Member != "":
			fmt.Fprintf(&b, " member %s", r.Member)
		case r.Tag != "":
			fmt.Fprintf(&b, " tag %s", r.Tag)
		case r.Via != "":
			fmt.Fprintf(&b, " %s", r.Via)
		}
		if r.Note != "" {
			fmt.Fprintf(&b, " description %s", r.Note)
		}
		b.WriteByte('\n')
	}
	if len(n.Rules) > 0 || n.ACLDefault == "drop" {
		b.WriteString("ip access-list overlay\n")
		if n.ACLDefault == "drop" {
			b.WriteString("  default deny\n")
		}
		for _, r := range n.Rules {
			fmt.Fprintf(&b, "  %s\n", RenderACLLine(r))
		}
	}
	s := n.Session
	writeSess := func(k string, v int) {
		if v != 0 {
			fmt.Fprintf(&b, "session %s %d\n", k, v)
		}
	}
	writeSess("handshake_ms", s.HandshakeMS)
	writeSess("idle_ms", s.IdleMS)
	writeSess("keepalive_ms", s.KeepaliveMS)
	writeSess("lease_hold_ms", s.LeaseHoldMS)
	writeSess("reconnect_min_ms", s.ReconnectMinMS)
	writeSess("reconnect_max_ms", s.ReconnectMaxMS)
	writeSess("reconnect_fast_n", s.ReconnectFastN)
	writeSess("reconnect_fast_for_ms", s.ReconnectFastForMS)
	writeSess("gap_hold_ms", s.GapHoldMS)
	writeSess("ttl_ms", s.TTLMS)
	writeSess("rate_bps", s.RateBPS)
	return b.String()
}

// onOff writes enable or disable for RenderCommands.
func onOff(v bool) string {
	if v {
		return "enable"
	}
	return "disable"
}

// emptyIP writes 0.0.0.0 when listen IP is omitted.
func emptyIP(s string) string {
	if s == "" {
		return "0.0.0.0"
	}
	return s
}

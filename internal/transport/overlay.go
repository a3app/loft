package transport

// Overlay switch: leases, hub/mesh/vlan Forward, loft.peer announce, DNS, ICMP.
//
// The overlay is the members' private network. Each connected member
// session gets an overlay IP (a lease from network.conf), and the node acts
// as the switch between them: a stream or packet aimed at another member's
// overlay IP is passed to that member's session instead of the internet.
// The peers mode in network.conf decides who may reach whom: hub, mesh, or
// vlan (only members of the same VLAN). On top of that, ACL rules (accept /
// drop by address, tag, VLAN, protocol and port) are checked on every Open
// and packet. Routes let a member publish a whole subnet behind it (for
// example a lab LAN); traffic to that subnet goes through that member.

import (
	"io"
	"net"
	"sync"
	"sync/atomic"
	"time"

	"github.com/a3app/loft/internal/mux"
	"github.com/a3app/loft/internal/netconf"
	"github.com/a3app/loft/internal/wire"
)

// netSwitch is the overlay fabric (nil = internet-only proxy).
// atomic so test cleanup and leftover serveMux.Detach do not race.
var netSwitch atomic.Pointer[Overlay]

// NetSwitch returns the overlay fabric. Nil means internet-only proxy.
func NetSwitch() *Overlay {
	return netSwitch.Load()
}

// SetNetSwitch installs the overlay fabric. Tests restore the previous value.
func SetNetSwitch(o *Overlay) {
	netSwitch.Store(o)
}

// Overlay is the node's member fabric: overlay CIDR, leases, and live sessions.
type Overlay struct {
	cur  atomic.Pointer[netconf.Network] // swapped by Reload; read with N()
	Reg  *netconf.Registry
	dns  atomic.Pointer[dnsResolver]
	mu   sync.Mutex
	tab  map[string]*ovPeer // overlay IP
	byS  map[*mux.Session]*ovPeer
	byL3 map[l3Sink]*ovPeer
}

// l3Sink is an attached L3 session that can emit an IP datagram.
type l3Sink interface {
	WriteIP([]byte) error
}

// ovPeer is one attached session: overlay IP, member, instance, VLAN, P2P listen.
// A peer is either a mux session (sess) or an L3 session (l3), never both.
// seen is the client's public address as the node saw it; together with
// listen and p2pPub it lets other members try a direct P2P connection.
type ovPeer struct {
	pin    []byte // PinOf the controller key the member's client trusts (Hello)
	ip     string
	member string
	inst   string
	vlan   int
	sess   *mux.Session
	l3     l3Sink
	seen   string
	listen uint16
	p2pPub []byte
	ttl    *time.Timer
	at     time.Time
}

// NewOverlay builds a switch for n (nil network yields nil).
func NewOverlay(n *netconf.Network) *Overlay {
	if n == nil {
		return nil
	}
	o := &Overlay{
		Reg: netconf.NewRegistry(n),
		tab: map[string]*ovPeer{}, byS: map[*mux.Session]*ovPeer{}, byL3: map[l3Sink]*ovPeer{},
	}
	o.cur.Store(n)
	o.dns.Store(newDNSResolver(n.DNSUpstream()))
	return o
}

// N is the network the switch runs now. It changes only by Reload, so a
// caller that needs several fields to agree takes one N() and keeps it.
func (o *Overlay) N() *netconf.Network {
	if o == nil {
		return nil
	}
	return o.cur.Load()
}

// remoteAddr parses host:port into a TCPAddr.
func remoteAddr(s string) net.Addr {
	host, port, err := net.SplitHostPort(s)
	if err != nil {
		return nil
	}
	ip := net.ParseIP(host)
	return &net.TCPAddr{IP: ip, Port: int(wire.Port(port))}
}

// stampOverlay binds overlay from Hello without a member token (the shared
// token case: no key ring, so no member id).
func stampOverlay(st *wire.Status, rest []byte, sess *mux.Session, remote net.Addr) {
	stampOverlayAuth(st, rest, sess, remote, nil)
}

// Contains reports whether host is inside a segment CIDR.
func (o *Overlay) Contains(host string) bool {
	if o == nil || o.N() == nil {
		return false
	}
	return o.N().ContainsIP(net.ParseIP(host))
}

// stampL3 copies the segment's gateway, DNS servers and MTU onto Status.
// Bind uses it for mux sessions too.
func (o *Overlay) stampL3(st *wire.Status, vlan int) {
	n := o.N()
	if n == nil {
		return
	}
	s := n.Seg(vlan)
	st.Gateway = s.Gateway
	st.DNS = s.DNSServers()
	if n.MTU > 0 {
		st.MTU = wire.U16(n.MTU)
	}
}

// Bind admits the session, stamps overlay IP/VLAN/DNS on Status, and attaches the peer.
// The registry leases the IP for member+instance, so the same install gets
// the same address back after a reconnect (OKResumed). When the registry
// refuses and the node runs on a shared token (no member keys), any free
// pool address is handed out instead; with member keys the refusal stands.
// Status also gets the peer hints, routes, ACL policy, session TTL and rate
// cap for this member, and the TTL and rate are armed on the session.
func (o *Overlay) Bind(st *wire.Status, hello wire.Hello, sess *mux.Session, remote net.Addr) {
	if o == nil {
		return
	}
	st.Peers = o.N().PeersMode()
	if o.N().P2PEnabled() {
		st.P2P = 1
	}
	member := hello.MemberID
	inst := hello.InstanceID
	ip, inst2, code := o.Reg.Admit(member, inst)
	if code != wire.OK && code != wire.OKResumed {
		if (member == "" || o.N().MemberByID(member) == nil) && (AuthKeys == nil || AuthKeys.Len() == 0) {
			used := map[string]bool{}
			o.mu.Lock()
			for x := range o.tab {
				used[x] = true
			}
			o.mu.Unlock()
			ip, err := o.N().AllocateIP(used)
			if err != nil {
				st.Code = wire.IPPool
				return
			}
			inst2 = inst
			if inst2 == "" {
				inst2 = "x"
			}
			st.Code = wire.OK
			st.IP = ip
			st.InstanceID = inst2
			o.stampL3(st, 0)
			o.attachTo(ip, 0, sess, nil, remote, "", inst2)
			st.PeerHints = o.hints(ip)
			o.stampRoutes(st)
			o.stampPolicy(st, "")
			o.stampTTL(st, "")
			o.armTTL(sess, "")
			o.stampRate(st, "")
			o.armRate(sess, "")
			return
		}
		st.Code = code
		return
	}
	vlan := 0
	if m := o.N().MemberByID(member); m != nil {
		vlan = m.VLAN
		st.VLAN = wire.U16(vlan)
	}
	st.Code = code
	st.IP = ip
	st.InstanceID = inst2
	o.stampL3(st, vlan)
	o.attachTo(ip, vlan, sess, nil, remote, member, inst2)
	st.PeerHints = o.hints(ip)
	o.stampRoutes(st)
	o.stampPolicy(st, member)
	o.stampTTL(st, member)
	o.armTTL(sess, member)
	o.stampRate(st, member)
	o.armRate(sess, member)
}

// BindL3 is Bind for an L3 IP pump instead of a mux session.
// Unlike Bind it only stamps the TTL and rate onto Status; it does not arm
// them, since armTTL and armRate work on a mux session.
func (o *Overlay) BindL3(st *wire.Status, hello wire.Hello, sink l3Sink, remote net.Addr) {
	if o == nil {
		return
	}
	st.Peers = o.N().PeersMode()
	if o.N().P2PEnabled() {
		st.P2P = 1
	}
	member := hello.MemberID
	inst := hello.InstanceID
	ip, inst2, code := o.Reg.Admit(member, inst)
	if code != wire.OK && code != wire.OKResumed {
		if (member == "" || o.N().MemberByID(member) == nil) && (AuthKeys == nil || AuthKeys.Len() == 0) {
			used := map[string]bool{}
			o.mu.Lock()
			for x := range o.tab {
				used[x] = true
			}
			o.mu.Unlock()
			ip, err := o.N().AllocateIP(used)
			if err != nil {
				st.Code = wire.IPPool
				return
			}
			inst2 = inst
			if inst2 == "" {
				inst2 = "x"
			}
			st.Code = wire.OK
			st.IP = ip
			st.InstanceID = inst2
			o.stampL3(st, 0)
			o.attachTo(ip, 0, nil, sink, remote, "", inst2)
			st.PeerHints = o.hints(ip)
			o.stampRoutes(st)
			o.stampPolicy(st, "")
			o.stampTTL(st, "")
			o.stampRate(st, "")
			return
		}
		st.Code = code
		return
	}
	vlan := 0
	if m := o.N().MemberByID(member); m != nil {
		vlan = m.VLAN
		st.VLAN = wire.U16(vlan)
	}
	st.Code = code
	st.IP = ip
	st.InstanceID = inst2
	o.stampL3(st, vlan)
	o.attachTo(ip, vlan, nil, sink, remote, member, inst2)
	st.PeerHints = o.hints(ip)
	o.stampRoutes(st)
	o.stampPolicy(st, member)
	o.stampTTL(st, member)
	o.stampRate(st, member)
}

// stampRoutes copies managed CIDRs onto Status, skipping routes this member owns.
// Via on the wire is the resolved overlay IP (live, then sticky).
func (o *Overlay) stampRoutes(st *wire.Status) {
	if o == nil || o.N() == nil || st == nil {
		return
	}
	for _, r := range o.N().Routes {
		if r.Dst == "" {
			continue
		}
		via := o.resolveVia(r)
		if via != "" && via == st.IP {
			continue
		}
		if via == "" && (r.Member != "" || r.Tag != "") {
			continue
		}
		st.Routes = append(st.Routes, wire.Route{Dst: r.Dst, Via: via})
		if len(st.Routes) >= 16 {
			break
		}
	}
}

// stampPolicy copies compact ACL onto Status so P2P can use the same Allow.
// A direct P2P connection between two members bypasses the node, so the
// client must enforce the same rules itself. The policy carries the rules,
// the member's own tags and the tags of other live peers (up to 32 each).
func (o *Overlay) stampPolicy(st *wire.Status, member string) {
	if o == nil || o.N() == nil || st == nil {
		return
	}
	n := o.N()
	var p wire.Policy
	if m := n.MemberByID(member); m != nil && len(m.Tags) > 0 {
		p.OwnTags = append([]string(nil), m.Tags...)
	}
	if n.ACLDefault == "drop" {
		p.ACLDefault = 1
	}
	for _, r := range n.Rules {
		p.Rules = append(p.Rules, policyRuleOf(r))
		if len(p.Rules) >= 32 {
			break
		}
	}
	o.mu.Lock()
	for ip, peer := range o.tab {
		if ip == st.IP {
			continue
		}
		tags := o.tagsOfLocked(peer.member)
		if len(tags) == 0 {
			continue
		}
		p.Peers = append(p.Peers, wire.PeerTags{IP: ip, Tags: tags})
		if len(p.Peers) >= 32 {
			break
		}
	}
	o.mu.Unlock()
	if len(p.OwnTags) == 0 && p.ACLDefault == 0 && len(p.Rules) == 0 && len(p.Peers) == 0 {
		return
	}
	p.Present = true
	st.Policy = p
}

// policyRuleOf converts a network.conf rule to its compact wire form.
func policyRuleOf(r netconf.Rule) wire.PolicyRule {
	pr := wire.PolicyRule{}
	if r.Action == "accept" {
		pr.Action = 1
	}
	if r.SrcVLAN != nil {
		v := uint16(*r.SrcVLAN)
		pr.SrcVLAN = &v
	}
	if r.DstVLAN != nil {
		v := uint16(*r.DstVLAN)
		pr.DstVLAN = &v
	}
	pr.Src, pr.Dst = r.Src, r.Dst
	pr.SrcTag, pr.DstTag = r.SrcTag, r.DstTag
	switch r.Proto {
	case "tcp":
		pr.Proto = 1
	case "udp":
		pr.Proto = 2
	case "icmp":
		pr.Proto = 3
	}
	for _, p := range r.Dport {
		pr.Dport = append(pr.Dport, uint16(p))
	}
	for _, p := range r.Sport {
		pr.Sport = append(pr.Sport, uint16(p))
	}
	return pr
}

// sessionOf is the session settings for member: built-in defaults, then the
// network's, then the member's own overrides.
func (o *Overlay) sessionOf(member string) netconf.SessionPolicy {
	p := netconf.DefaultSession().Merge(o.N().Session)
	if m := o.N().MemberByID(member); m != nil {
		p = p.Merge(m.Session)
	}
	return p
}

// stampTTL copies live-session max onto Status. 0 omits the tail.
func (o *Overlay) stampTTL(st *wire.Status, member string) {
	if o == nil || o.N() == nil || st == nil {
		return
	}
	if d := o.sessionOf(member).TTL(); d > 0 {
		st.SessionTTLMS = uint32(d / time.Millisecond)
	}
}

// armTTL closes sess after the live-session max. Distinct from lease_hold.
func (o *Overlay) armTTL(sess *mux.Session, member string) {
	if o == nil || sess == nil || o.N() == nil {
		return
	}
	d := o.sessionOf(member).TTL()
	if d <= 0 {
		return
	}
	o.mu.Lock()
	peer := o.byS[sess]
	if peer == nil {
		o.mu.Unlock()
		return
	}
	if peer.ttl != nil {
		peer.ttl.Stop()
	}
	peer.ttl = time.AfterFunc(d, func() {
		sess.Close()
		o.Detach(sess)
	})
	o.mu.Unlock()
}

// stampRate copies the bulk payload cap onto Status. 0 omits the tail.
func (o *Overlay) stampRate(st *wire.Status, member string) {
	if o == nil || o.N() == nil || st == nil {
		return
	}
	if bps := o.sessionOf(member).Rate(); bps > 0 {
		st.RateBPS = bps
	}
}

// armRate caps bulk Data/Dgram on sess. 0 is unlimited. Relay trunks skip this.
func (o *Overlay) armRate(sess *mux.Session, member string) {
	if o == nil || sess == nil || o.N() == nil || sess.Relay() {
		return
	}
	sess.SetRate(uint64(o.sessionOf(member).Rate()))
}

// resolveVia is live overlay IP of Member/Tag, else sticky RouteHop.
// A route names the member (or a tag) that owns the subnet, not an IP; the
// IP is whatever that member holds now. When no such member is connected,
// RouteHop falls back to the configured Via or the member's sticky
// (reserved) IP.
func (o *Overlay) resolveVia(r netconf.Route) string {
	if o == nil {
		return ""
	}
	if r.Member != "" {
		if ip := o.liveMember(r.Member); ip != "" {
			return ip
		}
	}
	if r.Tag != "" {
		if ip := o.liveTag(r.Tag); ip != "" {
			return ip
		}
	}
	if o.N() != nil {
		return o.N().RouteHop(r)
	}
	return ""
}

// liveMember is the overlay IP of a connected session of member id.
func (o *Overlay) liveMember(id string) string {
	if o == nil || id == "" {
		return ""
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	for ip, p := range o.tab {
		if p != nil && p.member == id {
			return ip
		}
	}
	return ""
}

// liveTag is the overlay IP of a connected member that has tag.
func (o *Overlay) liveTag(tag string) string {
	if o == nil || tag == "" || o.N() == nil {
		return ""
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	for ip, p := range o.tab {
		if p == nil || p.member == "" {
			continue
		}
		if m := o.N().MemberByID(p.member); m != nil && m.HasTag(tag) {
			return ip
		}
	}
	return ""
}

// Routed is the overlay IP of the member that owns host, or empty.
// With overlapping routes the most specific (longest prefix) wins.
func (o *Overlay) Routed(host string) string {
	if o == nil || o.N() == nil {
		return ""
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return ""
	}
	best := -1
	via := ""
	for _, r := range o.N().Routes {
		hop := o.resolveVia(r)
		if hop == "" {
			continue
		}
		_, cidr, err := net.ParseCIDR(r.Dst)
		if err != nil || !cidr.Contains(ip) {
			continue
		}
		ones, _ := cidr.Mask.Size()
		if ones >= best {
			best = ones
			via = hop
		}
	}
	return via
}

// attach records sess under overlay ip (replacing any previous row for this session).
func (o *Overlay) attach(ip string, vlan int, sess *mux.Session, remote net.Addr, member, inst string) {
	o.attachTo(ip, vlan, sess, nil, remote, member, inst)
}

// attachTo records a mux session and/or L3 sink under overlay ip.
// The new row replaces any previous row of this session and any other
// session that held the same IP (for example the old session of a client
// that just reconnected).
func (o *Overlay) attachTo(ip string, vlan int, sess *mux.Session, sink l3Sink, remote net.Addr, member, inst string) {
	if ip == "" || (sess == nil && sink == nil) {
		return
	}
	host := ""
	if remote != nil {
		host, _, _ = net.SplitHostPort(remote.String())
		if host == "" {
			host = remote.String()
		}
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if sess != nil {
		if old := o.byS[sess]; old != nil {
			if old.ttl != nil {
				old.ttl.Stop()
			}
			if o.tab[old.ip] == old {
				delete(o.tab, old.ip)
			}
		}
	}
	if sink != nil {
		if old := o.byL3[sink]; old != nil {
			if old.ttl != nil {
				old.ttl.Stop()
			}
			if o.tab[old.ip] == old {
				delete(o.tab, old.ip)
			}
		}
	}
	if cur := o.tab[ip]; cur != nil && cur.sess != sess && cur.l3 != sink {
		if cur.ttl != nil {
			cur.ttl.Stop()
		}
		if cur.sess != nil {
			delete(o.byS, cur.sess)
		}
		if cur.l3 != nil {
			delete(o.byL3, cur.l3)
		}
	}
	p := &ovPeer{ip: ip, member: member, inst: inst, vlan: vlan, sess: sess, l3: sink, seen: host, at: time.Now()}
	o.tab[ip] = p
	if sess != nil {
		o.byS[sess] = p
	}
	if sink != nil {
		o.byL3[sink] = p
	}
}

// SessionIP is the overlay IPv4 leased to sess, or empty if unbound.
func (o *Overlay) SessionIP(sess *mux.Session) string {
	if o == nil || sess == nil {
		return ""
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if p := o.byS[sess]; p != nil {
		return p.ip
	}
	return ""
}

// Detach drops sess from the table and Releases the lease (lease_hold may keep the IP).
// A later session that already took this IP (same instance resume) is left in place.
func (o *Overlay) Detach(sess *mux.Session) {
	if o == nil || sess == nil {
		return
	}
	o.mu.Lock()
	p := o.byS[sess]
	release := false
	if p != nil {
		if p.ttl != nil {
			p.ttl.Stop()
			p.ttl = nil
		}
		delete(o.byS, sess)
		if o.tab[p.ip] == p {
			delete(o.tab, p.ip)
			release = true
		}
	}
	o.mu.Unlock()
	if release && o.Reg != nil {
		o.Reg.Release(p.member, p.inst)
	}
}

// DetachL3 drops an L3 sink from the table and Releases the lease.
func (o *Overlay) DetachL3(sink l3Sink) {
	if o == nil || sink == nil {
		return
	}
	o.mu.Lock()
	p := o.byL3[sink]
	release := false
	if p != nil {
		if p.ttl != nil {
			p.ttl.Stop()
			p.ttl = nil
		}
		delete(o.byL3, sink)
		if o.tab[p.ip] == p {
			delete(o.tab, p.ip)
			release = true
		}
	}
	o.mu.Unlock()
	if release && o.Reg != nil {
		o.Reg.Release(p.member, p.inst)
	}
}

// WriteL3 emits pkt to the live L3 sink leased at overlay ip.
// NAT replies look up the sink so a replaced CONNECT keeps the flow.
func (o *Overlay) WriteL3(ip string, pkt []byte) error {
	if o == nil || ip == "" || len(pkt) < 20 {
		return io.ErrClosedPipe
	}
	o.mu.Lock()
	p := o.tab[ip]
	o.mu.Unlock()
	if p == nil || p.l3 == nil {
		return io.ErrClosedPipe
	}
	return p.l3.WriteIP(pkt)
}

// SetListen records the P2P listen port and optional X25519 pub from loft.peer.
func (o *Overlay) SetListen(sess *mux.Session, port uint16, pub []byte) {
	if o == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if p := o.byS[sess]; p != nil {
		p.listen = port
		if len(pub) == 32 {
			p.p2pPub = append([]byte(nil), pub...)
		}
	}
}

// hints lists up to 16 live P2P listen ports (TLS-observed host), excluding except.
func (o *Overlay) hints(except string) []wire.PeerHint {
	o.mu.Lock()
	defer o.mu.Unlock()
	var out []wire.PeerHint
	for ip, p := range o.tab {
		if ip == except || p.listen == 0 || p.seen == "" {
			continue
		}
		out = append(out, wire.PeerHint{IP: ip, Host: p.seen, Port: p.listen, VLAN: wire.U16(p.vlan), Pub: append([]byte(nil), p.p2pPub...)})
		if len(out) >= 16 {
			break
		}
	}
	return out
}

// Forward splices st to the peer session (hub: policy_drop; vlan: same VLAN; mesh: Open 127.0.0.1:port).
func (o *Overlay) Forward(st *mux.Stream, host, port string) {
	o.forwardMember(st, host, port)
}

// forwardMember is overlay-IP Open: ACL, then splice as 127.0.0.1:port on the peer.
// The node opens a stream on the destination member's mux with target
// 127.0.0.1:port, which that member's client connects to its own local
// port. A VLAN mismatch gets its own reset code so the client can tell it
// from an ordinary ACL drop.
func (o *Overlay) forwardMember(st *mux.Stream, host, port string) {
	if o == nil {
		st.Reset(wire.StreamRefused)
		return
	}
	o.mu.Lock()
	src := o.byS[st.Sess()]
	dst := o.tab[host]
	o.mu.Unlock()
	if dst == nil || dst.sess == nil {
		st.Reset(wire.StreamRefused)
		return
	}
	p := o.packet(src, net.ParseIP(host), dst, "tcp", port, false)
	if !o.N().Allow(p) {
		if o.N().Peers == netconf.PeersVLAN && src != nil && src.vlan != dst.vlan {
			st.Reset(wire.VLANDenied)
			return
		}
		st.Reset(wire.PolicyDrop)
		return
	}
	o.splice(st, dst.sess, net.JoinHostPort("127.0.0.1", port))
}

// forwardRouted is Open to a CIDR behind a member: ACL, then splice host:port on that member.
func (o *Overlay) forwardRouted(st *mux.Stream, via, host, port string) {
	if o == nil {
		st.Reset(wire.StreamRefused)
		return
	}
	o.mu.Lock()
	src := o.byS[st.Sess()]
	dst := o.tab[via]
	o.mu.Unlock()
	if dst == nil || dst.sess == nil {
		st.Reset(wire.StreamRefused)
		return
	}
	p := o.packet(src, net.ParseIP(host), dst, "tcp", port, true)
	if !o.N().Allow(p) {
		st.Reset(wire.PolicyDrop)
		return
	}
	o.splice(st, dst.sess, net.JoinHostPort(host, port))
}

// packet is one overlay Open/ICMP for Allow.
func (o *Overlay) packet(src *ovPeer, dstIP net.IP, dst *ovPeer, proto, port string, routed bool) netconf.Packet {
	p := netconf.Packet{
		Dst:    dstIP,
		Proto:  proto,
		Dport:  int(wire.Port(port)),
		Routed: routed,
	}
	if o.N() != nil && dstIP != nil {
		p.ToGW = o.N().IsGateway(dstIP.String())
	}
	if src != nil {
		p.Src = net.ParseIP(src.ip)
		p.SrcVLAN = src.vlan
		p.SrcTags = o.tagsOf(src.member)
	}
	if dst != nil {
		p.DstVLAN = dst.vlan
		p.DstTags = o.tagsOf(dst.member)
	}
	return p
}

// tagsOf is the live member tag list (config reload without reconnect).
func (o *Overlay) tagsOf(id string) []string {
	if o == nil || o.N() == nil || id == "" {
		return nil
	}
	return o.tagsOfLocked(id)
}

// tagsOfLocked is tagsOf for callers that hold o.mu. It reads only the
// config, not the session table.
func (o *Overlay) tagsOfLocked(id string) []string {
	if o.N() == nil || id == "" {
		return nil
	}
	if m := o.N().MemberByID(id); m != nil && len(m.Tags) > 0 {
		return append([]string(nil), m.Tags...)
	}
	return nil
}

// splice copies st to an Open on dst.
func (o *Overlay) splice(st *mux.Stream, dst *mux.Session, target string) {
	peer, err := dst.Open(target)
	if err != nil {
		st.Reset(wire.StreamRefused)
		return
	}
	defer peer.Close()
	go func() { _, _ = io.Copy(peer, st); peer.Close() }()
	_, _ = io.Copy(st, peer)
}

// DeliverICMP switches an overlay IPv4 ICMP packet.
// Gateway address is answered here. Other members follow peers/ACL.
// A destination inside a routed subnet goes to the member that owns it.
func (o *Overlay) DeliverICMP(from *mux.Session, pkt []byte) {
	if o == nil || o.N() == nil || from == nil || len(pkt) < 28 {
		return
	}
	if pkt[0]>>4 != 4 || pkt[9] != 1 {
		return
	}
	src := net.IP(pkt[12:16]).String()
	dst := net.IP(pkt[16:20]).String()
	o.mu.Lock()
	srcP := o.byS[from]
	dstP := o.tab[dst]
	o.mu.Unlock()
	if srcP == nil {
		return
	}
	// Trust the session lease, not the IPv4 source. Darwin utun sources
	// overlay pings from 169.254.8.1 (dummy p2p local).
	if srcP.ip != src {
		sip := net.ParseIP(srcP.ip)
		if sip == nil || sip.To4() == nil {
			return
		}
		pkt = rewriteIPv4Src(pkt, sip)
		src = srcP.ip
	}
	p := o.packet(srcP, net.ParseIP(dst), dstP, "icmp", overlayICMPPort, false)
	if dstP == nil {
		if via := o.Routed(dst); via != "" {
			o.mu.Lock()
			viaP := o.tab[via]
			o.mu.Unlock()
			p = o.packet(srcP, net.ParseIP(dst), viaP, "icmp", overlayICMPPort, true)
			if !o.N().Allow(p) || viaP == nil || viaP.sess == nil {
				return
			}
			_ = viaP.sess.WriteDgram(net.JoinHostPort(dst, overlayICMPPort), pkt)
			return
		}
	}
	if !o.N().Allow(p) {
		return
	}
	if dst == o.N().Gateway {
		reply, ok := icmpEchoSwap(pkt)
		if !ok {
			return
		}
		_ = from.WriteDgram(net.JoinHostPort(srcP.ip, overlayICMPPort), reply)
		return
	}
	if dstP == nil || dstP.sess == nil {
		return
	}
	_ = dstP.sess.WriteDgram(net.JoinHostPort(dst, overlayICMPPort), pkt)
}

// parsePeerAnnounce reads loft.peer:<port> from an Open target.
func parsePeerAnnounce(target string) (uint16, bool) {
	host, port, err := net.SplitHostPort(target)
	if err != nil || host != "loft.peer" {
		return 0, false
	}
	p := wire.Port(port)
	if p == 0 {
		return 0, false
	}
	return p, true
}

// Handle is overlay Open: loft.peer announce, gateway:53 DNS TCP, or CIDR Forward.
// It returns false when the target is not an overlay matter, so the caller
// sends it to the internet. loft.peer:<port> is a pseudo-target a client
// opens to announce its P2P listen port; it may be followed by a 32-byte
// X25519 public key for the P2P handshake.
func (o *Overlay) Handle(st *mux.Stream) bool {
	if o == nil {
		return false
	}
	host, port, err := net.SplitHostPort(st.Target())
	if err != nil {
		return false
	}
	if host == "loft.peer" {
		if p, ok := parsePeerAnnounce(st.Target()); ok {
			var pub []byte
			buf := make([]byte, 32)
			_ = st.SetReadDeadline(time.Now().Add(800 * time.Millisecond))
			if _, err := io.ReadFull(st, buf); err == nil {
				pub = buf
			}
			_ = st.SetReadDeadline(time.Time{})
			o.SetListen(st.Sess(), p, pub)
		}
		st.Close()
		return true
	}
	if port == dnsPort && o.IsDNS(host) {
		go o.serveDNSTCP(st)
		return true
	}
	if o.Contains(host) {
		o.forwardMember(st, host, port)
		return true
	}
	if via := o.Routed(host); via != "" {
		o.forwardRouted(st, via, host, port)
		return true
	}
	return false
}

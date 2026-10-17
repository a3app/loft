package transport

// Overlay switch: leases, hub/mesh/vlan Forward, loft.peer announce, DNS, ICMP.

import (
	"io"
	"net"
	"sync"

	"github.com/a3app/loft/internal/mux"
	"github.com/a3app/loft/internal/netconf"
	"github.com/a3app/loft/internal/wire"
)

// NetSwitch is the overlay fabric (nil = internet-only proxy).
var NetSwitch *Overlay

// Overlay is the node's member fabric: overlay CIDR, leases, and live sessions.
type Overlay struct {
	Net *netconf.Network
	Reg *netconf.Registry
	dns *dnsResolver
	mu  sync.Mutex
	tab map[string]*ovPeer // overlay IP
	byS map[*mux.Session]*ovPeer
}

// ovPeer is one attached session: overlay IP, member, instance, VLAN, P2P listen.
type ovPeer struct {
	ip     string
	member string
	inst   string
	vlan   int
	sess   *mux.Session
	seen   string
	listen uint16
}

// NewOverlay builds a switch for n (nil network yields nil).
func NewOverlay(n *netconf.Network) *Overlay {
	if n == nil {
		return nil
	}
	return &Overlay{
		Net: n, Reg: netconf.NewRegistry(n), dns: newDNSResolver(n.DNSUpstream()),
		tab: map[string]*ovPeer{}, byS: map[*mux.Session]*ovPeer{},
	}
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

// stampOverlay binds overlay from Hello without a member token.
func stampOverlay(st *wire.Status, rest []byte, sess *mux.Session, remote net.Addr) {
	stampOverlayAuth(st, rest, sess, remote, nil)
}

// Contains reports whether host is inside the overlay CIDR.
func (o *Overlay) Contains(host string) bool {
	if o == nil || o.Net == nil {
		return false
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return false
	}
	_, cidr, err := net.ParseCIDR(o.Net.CIDR)
	if err != nil {
		return false
	}
	return cidr.Contains(ip)
}

// Bind admits the session, stamps overlay IP/VLAN/DNS on Status, and attaches the peer.
func (o *Overlay) Bind(st *wire.Status, hello wire.Hello, sess *mux.Session, remote net.Addr) {
	if o == nil {
		return
	}
	st.Peers = o.Net.PeersMode()
	if o.Net.P2PEnabled() {
		st.P2P = 1
	}
	member := hello.MemberID
	inst := hello.InstanceID
	ip, inst2, code := o.Reg.Admit(member, inst)
	if code != wire.OK && code != wire.OKResumed {
		if (member == "" || o.Net.MemberByID(member) == nil) && (AuthKeys == nil || AuthKeys.Len() == 0) {
			used := map[string]bool{}
			o.mu.Lock()
			for x := range o.tab {
				used[x] = true
			}
			o.mu.Unlock()
			ip, err := o.Net.AllocateIP(used)
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
			st.Gateway = o.Net.Gateway
			st.MTU = wire.U16(o.Net.MTU)
			st.DNS = o.Net.DNSServers()
			o.attach(ip, 0, sess, remote, "", inst2)
			st.PeerHints = o.hints(ip)
			return
		}
		st.Code = code
		return
	}
	vlan := 0
	if m := o.Net.MemberByID(member); m != nil {
		vlan = m.VLAN
		st.VLAN = wire.U16(vlan)
	}
	st.Code = code
	st.IP = ip
	st.InstanceID = inst2
	st.Gateway = o.Net.Gateway
	if o.Net.MTU > 0 {
		st.MTU = wire.U16(o.Net.MTU)
	}
	st.DNS = o.Net.DNSServers()
	o.attach(ip, vlan, sess, remote, member, inst2)
	st.PeerHints = o.hints(ip)
}

// attach records sess under overlay ip (replacing any previous row for this session).
func (o *Overlay) attach(ip string, vlan int, sess *mux.Session, remote net.Addr, member, inst string) {
	if sess == nil || ip == "" {
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
	if old := o.byS[sess]; old != nil && o.tab[old.ip] == old {
		delete(o.tab, old.ip)
	}
	if cur := o.tab[ip]; cur != nil && cur.sess != sess {
		delete(o.byS, cur.sess)
	}
	p := &ovPeer{ip: ip, member: member, inst: inst, vlan: vlan, sess: sess, seen: host}
	o.tab[ip] = p
	o.byS[sess] = p
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

// SetListen records the P2P listen port announced as loft.peer:<port>.
func (o *Overlay) SetListen(sess *mux.Session, port uint16) {
	if o == nil {
		return
	}
	o.mu.Lock()
	defer o.mu.Unlock()
	if p := o.byS[sess]; p != nil {
		p.listen = port
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
		out = append(out, wire.PeerHint{IP: ip, Host: p.seen, Port: p.listen, VLAN: wire.U16(p.vlan)})
		if len(out) >= 16 {
			break
		}
	}
	return out
}

// Forward splices st to the peer session (hub: policy_drop; vlan: same VLAN; mesh: Open 127.0.0.1:port).
func (o *Overlay) Forward(st *mux.Stream, host, port string) {
	if o == nil {
		st.Reset(wire.StreamRefused)
		return
	}
	if o.Net.Peers == netconf.PeersHub || o.Net.Peers == "" {
		st.Reset(wire.PolicyDrop)
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
	if o.Net.Peers == netconf.PeersVLAN && src != nil && src.vlan != dst.vlan {
		st.Reset(wire.VLANDenied)
		return
	}
	local := net.JoinHostPort("127.0.0.1", port)
	peer, err := dst.sess.Open(local)
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
func (o *Overlay) DeliverICMP(from *mux.Session, pkt []byte) {
	if o == nil || o.Net == nil || from == nil || len(pkt) < 28 {
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
	p := netconf.Packet{
		Src: net.ParseIP(src), Dst: net.ParseIP(dst),
		Proto: "icmp", ToGW: dst == o.Net.Gateway,
	}
	p.SrcVLAN = srcP.vlan
	if dstP != nil {
		p.DstVLAN = dstP.vlan
	}
	if !o.Net.Allow(p) {
		return
	}
	if dst == o.Net.Gateway {
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
			o.SetListen(st.Sess(), p)
		}
		st.Close()
		return true
	}
	if port == dnsPort && o.IsDNS(host) {
		go o.serveDNSTCP(st)
		return true
	}
	if o.Contains(host) {
		o.Forward(st, host, port)
		return true
	}
	return false
}

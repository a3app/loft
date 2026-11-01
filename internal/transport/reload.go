package transport

// Live reload of network.conf and members.keys (SIGHUP).

import (
	"reflect"

	"github.com/a3app/loft/internal/mux"
	"github.com/a3app/loft/internal/netconf"
)

// Reload switches the overlay to n without dropping the members it does
// not concern. What a session learnt at Hello (address, gateway, MTU, DNS,
// routes, peers, VLANs, policy, timers) is stamped once, so a change there
// closes every session and clients reconnect with the new values; a change
// to one member closes only that member's sessions. Rules are enforced by
// the node on every packet, but with p2p on they also travel to clients, so
// then a rule change reconnects everyone too. It returns how many sessions
// it closed.
func (o *Overlay) Reload(n *netconf.Network) int {
	if o == nil || n == nil {
		return 0
	}
	old := o.N()
	o.cur.Store(n)
	if o.Reg != nil {
		o.Reg.SetNetwork(n)
	}
	if old != nil && !reflect.DeepEqual(old.DNSUpstream(), n.DNSUpstream()) {
		o.dns.Store(newDNSResolver(n.DNSUpstream()))
	}
	all := old == nil || networkWide(old, n)
	var drop []*mux.Session
	o.mu.Lock()
	for s, p := range o.byS {
		if all || memberChanged(old, n, p.member) {
			drop = append(drop, s)
		}
	}
	o.mu.Unlock()
	for _, s := range drop {
		s.Close()
		o.Detach(s)
	}
	return len(drop)
}

// networkWide reports a change that sessions learnt at Hello.
func networkWide(a, b *netconf.Network) bool {
	if a.ID != b.ID || a.CIDR != b.CIDR || a.Gateway != b.Gateway || a.MTU != b.MTU || a.Peers != b.Peers ||
		a.PoolFrom != b.PoolFrom || a.PoolTo != b.PoolTo || a.Enabled != b.Enabled || a.P2PEnabled() != b.P2PEnabled() {
		return true
	}
	if !reflect.DeepEqual(a.DNSServers(), b.DNSServers()) || !reflect.DeepEqual(a.Routes, b.Routes) ||
		!reflect.DeepEqual(a.VLANs, b.VLANs) || !reflect.DeepEqual(a.Session, b.Session) {
		return true
	}
	return b.P2PEnabled() && !reflect.DeepEqual(a.Rules, b.Rules)
}

// memberChanged is true when member id was removed, disabled, or edited
// (key, address, VLAN, devices, tags, timers). An unbound session ("") of a
// shared-token node is kept.
func memberChanged(a, b *netconf.Network, id string) bool {
	if id == "" {
		return false
	}
	x, y := a.MemberByID(id), b.MemberByID(id)
	if x == nil || y == nil {
		return true
	}
	return !reflect.DeepEqual(*x, *y)
}

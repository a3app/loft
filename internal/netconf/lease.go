package netconf

// Live overlay leases: sticky member IPs, multi-instance caps, hold-then-GC.

import (
	"bytes"
	"crypto/rand"
	"encoding/hex"
	"net"
	"sync"
	"time"

	"github.com/a3app/loft/internal/wire"
)

// Lease binds one running instance of a subscriber key to an overlay IP.
type Lease struct {
	MemberID   string
	InstanceID string
	IP         string
	Online     bool
	Sticky     bool
	Seen       time.Time
}

// Registry is the live set of overlay leases for one Network.
type Registry struct {
	mu     sync.Mutex
	net    *Network
	leases map[string]*Lease // memberID+"\x00"+instanceID
	byIP   map[string]*Lease
	now    func() time.Time
}

// NewRegistry holds leases for n. n must remain valid for the registry lifetime.
func NewRegistry(n *Network) *Registry {
	return &Registry{
		net:    n,
		leases: map[string]*Lease{},
		byIP:   map[string]*Lease{},
		now:    time.Now,
	}
}

// keyOf is memberID + NUL + instanceID, unique per running instance.
func keyOf(memberID, instanceID string) string { return memberID + "\x00" + instanceID }

// newInstanceID is 8 random bytes, hex-encoded, when Admit gets an empty instance.
func newInstanceID() string {
	var b [8]byte
	_, _ = rand.Read(b[:])
	return hex.EncodeToString(b[:])
}

// Admit checks the key's policy and returns the overlay IP for this instance.
// Empty instanceID is minted. The same instance reconnects (OKResumed) without a new IP.
func (r *Registry) Admit(memberID, instanceID string) (ip string, inst string, code wire.Code) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.gc()

	if !r.net.Enabled {
		return "", instanceID, wire.NetDisabled
	}
	m := r.net.MemberByID(memberID)
	if m == nil {
		return "", instanceID, wire.AuthBadKey
	}
	if !m.Authorized {
		return "", instanceID, wire.AuthNotAuthorized
	}
	if instanceID == "" {
		instanceID = newInstanceID()
	}

	pol := DefaultSession().Merge(r.net.Session).Merge(m.Session)
	k := keyOf(memberID, instanceID)
	if old := r.leases[k]; old != nil {
		old.Online = true
		old.Seen = r.now()
		return old.IP, instanceID, wire.OKResumed
	}

	online := r.onlineOf(memberID)
	if !m.Multi && len(online) > 0 {
		return "", instanceID, wire.AuthAlreadyOnline
	}
	if len(online) >= m.MaxSessions() {
		return "", instanceID, wire.AuthLimit
	}

	ip, sticky, err := r.pickIP(m, online)
	if err != nil {
		return "", instanceID, wire.IPPool
	}
	l := &Lease{
		MemberID: memberID, InstanceID: instanceID, IP: ip,
		Online: true, Sticky: sticky, Seen: r.now(),
	}
	r.leases[k] = l
	r.byIP[ip] = l
	_ = pol
	return ip, instanceID, wire.OK
}

// Release marks the instance offline; the IP stays reserved until Hold elapses.
func (r *Registry) Release(memberID, instanceID string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if l := r.leases[keyOf(memberID, instanceID)]; l != nil {
		l.Online = false
		l.Seen = r.now()
	}
}

// onlineOf lists currently Online leases for memberID.
func (r *Registry) onlineOf(memberID string) []*Lease {
	var out []*Lease
	for _, l := range r.leases {
		if l.MemberID == memberID && l.Online {
			out = append(out, l)
		}
	}
	return out
}

// pickIP prefers the member sticky IP when not held, else the next free pool address.
func (r *Registry) pickIP(m *Member, online []*Lease) (ip string, sticky bool, err error) {
	used := r.used()
	stickyIP := m.StickyIP()
	if stickyIP != "" {
		holder := r.byIP[stickyIP]
		taken := holder != nil && (holder.Online || r.now().Sub(holder.Seen) < r.hold(m))
		if !taken {
			return stickyIP, true, nil
		}
	}
	ip, err = r.net.nextFree(used)
	return ip, false, err
}

// used is IPs still reserved: gateway, online leases, and offline leases inside Hold.
func (r *Registry) used() map[string]bool {
	u := map[string]bool{}
	if gw := r.net.Gateway; gw != "" {
		u[gw] = true
	}
	hold := DefaultSession().Merge(r.net.Session).Hold()
	now := r.now()
	for _, l := range r.leases {
		if l.Online || now.Sub(l.Seen) < hold {
			u[l.IP] = true
		}
	}
	return u
}

// hold is DefaultSession merged with network then member LeaseHold.
func (r *Registry) hold(m *Member) time.Duration {
	return DefaultSession().Merge(r.net.Session).Merge(m.Session).Hold()
}

// gc drops offline leases whose Hold has elapsed and frees their IPs.
func (r *Registry) gc() {
	now := r.now()
	for k, l := range r.leases {
		m := r.net.MemberByID(l.MemberID)
		h := DefaultSession().Merge(r.net.Session).Hold()
		if m != nil {
			h = r.hold(m)
		}
		if !l.Online && now.Sub(l.Seen) >= h {
			delete(r.leases, k)
			if r.byIP[l.IP] == l {
				delete(r.byIP, l.IP)
			}
		}
	}
}

// AllocateIP is nextFree with an optional used set (gateway is always reserved).
func (n *Network) AllocateIP(used map[string]bool) (string, error) {
	if used == nil {
		used = map[string]bool{}
	}
	return n.nextFree(used)
}

// nextFree scans CIDR from network+1 (or PoolFrom) through PoolTo, skipping used and broadcast.
// Prefixes shorter than /16 return errWide.
func (n *Network) nextFree(used map[string]bool) (string, error) {
	if used == nil {
		used = map[string]bool{}
	}
	if n.Gateway != "" {
		used[n.Gateway] = true
	}
	ip, ipnet, err := parseCIDR4(n.CIDR)
	if err != nil {
		return "", err
	}
	ones, bits := ipnet.Mask.Size()
	if bits-ones > 16 {
		return "", errWide
	}
	from, to, err := n.poolBounds(ipnet)
	if err != nil {
		return "", err
	}
	cur := ip.Mask(ipnet.Mask)
	inc(cur)
	if from != nil && bytes.Compare(cur, from) < 0 {
		cur = append(net.IP(nil), from...)
	}
	for ipnet.Contains(cur) {
		if to != nil && bytes.Compare(cur, to) > 0 {
			break
		}
		s := cur.String()
		if !used[s] && !isBroadcast(cur, ipnet) {
			return s, nil
		}
		inc(cur)
	}
	return "", errPool
}

// SetNetwork points the registry at n (a reload). Leases of members that
// are gone, and leases whose IP left the network, are dropped; the rest keep
// their IPs.
func (r *Registry) SetNetwork(n *Network) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.net = n
	_, cidr, err := net.ParseCIDR(n.CIDR)
	for k, l := range r.leases {
		gone := n.MemberByID(l.MemberID) == nil && l.MemberID != ""
		out := err == nil && !cidr.Contains(net.ParseIP(l.IP))
		if gone || out {
			delete(r.leases, k)
			if r.byIP[l.IP] == l {
				delete(r.byIP, l.IP)
			}
		}
	}
}

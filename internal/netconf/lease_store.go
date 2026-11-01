package netconf

// Leases that survive a node restart.
//
// Why this exists: the Registry keeps leases only in memory. When the node
// restarts, every reservation is gone, and the members that reconnect take
// addresses in the order they happen to arrive. A member that had 10.77.0.10
// before the restart can come back as 10.77.0.11. For a member in TUN mode
// that is bad: the operating system on the member's side still has the old
// address on its network card and drops every reply sent to the new one.
//
// The fix: the Registry writes its leases (member, instance, IP) to a small
// file in the node's data directory each time the set changes. On start the
// node reads the file back and gives every lease a fresh hold window, as if
// all members had just disconnected. A member that reconnects within that
// window resumes its lease and gets its old address again, exactly as after
// a short network gap without a restart.

import (
	"encoding/json"
	"net"
	"os"
)

// storedLease is one line of the lease file. Online and Seen are not stored:
// after a restart nobody is online, and the hold window starts at load time.
type storedLease struct {
	Member   string `json:"member"`
	Instance string `json:"instance"`
	IP       string `json:"ip"`
	Sticky   bool   `json:"sticky,omitempty"`
}

// Persist makes the registry keep its leases in the file at path.
//
// Steps:
//  1. Read the file left by the previous run, if there is one. A missing or
//     broken file is not an error: the node then starts with no leases, the
//     same as before this feature existed.
//  2. Put back each lease that still makes sense for the current network
//     (see restore), marked offline with Seen = now, so it is held for the
//     usual lease hold time from this moment.
//  3. Remember path; from now on every change of the lease set is written.
//
// Call it once at start, before the node admits anyone.
func (r *Registry) Persist(path string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if b, err := os.ReadFile(path); err == nil {
		var list []storedLease
		if json.Unmarshal(b, &list) == nil {
			r.restore(list)
		}
	}
	r.path = path
	// Write at once: restore may have dropped stale lines, and the file
	// should show what the node really holds.
	r.save()
}

// restore puts stored leases back. A lease is skipped when:
//   - its member was removed from the network while the node was down;
//   - its IP is no longer inside the member's segment (the admin changed
//     the subnet or moved the member to another VLAN);
//   - another lease already took that IP (a broken or hand-edited file).
//
// In all these cases the member simply gets a new address on reconnect.
func (r *Registry) restore(list []storedLease) {
	now := r.now()
	for _, s := range list {
		ip := net.ParseIP(s.IP)
		if ip == nil || s.Instance == "" {
			continue
		}
		m := r.net.MemberByID(s.Member)
		if m == nil && s.Member != "" {
			continue
		}
		vlan := 0
		if m != nil {
			vlan = m.VLAN
		}
		_, cidr, err := net.ParseCIDR(r.net.Seg(vlan).CIDR)
		if err != nil || !cidr.Contains(ip) {
			continue
		}
		if r.byIP[s.IP] != nil {
			continue
		}
		l := &Lease{
			MemberID: s.Member, InstanceID: s.Instance, IP: s.IP,
			Online: false, Sticky: s.Sticky, Seen: now,
		}
		r.leases[keyOf(s.Member, s.Instance)] = l
		r.byIP[s.IP] = l
	}
}

// save writes the current leases to r.path. The caller holds r.mu.
//
// It is called only when a lease is added or removed, not on every
// reconnect, so it runs rarely and stays cheap. The file is written to a
// temporary name first and then renamed: a crash in the middle leaves the
// old file whole instead of a half-written one.
//
// A failed write is ignored on purpose: losing the file only brings back
// the old behaviour (new addresses after a restart); it must never stop
// members from connecting.
func (r *Registry) save() {
	if r.path == "" {
		return
	}
	list := make([]storedLease, 0, len(r.leases))
	for _, l := range r.leases {
		list = append(list, storedLease{
			Member: l.MemberID, Instance: l.InstanceID, IP: l.IP, Sticky: l.Sticky,
		})
	}
	b, err := json.Marshal(list)
	if err != nil {
		return
	}
	tmp := r.path + ".new"
	if os.WriteFile(tmp, b, 0o600) != nil {
		return
	}
	_ = os.Rename(tmp, r.path)
}

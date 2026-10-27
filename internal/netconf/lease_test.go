package netconf

// Admit: single-session, sticky reconnect, multi cap, hold, unauthorized.

import (
	"testing"
	"time"

	"github.com/a3app/loft/internal/wire"
)

// office is a /29 fixture with alice (sticky) and bob (multi-2).
func office() *Network {
	return &Network{
		ID: "n", CIDR: "10.8.0.0/29", Gateway: "10.8.0.1", Enabled: true, Peers: PeersHub,
		Members: []Member{
			{ID: "alice", Name: "alice", IP: "10.8.0.2", Authorized: true},
			{ID: "bob", Name: "bob", Authorized: true, Multi: true, MultiLimit: 2},
		},
	}
}

// TestAdmitSingleRejectsSecondInstance is AuthAlreadyOnline for a non-multi second instance.
func TestAdmitSingleRejectsSecondInstance(t *testing.T) {
	r := NewRegistry(office())
	ip, _, code := r.Admit("alice", "phone")
	if !code.OK() || ip != "10.8.0.2" {
		t.Fatalf("first %s %s", ip, code)
	}
	_, _, code = r.Admit("alice", "laptop")
	if code != wire.AuthAlreadyOnline {
		t.Fatalf("second: %s", code)
	}
}

// TestAdmitSameInstanceReconnects is OKResumed after Release of the same instance.
func TestAdmitSameInstanceReconnects(t *testing.T) {
	r := NewRegistry(office())
	ip1, _, _ := r.Admit("alice", "phone")
	r.Release("alice", "phone")
	ip2, _, code := r.Admit("alice", "phone")
	if code != wire.OKResumed || ip2 != ip1 {
		t.Fatalf("resume %s %s", ip2, code)
	}
}

// TestAdmitMultiStickyThenPool gives the first instance sticky IP, second pool, third AuthLimit.
func TestAdmitMultiStickyThenPool(t *testing.T) {
	n := office()
	n.Members[1].IP = "10.8.0.3"
	r := NewRegistry(n)
	ip1, _, c1 := r.Admit("bob", "a")
	ip2, _, c2 := r.Admit("bob", "b")
	if !c1.OK() || !c2.OK() {
		t.Fatalf("codes %s %s", c1, c2)
	}
	if ip1 != "10.8.0.3" {
		t.Fatalf("sticky to first instance, got %s", ip1)
	}
	if ip2 == ip1 || ip2 == "10.8.0.1" {
		t.Fatalf("second should be pool, got %s", ip2)
	}
	_, _, c3 := r.Admit("bob", "c")
	if c3 != wire.AuthLimit {
		t.Fatalf("limit: %s", c3)
	}
}

// TestAdmitEmptyInstanceGetsID mints an instanceID when Admit gets "".
func TestAdmitEmptyInstanceGetsID(t *testing.T) {
	r := NewRegistry(office())
	_, inst, code := r.Admit("alice", "")
	if !code.OK() || inst == "" {
		t.Fatalf("ephemeral %q %s", inst, code)
	}
}

// TestLeaseHoldThenFreeSticky lets another instance take the sticky IP after Hold.
func TestLeaseHoldThenFreeSticky(t *testing.T) {
	n := office()
	n.Session.LeaseHoldMS = 10
	r := NewRegistry(n)
	now := time.Now()
	r.now = func() time.Time { return now }
	_, _, _ = r.Admit("alice", "phone")
	r.Release("alice", "phone")
	now = now.Add(time.Second)
	ip, _, code := r.Admit("alice", "laptop")
	if !code.OK() || ip != "10.8.0.2" {
		t.Fatalf("after hold sticky is free: %s %s", ip, code)
	}
}

// TestUnauthorized is AuthNotAuthorized when Authorized is false.
func TestUnauthorized(t *testing.T) {
	n := office()
	n.Members[0].Authorized = false
	r := NewRegistry(n)
	_, _, code := r.Admit("alice", "x")
	if code != wire.AuthNotAuthorized {
		t.Fatalf("%s", code)
	}
}

// TestLeasesSurviveRestart: a member reconnecting after a node restart gets
// the address it had before, even when another member connects first.
func TestLeasesSurviveRestart(t *testing.T) {
	path := t.TempDir() + "/leases.json"
	r := NewRegistry(office())
	r.Persist(path)
	ip1, _, _ := r.Admit("bob", "phone") // bob has no fixed IP: next free one
	ip2, _, _ := r.Admit("bob", "laptop")

	// The node restarts: a new registry reads the same file.
	r2 := NewRegistry(office())
	r2.Persist(path)
	// laptop comes back first; before, it took phone's address.
	ip, _, code := r2.Admit("bob", "laptop")
	if code != wire.OKResumed || ip != ip2 {
		t.Fatalf("laptop: %s %s, want %s resumed", ip, code, ip2)
	}
	ip, _, code = r2.Admit("bob", "phone")
	if code != wire.OKResumed || ip != ip1 {
		t.Fatalf("phone: %s %s, want %s resumed", ip, code, ip1)
	}
}

// TestRestoreSkipsStaleLeases: leases of removed members and addresses
// outside the subnet are not brought back.
func TestRestoreSkipsStaleLeases(t *testing.T) {
	path := t.TempDir() + "/leases.json"
	r := NewRegistry(office())
	r.Persist(path)
	r.Admit("bob", "phone")

	n := office()
	n.Members = n.Members[:1] // bob was removed while the node was down
	r2 := NewRegistry(n)
	r2.Persist(path)
	if len(r2.leases) != 0 {
		t.Fatalf("restored %d leases of a removed member", len(r2.leases))
	}
}

// TestRestoredLeaseExpires: a restored lease nobody resumes is freed after
// the usual hold time.
func TestRestoredLeaseExpires(t *testing.T) {
	path := t.TempDir() + "/leases.json"
	r := NewRegistry(office())
	r.Persist(path)
	r.Admit("bob", "phone")

	now := time.Now()
	r2 := NewRegistry(office())
	r2.now = func() time.Time { return now }
	r2.Persist(path)
	now = now.Add(time.Hour)
	r2.Admit("alice", "phone") // any Admit runs gc
	if _, ok := r2.leases[keyOf("bob", "phone")]; ok {
		t.Fatal("restored lease outlived the hold time")
	}
}

package transport

// Tests for overlay CIDR, mesh P2P default, bind/admit, key-not-claim, and Detach.

import (
	"sync"
	"testing"
	"time"

	"github.com/a3app/loft/internal/identity"
	"github.com/a3app/loft/internal/mux"
	"github.com/a3app/loft/internal/netconf"
	"github.com/a3app/loft/internal/wire"
)

// TestOverlayHubIsolates matches the overlay CIDR and leaves hub P2P off.
func TestOverlayHubIsolates(t *testing.T) {
	n := &netconf.Network{ID: "n", CIDR: "10.8.0.0/24", Gateway: "10.8.0.1", Peers: netconf.PeersHub, Enabled: true}
	o := NewOverlay(n)
	if !o.Contains("10.8.0.5") {
		t.Fatal("cidr")
	}
	if o.Contains("8.8.8.8") {
		t.Fatal("public")
	}
	if n.P2PEnabled() {
		t.Fatal("hub p2p default off")
	}
}

// TestOverlayMeshP2PDefault is on for mesh unless the network document sets p2p false.
func TestOverlayMeshP2PDefault(t *testing.T) {
	n := &netconf.Network{ID: "n", CIDR: "10.8.0.0/24", Gateway: "10.8.0.1", Peers: netconf.PeersMesh, Enabled: true}
	if !n.P2PEnabled() || n.PeersMode() != 1 {
		t.Fatal("mesh")
	}
	off := false
	n.P2P = &off
	if n.P2PEnabled() {
		t.Fatal("override")
	}
}

// TestOverlayBindVLANSegment stamps the member's segment gateway and DNS.
func TestOverlayBindVLANSegment(t *testing.T) {
	n := &netconf.Network{
		ID: "n", CIDR: "10.8.0.0/24", Gateway: "10.8.0.1", Enabled: true,
		VLANs: []netconf.VLAN{{
			ID: 10, Name: "staff", CIDR: "10.9.0.0/24", Gateway: "10.9.0.1",
			PoolFrom: "10.9.0.10", PoolTo: "10.9.0.200",
		}},
		Members: []netconf.Member{{ID: "alice", Authorized: true, VLAN: 10, IP: "10.9.0.14", Multi: true, MultiLimit: 2}},
	}
	if err := n.Validate(); err != nil {
		t.Fatal(err)
	}
	o := NewOverlay(n)
	if !o.Contains("10.9.0.5") || !o.Contains("10.8.0.5") || o.Contains("10.7.0.1") {
		t.Fatal("segment cidrs")
	}
	r := identity.NewRing()
	r.Put("alice", "lt_alice")
	old := AuthKeys
	defer func() { AuthKeys = old }()
	AuthKeys = r
	st := wire.Status{}
	o.Bind(&st, wire.Hello{MemberID: "alice"}, nil, nil)
	if st.Code != wire.OK || st.IP != "10.9.0.14" || st.Gateway != "10.9.0.1" || int(st.VLAN) != 10 {
		t.Fatalf("%+v", st)
	}
	if len(st.DNS) != 1 || st.DNS[0] != "10.9.0.1" {
		t.Fatalf("dns %+v", st.DNS)
	}
	st = wire.Status{}
	o.BindL3(&st, wire.Hello{MemberID: "alice", InstanceID: "l3"}, nil, nil)
	if st.Code != wire.OK || st.Gateway != "10.9.0.1" || int(st.VLAN) != 10 {
		t.Fatalf("l3 %+v", st)
	}
}

// TestOverlayBindEphemeralOnlyWithoutKeys allocates a pool IP with no keys; named members need a key.
func TestOverlayBindEphemeralOnlyWithoutKeys(t *testing.T) {
	n := &netconf.Network{
		ID: "n", CIDR: "10.8.0.0/24", Gateway: "10.8.0.1",
		Enabled: true,
		Members: []netconf.Member{{ID: "alice", Authorized: true, Multi: true, MultiLimit: 8}},
	}
	o := NewOverlay(n)
	old := AuthKeys
	defer func() { AuthKeys = old }()

	AuthKeys = nil
	st := wire.Status{}
	o.Bind(&st, wire.Hello{}, nil, nil)
	if st.Code != wire.OK || st.IP == "" {
		t.Fatalf("ephemeral without keys: %+v", st)
	}
	if len(st.DNS) != 1 || st.DNS[0] != "10.8.0.1" {
		t.Fatalf("dns %+v", st.DNS)
	}

	r := identity.NewRing()
	r.Put("alice", "lt_alice")
	AuthKeys = r
	st = wire.Status{}
	o.Bind(&st, wire.Hello{}, nil, nil)
	if st.Code != wire.AuthBadKey {
		t.Fatalf("empty member with keys: %v", st.Code)
	}
	st = wire.Status{}
	o.Bind(&st, wire.Hello{MemberID: "alice"}, nil, nil)
	if st.Code != wire.OK || st.IP == "" {
		t.Fatalf("named member: %+v", st)
	}
}

// TestStampOverlayAuthBindsKeyNotClaim uses the member key, not Hello.MemberID, for the lease.
func TestStampOverlayAuthBindsKeyNotClaim(t *testing.T) {
	n := &netconf.Network{
		ID: "n", CIDR: "10.8.0.0/24", Gateway: "10.8.0.1",
		Enabled: true,
		Members: []netconf.Member{
			{ID: "alice", Authorized: true, Multi: true, MultiLimit: 8, IP: "10.8.0.10"},
			{ID: "bob", Authorized: true, Multi: true, MultiLimit: 8, IP: "10.8.0.11"},
		},
	}
	oldNet, oldKeys := NetSwitch(), AuthKeys
	defer func() { SetNetSwitch(oldNet); AuthKeys = oldKeys }()
	SetNetSwitch(NewOverlay(n))
	r := identity.NewRing()
	r.Put("alice", "lt_alice")
	r.Put("bob", "lt_bob")
	AuthKeys = r

	hello := wire.Hello{MemberID: "bob"}
	st := wire.Status{}
	stampOverlayAuth(&st, hello.Encode(), nil, nil, []byte("lt_alice"))
	if st.Code != wire.OK || st.IP != "10.8.0.10" {
		t.Fatalf("want alice sticky, got code=%v ip=%s", st.Code, st.IP)
	}

	st = wire.Status{}
	stampOverlayAuth(&st, hello.Encode(), nil, nil, []byte("lt_unknown"))
	if st.Code != wire.AuthBadKey {
		t.Fatalf("unknown key: %v", st.Code)
	}
}

// TestOverlayPeersReportsBulkFront copies the GET session's live bulk front.
func TestOverlayPeersReportsBulkFront(t *testing.T) {
	n := &netconf.Network{
		ID: "n", CIDR: "10.8.0.0/29", Gateway: "10.8.0.1",
		Enabled: true, Peers: netconf.PeersHub,
		Members: []netconf.Member{{ID: "alice", Name: "alice", IP: "10.8.0.2", Authorized: true}},
	}
	o := NewOverlay(n)
	sess := mux.NewServer()
	st := wire.Status{}
	o.Bind(&st, wire.Hello{MemberID: "alice", InstanceID: "phone"}, sess, nil)
	peers := o.Peers()
	if len(peers) != 1 || peers[0].Front != "" {
		t.Fatalf("unattached %+v", peers)
	}
	ss := &serverSess{id: "aabbccddeeff001122334455", mux: sess, bulk: wire.FrontH2}
	ss.cond = sync.NewCond(&ss.mu)
	ticketTab.Store(ss.id, ss)
	t.Cleanup(func() { ticketTab.Delete(ss.id) })
	peers = o.Peers()
	if len(peers) != 1 || peers[0].Front != wire.FrontH2 || peers[0].Member != "alice" {
		t.Fatalf("%+v", peers)
	}
}

// TestOverlayDetachReleasesLease lets a second instance admit after Detach.
func TestOverlayDetachReleasesLease(t *testing.T) {
	n := &netconf.Network{
		ID: "n", CIDR: "10.8.0.0/29", Gateway: "10.8.0.1",
		Enabled: true, Peers: netconf.PeersHub,
		Members: []netconf.Member{{ID: "alice", Name: "alice", IP: "10.8.0.2", Authorized: true}},
	}
	o := NewOverlay(n)
	sess := mux.NewServer()
	st := wire.Status{}
	o.Bind(&st, wire.Hello{MemberID: "alice", InstanceID: "phone"}, sess, nil)
	if !st.Code.OK() || st.IP != "10.8.0.2" {
		t.Fatalf("bind %+v", st)
	}
	_, _, code := o.Reg.Admit("alice", "laptop")
	if code != wire.AuthAlreadyOnline {
		t.Fatalf("still online: %s", code)
	}
	o.Detach(sess)
	_, _, code = o.Reg.Admit("alice", "laptop")
	if !code.OK() {
		t.Fatalf("after leave still blocked: %s", code)
	}
}

// TestOverlayResumeKeepsForwarding: same instance, new mux; Detach of the old
// session must not wipe tab[ip] or mark the lease offline.
func TestOverlayResumeKeepsForwarding(t *testing.T) {
	n := &netconf.Network{
		ID: "n", CIDR: "10.8.0.0/29", Gateway: "10.8.0.1",
		Enabled: true, Peers: netconf.PeersHub,
		Members: []netconf.Member{{ID: "alice", Name: "alice", IP: "10.8.0.2", Authorized: true}},
	}
	o := NewOverlay(n)
	oldS := mux.NewServer()
	newS := mux.NewServer()
	st := wire.Status{}
	o.Bind(&st, wire.Hello{MemberID: "alice", InstanceID: "phone"}, oldS, nil)
	if !st.Code.OK() || st.IP != "10.8.0.2" {
		t.Fatalf("bind %+v", st)
	}
	st2 := wire.Status{}
	o.Bind(&st2, wire.Hello{MemberID: "alice", InstanceID: "phone"}, newS, nil)
	if st2.Code != wire.OKResumed || st2.IP != "10.8.0.2" {
		t.Fatalf("resume %+v", st2)
	}
	o.Detach(oldS)
	if o.SessionIP(newS) != "10.8.0.2" {
		t.Fatal("detach old wiped new")
	}
	if o.SessionIP(oldS) != "" {
		t.Fatal("old still attached")
	}
	_, _, code := o.Reg.Admit("alice", "laptop")
	if code != wire.AuthAlreadyOnline {
		t.Fatalf("lease dropped: %s", code)
	}
}

func TestLearnedMaskPub(t *testing.T) {
	pub := make([]byte, 32)
	for i := range pub {
		pub[i] = byte(i + 1)
	}
	RememberVis(wire.Status{IP: "10.8.0.6", MaskPub: pub})
	t.Cleanup(func() {
		visMu.Lock()
		visMaskPub = nil
		visMu.Unlock()
		RememberVis(wire.Status{})
	})
	got := LearnedMaskPub()
	if len(got) != 64 {
		t.Fatalf("%q", got)
	}
	RememberVis(wire.Status{IP: "10.8.0.7"})
	if LearnedMaskPub() != got {
		t.Fatal("empty Status dropped mask")
	}
}

// TestOverlayGatewayVis keeps STATUS gw for the popover Route row.
func TestOverlayGatewayVis(t *testing.T) {
	RememberVis(wire.Status{IP: "10.8.0.6", Gateway: "10.8.0.1"})
	t.Cleanup(func() { RememberVis(wire.Status{}) })
	if OverlayIP() != "10.8.0.6" || OverlayGateway() != "10.8.0.1" {
		t.Fatalf("%s %s", OverlayIP(), OverlayGateway())
	}
	RememberGateway("172.16.223.1")
	if OverlayGateway() != "172.16.223.1" {
		t.Fatal(OverlayGateway())
	}
}

func TestOverlayTTLClosesSession(t *testing.T) {
	n := &netconf.Network{
		ID: "n", CIDR: "10.8.0.0/24", Gateway: "10.8.0.1", Peers: netconf.PeersHub, Enabled: true,
		Session: netconf.SessionPolicy{TTLMS: 40},
		Members: []netconf.Member{{ID: "alice", IP: "10.8.0.2", Authorized: true}},
	}
	o := NewOverlay(n)
	sess := mux.NewServer()
	st := wire.Status{}
	o.Bind(&st, wire.Hello{MemberID: "alice", InstanceID: "phone"}, sess, nil)
	if st.SessionTTLMS != 40 {
		t.Fatalf("ttl %d", st.SessionTTLMS)
	}
	deadline := time.Now().Add(time.Second)
	for time.Now().Before(deadline) {
		if sess.Closed() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("session still open")
}

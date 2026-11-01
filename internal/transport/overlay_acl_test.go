package transport

// Overlay TCP Forward: hub isolation, tag ACL, managed CIDR behind a member.

import (
	"net"
	"testing"
	"time"

	"github.com/a3app/loft/internal/mux"
	"github.com/a3app/loft/internal/netconf"
	"github.com/a3app/loft/internal/wire"
)

func pumpPair(a, b *mux.Session) {
	go func() {
		for {
			fs := a.Pull(64*1024, 50*time.Millisecond)
			if len(fs) > 0 {
				b.Push(fs)
			}
		}
	}()
	go func() {
		for {
			fs := b.Pull(64*1024, 50*time.Millisecond)
			if len(fs) > 0 {
				a.Push(fs)
			}
		}
	}()
}

func bindMember(t *testing.T, o *Overlay, id string, sess *mux.Session) string {
	t.Helper()
	st := wire.Status{}
	o.Bind(&st, wire.Hello{MemberID: id, InstanceID: id + "-1"}, sess, nil)
	if !st.Code.OK() || st.IP == "" {
		t.Fatalf("%s bind %+v", id, st)
	}
	return st.IP
}

func TestForwardHubDropsNeighbor(t *testing.T) {
	n := &netconf.Network{
		ID: "n", CIDR: "10.8.0.0/24", Gateway: "10.8.0.1", Peers: netconf.PeersHub, Enabled: true,
		Members: []netconf.Member{
			{ID: "a", IP: "10.8.0.2", Authorized: true},
			{ID: "b", IP: "10.8.0.3", Authorized: true},
		},
	}
	o := NewOverlay(n)
	aCli, aSrv := mux.NewClient(), mux.NewServer()
	bCli, bSrv := mux.NewClient(), mux.NewServer()
	pumpPair(aCli, aSrv)
	pumpPair(bCli, bSrv)
	bindMember(t, o, "a", aSrv)
	bip := bindMember(t, o, "b", bSrv)
	st, err := aCli.Open(net.JoinHostPort(bip, "9"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := aSrv.Accept()
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { o.Forward(got, bip, "9"); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("forward hung")
	}
	_ = st.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	buf := make([]byte, 8)
	if _, err := st.Read(buf); err == nil {
		t.Fatal("hub leaked member tcp")
	}
}

func TestForwardTagACLMesh(t *testing.T) {
	n := &netconf.Network{
		ID: "n", CIDR: "10.8.0.0/24", Gateway: "10.8.0.1", Peers: netconf.PeersMesh, Enabled: true,
		ACLDefault: "drop",
		Rules: []netconf.Rule{
			{Action: "accept", Proto: "tcp", SrcTag: "ops", DstTag: "db", Dport: []int{5432}},
		},
		Members: []netconf.Member{
			{ID: "ops", IP: "10.8.0.2", Authorized: true, Tags: []string{"ops"}},
			{ID: "db", IP: "10.8.0.5", Authorized: true, Tags: []string{"db"}},
		},
	}
	o := NewOverlay(n)
	aCli, aSrv := mux.NewClient(), mux.NewServer()
	bCli, bSrv := mux.NewClient(), mux.NewServer()
	pumpPair(aCli, aSrv)
	pumpPair(bCli, bSrv)
	bindMember(t, o, "ops", aSrv)
	dip := bindMember(t, o, "db", bSrv)

	st, err := aCli.Open(net.JoinHostPort(dip, "5432"))
	if err != nil {
		t.Fatal(err)
	}
	got, err := aSrv.Accept()
	if err != nil {
		t.Fatal(err)
	}
	go o.Forward(got, dip, "5432")
	in, err := bCli.Accept()
	if err != nil {
		t.Fatal(err)
	}
	if in.Target() != net.JoinHostPort("127.0.0.1", "5432") {
		t.Fatalf("target %s", in.Target())
	}
	_ = in.Close()
	_ = st.Close()

	st2, err := aCli.Open(net.JoinHostPort(dip, "22"))
	if err != nil {
		t.Fatal(err)
	}
	got2, err := aSrv.Accept()
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() { o.Forward(got2, dip, "22"); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("ssh forward hung")
	}
	_ = st2.SetReadDeadline(time.Now().Add(200 * time.Millisecond))
	buf := make([]byte, 8)
	if _, err := st2.Read(buf); err == nil {
		t.Fatal("ssh under default deny")
	}
}

func TestHandleRoutedCIDRSplicesOriginalHost(t *testing.T) {
	n := &netconf.Network{
		ID: "n", CIDR: "10.8.0.0/24", Gateway: "10.8.0.1", Peers: netconf.PeersHub, Enabled: true,
		Routes: []netconf.Route{{Dst: "192.168.10.0/24", Via: "10.8.0.5"}},
		Members: []netconf.Member{
			{ID: "ops", IP: "10.8.0.2", Authorized: true, Tags: []string{"ops"}},
			{ID: "lab", IP: "10.8.0.5", Authorized: true, Tags: []string{"lab"}},
		},
	}
	o := NewOverlay(n)
	aCli, aSrv := mux.NewClient(), mux.NewServer()
	bCli, bSrv := mux.NewClient(), mux.NewServer()
	pumpPair(aCli, aSrv)
	pumpPair(bCli, bSrv)
	aip := bindMember(t, o, "ops", aSrv)
	lip := bindMember(t, o, "lab", bSrv)
	if lip != "10.8.0.5" {
		t.Fatalf("lab %s", lip)
	}
	stLab := wire.Status{IP: lip}
	o.stampRoutes(&stLab)
	if len(stLab.Routes) != 0 {
		t.Fatalf("lab should not install its own LAN %+v", stLab.Routes)
	}
	stOps := wire.Status{IP: aip}
	o.stampRoutes(&stOps)
	if len(stOps.Routes) != 1 || stOps.Routes[0].Dst != "192.168.10.0/24" {
		t.Fatalf("ops routes %+v", stOps.Routes)
	}

	st, err := aCli.Open("192.168.10.9:22")
	if err != nil {
		t.Fatal(err)
	}
	got, err := aSrv.Accept()
	if err != nil {
		t.Fatal(err)
	}
	go o.Handle(got)
	in, err := bCli.Accept()
	if err != nil {
		t.Fatal(err)
	}
	if in.Target() != "192.168.10.9:22" {
		t.Fatalf("want original host, got %s", in.Target())
	}
	_ = in.Close()
	_ = st.Close()
}

func TestStampRoutesMemberAndTag(t *testing.T) {
	n := &netconf.Network{
		ID: "n", CIDR: "10.8.0.0/24", Gateway: "10.8.0.1", Peers: netconf.PeersHub, Enabled: true,
		Routes: []netconf.Route{
			{Dst: "192.168.10.0/24", Member: "lab"},
			{Dst: "10.20.0.0/16", Tag: "lab"},
		},
		Members: []netconf.Member{
			{ID: "ops", IP: "10.8.0.2", Authorized: true, Tags: []string{"ops"}},
			{ID: "lab", IP: "10.8.0.5", Authorized: true, Tags: []string{"lab"}},
		},
	}
	o := NewOverlay(n)
	aCli, aSrv := mux.NewClient(), mux.NewServer()
	bCli, bSrv := mux.NewClient(), mux.NewServer()
	pumpPair(aCli, aSrv)
	pumpPair(bCli, bSrv)
	_ = bindMember(t, o, "ops", aSrv)
	lip := bindMember(t, o, "lab", bSrv)
	stOps := wire.Status{IP: "10.8.0.2"}
	o.stampRoutes(&stOps)
	if len(stOps.Routes) != 2 || stOps.Routes[0].Via != lip || stOps.Routes[1].Via != lip {
		t.Fatalf("ops routes %+v lab %s", stOps.Routes, lip)
	}
	stLab := wire.Status{IP: lip}
	o.stampRoutes(&stLab)
	if len(stLab.Routes) != 0 {
		t.Fatalf("lab own %+v", stLab.Routes)
	}
}

func TestStampPolicyDefaultDeny(t *testing.T) {
	n := &netconf.Network{
		ID: "n", CIDR: "10.8.0.0/24", Gateway: "10.8.0.1", Peers: netconf.PeersMesh, Enabled: true,
		ACLDefault: "drop",
		Rules:      []netconf.Rule{{Action: "accept", SrcTag: "ops", DstTag: "db", Proto: "tcp", Dport: []int{5432}}},
		Members: []netconf.Member{
			{ID: "ops", IP: "10.8.0.2", Authorized: true, Tags: []string{"ops"}},
			{ID: "db", IP: "10.8.0.5", Authorized: true, Tags: []string{"db"}},
		},
		Session: netconf.SessionPolicy{TTLMS: 60_000},
	}
	o := NewOverlay(n)
	aCli, aSrv := mux.NewClient(), mux.NewServer()
	bCli, bSrv := mux.NewClient(), mux.NewServer()
	pumpPair(aCli, aSrv)
	pumpPair(bCli, bSrv)
	st := wire.Status{}
	o.Bind(&st, wire.Hello{MemberID: "ops", InstanceID: "ops-1"}, aSrv, nil)
	if !st.Policy.Present || st.Policy.ACLDefault != 1 || len(st.Policy.OwnTags) != 1 || st.Policy.OwnTags[0] != "ops" {
		t.Fatalf("policy %+v", st.Policy)
	}
	if len(st.Policy.Rules) != 1 || st.Policy.Rules[0].SrcTag != "ops" || st.SessionTTLMS != 60_000 {
		t.Fatalf("rules %+v ttl %d", st.Policy.Rules, st.SessionTTLMS)
	}
	_ = bindMember(t, o, "db", bSrv)
	st2 := wire.Status{IP: st.IP}
	o.stampPolicy(&st2, "ops")
	if len(st2.Policy.Peers) != 1 || st2.Policy.Peers[0].Tags[0] != "db" {
		t.Fatalf("peers %+v", st2.Policy.Peers)
	}
}

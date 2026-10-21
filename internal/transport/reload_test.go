package transport

import (
	"testing"

	"github.com/a3app/loft/internal/identity"
	"github.com/a3app/loft/internal/mux"
	"github.com/a3app/loft/internal/netconf"
)

const reloadBase = `network main
enable
ipif overlay 10.77.0.0/24
  gateway 10.77.0.1
  peers hub
create member alice
config member alice key_sha256 aa
enable member alice
create member bob
config member bob key_sha256 bb
enable member bob
`

func reloadNet(t *testing.T, extra string) *netconf.Network {
	t.Helper()
	n, err := netconf.ParseCommands(reloadBase + extra)
	if err != nil {
		t.Fatal(err)
	}
	return n
}

func TestReloadKeepsUntouchedMembers(t *testing.T) {
	o := NewOverlay(reloadNet(t, ""))
	a, b := mux.NewServer(), mux.NewServer()
	o.attach("10.77.0.10", 0, a, nil, "alice", "i1")
	o.attach("10.77.0.11", 0, b, nil, "bob", "i2")

	// Add carol: nobody connected is concerned.
	if n := o.Reload(reloadNet(t, "create member carol\nconfig member carol key_sha256 cc\nenable member carol\n")); n != 0 || a.Closed() || b.Closed() {
		t.Fatalf("adding a member closed %d sessions", n)
	}
	if o.N().MemberByID("carol") == nil {
		t.Fatal("new network not in use")
	}
	// Disable bob: only bob goes.
	n := o.Reload(reloadNet(t, "disable member bob\n"))
	if n != 1 || a.Closed() || !b.Closed() {
		t.Fatalf("disable bob: closed %d, alice %v bob %v", n, a.Closed(), b.Closed())
	}
	if o.SessionIP(b) != "" || o.SessionIP(a) != "10.77.0.10" {
		t.Fatal("bob still attached or alice lost her address")
	}
}

func TestReloadNetworkWide(t *testing.T) {
	o := NewOverlay(reloadNet(t, ""))
	a := mux.NewServer()
	o.attach("10.77.0.10", 0, a, nil, "alice", "i1")
	text := "network main\nenable\nipif overlay 10.77.0.0/24\n  gateway 10.77.0.1\n  mtu 1200\n  peers hub\ncreate member alice\nconfig member alice key_sha256 aa\nenable member alice\ncreate member bob\nconfig member bob key_sha256 bb\nenable member bob\n"
	n2, _ := netconf.ParseCommands(text)
	if n := o.Reload(n2); n != 1 || !a.Closed() {
		t.Fatalf("MTU change must reconnect everyone, closed %d", n)
	}
}

func TestReloadRules(t *testing.T) {
	// hub without p2p: rules are the node's own business, sessions stay.
	o := NewOverlay(reloadNet(t, ""))
	a := mux.NewServer()
	o.attach("10.77.0.10", 0, a, nil, "alice", "i1")
	if n := o.Reload(reloadNet(t, "ip access-list overlay\n  deny tcp any any eq 22\n")); n != 0 || a.Closed() {
		t.Fatalf("rule change without p2p closed %d", n)
	}
	if len(o.N().Rules) != 1 {
		t.Fatal("rules not applied")
	}
}

func TestReloadRegistryDropsGoneMembers(t *testing.T) {
	n := reloadNet(t, "")
	o := NewOverlay(n)
	ip, inst, code := o.Reg.Admit("bob", "x")
	if !code.OK() || ip == "" {
		t.Fatalf("admit %v", code)
	}
	o.Reg.Release("bob", inst)
	without, _ := netconf.ParseCommands("network main\nenable\nipif overlay 10.77.0.0/24\n  gateway 10.77.0.1\ncreate member alice\nconfig member alice key_sha256 aa\nenable member alice\n")
	o.Reload(without)
	if _, _, code := o.Reg.Admit("bob", inst); code.OK() {
		t.Fatal("removed member admitted after reload")
	}
}

func TestRingReplace(t *testing.T) {
	r := identity.NewRing()
	r.Put("old", "lt_old")
	src := identity.NewRing()
	src.Put("new", "lt_new")
	r.Replace(src)
	if r.Len() != 1 || r.IDFor([]byte("lt_new")) != "new" || r.IDFor([]byte("lt_old")) != "" {
		t.Fatal("ring not replaced")
	}
	src.Put("later", "lt_later")
	if r.Len() != 1 {
		t.Fatal("Replace kept a reference to src")
	}
}

package adm

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/a3app/loft/internal/identity"
	"github.com/a3app/loft/server/panel/admproto"
)

const conf1 = `network main
enable
ipif overlay 10.77.0.0/16
  gateway 10.77.0.1
  mtu 1280
  peers vlan
create member alice-phone
config member alice-phone key_sha256 %s
enable member alice-phone
create member bob
config member bob key_sha256 %s
enable member bob
`

// fakeNode plays systemd: restart succeeds, and is-active reports state.
type fakeNode struct {
	calls  []string
	active func() string
	reload bool // unit has ExecReload
}

func (f *fakeNode) run(_ context.Context, args ...string) (string, error) {
	f.calls = append(f.calls, strings.Join(args, " "))
	switch args[0] {
	case "is-active":
		return f.active(), nil
	case "show":
		if len(args) > 2 && args[2] == "--property=CanReload" {
			if f.reload {
				return "yes", nil
			}
			return "no", nil
		}
		return "ActiveState=" + f.active() + "\nActiveEnterTimestamp=Tue 2026-10-20 11:28:00 UTC", nil
	}
	return "", nil
}

func setup(t *testing.T) (*Ops, *fakeNode, string, string) {
	t.Helper()
	dir := t.TempDir()
	fn := &fakeNode{active: func() string { return "active" }}
	o := &Ops{Dir: dir, Unit: "loft-node.service", UID: -1, GID: -1, Systemctl: fn.run, Settle: time.Millisecond}
	ka, sa, _ := identity.NewKey()
	kb, sb, _ := identity.NewKey()
	os.WriteFile(filepath.Join(dir, "members.keys"), []byte("alice-phone: "+ka+"\nbob: "+kb+"\n"), 0o600)
	os.WriteFile(filepath.Join(dir, "network.conf"), []byte(fmt.Sprintf(conf1, sa, sb)), 0o640)
	return o, fn, sa, sb
}

func TestNetworkGetApply(t *testing.T) {
	o, fn, sa, sb := setup(t)
	cur, err := o.NetworkGet()
	if err != nil || !strings.Contains(cur.Text, "network main") || len(cur.SHA256) != 64 {
		t.Fatalf("get %+v %v", cur, err)
	}
	next := strings.Replace(cur.Text, "peers vlan", "peers mesh", 1)
	got, err := o.NetworkApply(context.Background(), admproto.NetworkApply{Text: next, Expect: cur.SHA256})
	if err != nil {
		t.Fatal(err)
	}
	if got.SHA256 == cur.SHA256 || !strings.Contains(got.Text, "peers mesh") {
		t.Fatalf("apply result %+v", got)
	}
	disk, _ := os.ReadFile(filepath.Join(o.Dir, "network.conf"))
	prev, _ := os.ReadFile(filepath.Join(o.Dir, "network.conf.prev"))
	if string(disk) != next || string(prev) != cur.Text {
		t.Fatal("disk or .prev wrong")
	}
	if st, _ := os.Stat(filepath.Join(o.Dir, "network.conf")); st.Mode().Perm() != 0o640 {
		t.Fatalf("mode %v", st.Mode())
	}
	if !contains(fn.calls, "restart loft-node.service") {
		t.Fatalf("no restart: %v", fn.calls)
	}
	// Stale editor: same Expect again is refused.
	if _, err := o.NetworkApply(context.Background(), admproto.NetworkApply{Text: next, Expect: cur.SHA256}); err == nil || !strings.Contains(err.Error(), "changed since") {
		t.Fatalf("stale apply: %v", err)
	}
	_, _ = sa, sb
}

func TestNetworkApplyInvalid(t *testing.T) {
	o, fn, _, _ := setup(t)
	cur, _ := o.NetworkGet()
	_, err := o.NetworkApply(context.Background(), admproto.NetworkApply{Text: cur.Text + "ip access-list overlay\n  permit tcp any any eq 99999\n", Expect: cur.SHA256})
	if err == nil || !strings.Contains(err.Error(), `line 14: acl: bad port "99999"`) {
		t.Fatalf("want node's own error with line, got %v", err)
	}
	if len(fn.calls) != 0 {
		t.Fatal("invalid config touched the node")
	}
	disk, _ := os.ReadFile(filepath.Join(o.Dir, "network.conf"))
	if string(disk) != cur.Text {
		t.Fatal("invalid config written")
	}
}

func TestNetworkApplyRollback(t *testing.T) {
	o, fn, _, _ := setup(t)
	cur, _ := o.NetworkGet()
	keysBefore, _ := os.ReadFile(filepath.Join(o.Dir, "members.keys"))
	n := 0
	fn.active = func() string { // first restart fails, the rollback restart works
		n++
		if n == 1 {
			return "failed"
		}
		return "active"
	}
	// Drop bob: his key would be pruned, then must come back with the old config.
	next := cur.Text[:strings.Index(cur.Text, "create member bob")]
	_, err := o.NetworkApply(context.Background(), admproto.NetworkApply{Text: next, Expect: cur.SHA256})
	if err == nil || !strings.Contains(err.Error(), "previous one is back") {
		t.Fatalf("want rollback error, got %v", err)
	}
	disk, _ := os.ReadFile(filepath.Join(o.Dir, "network.conf"))
	keys, _ := os.ReadFile(filepath.Join(o.Dir, "members.keys"))
	if string(disk) != cur.Text || string(keys) != string(keysBefore) {
		t.Fatalf("not rolled back:\n%s\n%s", disk, keys)
	}
}

func TestPruneKeys(t *testing.T) {
	o, _, _, _ := setup(t)
	cur, _ := o.NetworkGet()
	next := cur.Text[:strings.Index(cur.Text, "create member bob")]
	if _, err := o.NetworkApply(context.Background(), admproto.NetworkApply{Text: next, Expect: cur.SHA256}); err != nil {
		t.Fatal(err)
	}
	keys, _ := os.ReadFile(filepath.Join(o.Dir, "members.keys"))
	if !strings.Contains(string(keys), "alice-phone:") || strings.Contains(string(keys), "bob:") {
		t.Fatalf("keys after removing bob:\n%s", keys)
	}
	if st, _ := os.Stat(filepath.Join(o.Dir, "members.keys")); st.Mode().Perm() != 0o600 {
		t.Fatalf("members.keys mode %v", st.Mode())
	}
}

func TestMemberKey(t *testing.T) {
	o, _, _, _ := setup(t)
	k, err := o.MemberKey(admproto.MemberKey{ID: "carol"})
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(k.Key, "lt_") || identity.Sum(k.Key) != k.KeySHA256 {
		t.Fatalf("issued %+v", k)
	}
	r, _ := identity.Load(filepath.Join(o.Dir, "members.keys"))
	if r.Len() != 3 {
		t.Fatalf("ring len %d", r.Len())
	}
	// Apply the new member: its key survives pruning.
	cur, _ := o.NetworkGet()
	next := cur.Text + "create member carol\nconfig member carol key_sha256 " + k.KeySHA256 + "\nenable member carol\n"
	if _, err := o.NetworkApply(context.Background(), admproto.NetworkApply{Text: next, Expect: cur.SHA256}); err != nil {
		t.Fatal(err)
	}
	r, _ = identity.Load(filepath.Join(o.Dir, "members.keys"))
	if r.Len() != 3 {
		t.Fatalf("carol pruned, ring len %d", r.Len())
	}
	for _, bad := range []string{"", "-x", "a b", strings.Repeat("a", 65), "../etc"} {
		if _, err := o.MemberKey(admproto.MemberKey{ID: bad}); err == nil {
			t.Fatalf("id %q accepted", bad)
		}
	}
}

func TestMemberGet(t *testing.T) {
	o, _, _, _ := setup(t)
	got, err := o.MemberGet(admproto.MemberKey{ID: "alice-phone"})
	if err != nil || !strings.HasPrefix(got.Key, "lt_") || identity.Sum(got.Key) != got.KeySHA256 {
		t.Fatalf("get %+v %v", got, err)
	}
	if _, err := o.MemberGet(admproto.MemberKey{ID: "nobody"}); err == nil || !strings.Contains(err.Error(), "no stored key") {
		t.Fatalf("missing: %v", err)
	}
	k, err := o.MemberKey(admproto.MemberKey{ID: "carol"})
	if err != nil {
		t.Fatal(err)
	}
	got, err = o.MemberGet(admproto.MemberKey{ID: "carol"})
	if err != nil || got.Key != k.Key || got.KeySHA256 != k.KeySHA256 {
		t.Fatalf("after issue %+v %v want %s", got, err, k.Key)
	}
	if _, err := o.MemberGet(admproto.MemberKey{ID: "../etc"}); err == nil {
		t.Fatal("bad id accepted")
	}
}

// TestMemberKeyReissueKeepsOldUntilApply is a past field failure:
// POST /api/members/key rewrote members.keys, a restart Filter'd by the
// still-applied key_sha256, and the ring went to 0. The old token must
// stay until apply prunes it.
func TestMemberKeyReissueKeepsOldUntilApply(t *testing.T) {
	o, _, sa, _ := setup(t)
	old, err := o.MemberGet(admproto.MemberKey{ID: "alice-phone"})
	if err != nil {
		t.Fatal(err)
	}
	k, err := o.MemberKey(admproto.MemberKey{ID: "alice-phone"})
	if err != nil {
		t.Fatal(err)
	}
	if k.Key == old.Key {
		t.Fatal("reissue returned the old key")
	}
	r, err := identity.Load(filepath.Join(o.Dir, "members.keys"))
	if err != nil {
		t.Fatal(err)
	}
	if r.Len() != 2 || r.IDFor([]byte(old.Key)) != "alice-phone" || r.IDFor([]byte(k.Key)) != "alice-phone" {
		t.Fatalf("disk lost the live key: len=%d old=%q new=%q", r.Len(), r.IDFor([]byte(old.Key)), r.IDFor([]byte(k.Key)))
	}
	r.Filter(map[string]string{"alice-phone": sa, "bob": identity.Sum(string(r.TokenFor("bob")))})
	if r.Len() != 2 || r.IDFor([]byte(old.Key)) != "alice-phone" || r.IDFor([]byte(k.Key)) != "" {
		t.Fatalf("reload before apply dropped alice: len=%d", r.Len())
	}
	cur, _ := o.NetworkGet()
	next := strings.Replace(cur.Text, "key_sha256 "+sa, "key_sha256 "+k.KeySHA256, 1)
	if _, err := o.NetworkApply(context.Background(), admproto.NetworkApply{Text: next, Expect: cur.SHA256}); err != nil {
		t.Fatal(err)
	}
	r, _ = identity.Load(filepath.Join(o.Dir, "members.keys"))
	if r.Len() != 2 || r.IDFor([]byte(k.Key)) != "alice-phone" || r.IDFor([]byte(old.Key)) != "" {
		t.Fatalf("apply did not prune old: len=%d new=%q old=%q", r.Len(), r.IDFor([]byte(k.Key)), r.IDFor([]byte(old.Key)))
	}
}

func TestNetworkFirstAndLegacy(t *testing.T) {
	dir := t.TempDir()
	fn := &fakeNode{active: func() string { return "active" }}
	o := &Ops{Dir: dir, Unit: "loft-node.service", UID: -1, GID: -1, Systemctl: fn.run, Settle: time.Millisecond}
	cur, err := o.NetworkGet()
	if err != nil || cur.Text != "" || cur.SHA256 != "" {
		t.Fatalf("empty dir: %+v %v", cur, err)
	}
	first := "network loft\nipif overlay 10.8.0.0/24\n"
	if _, err := o.NetworkApply(context.Background(), admproto.NetworkApply{Text: first, Expect: "x"}); err == nil {
		t.Fatal("Expect must be empty for a new file")
	}
	if _, err := o.NetworkApply(context.Background(), admproto.NetworkApply{Text: first}); err != nil {
		t.Fatal(err)
	}
	os.Remove(filepath.Join(dir, "network.conf"))
	os.WriteFile(filepath.Join(dir, "network.yaml"), []byte("id: old\ncidr: 10.9.0.0/24\nenabled: true\n"), 0o640)
	cur, err = o.NetworkGet()
	if err != nil || !cur.Legacy || !strings.HasPrefix(cur.Text, "network old\n") {
		t.Fatalf("legacy %+v %v", cur, err)
	}
	if _, err := o.NetworkApply(context.Background(), admproto.NetworkApply{Text: cur.Text, Expect: cur.SHA256}); err != nil {
		t.Fatalf("legacy apply: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dir, "network.conf")); err != nil {
		t.Fatal("network.conf not written over legacy yaml")
	}
}

func TestServerSocket(t *testing.T) {
	o, _, _, _ := setup(t)
	sd, err := os.MkdirTemp("/tmp", "lpadm")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(sd)
	sock := filepath.Join(sd, "adm.sock")
	ln, err := net.Listen("unix", sock)
	if err != nil {
		t.Fatal(err)
	}
	srv := &Server{Ops: o}
	go srv.Serve(ln)
	defer ln.Close()
	c := admproto.Client{Path: sock, By: "admin"}
	ctx := context.Background()
	var p admproto.Ping
	if err := c.Call(ctx, admproto.OpPing, nil, &p); err != nil || p.Version == "" {
		t.Fatalf("ping %+v %v", p, err)
	}
	var n admproto.Network
	if err := c.Call(ctx, admproto.OpNetworkGet, nil, &n); err != nil || n.SHA256 == "" {
		t.Fatalf("get %v", err)
	}
	if err := c.Call(ctx, "rm -rf", nil, nil); err == nil || !strings.Contains(err.Error(), "unknown operation") {
		t.Fatalf("unknown op: %v", err)
	}
	if err := c.Call(ctx, admproto.OpMemberKey, map[string]any{"id": "x", "extra": 1}, nil); err == nil || !strings.Contains(err.Error(), "bad body") {
		t.Fatalf("unknown field: %v", err)
	}
	var st admproto.NodeStatus
	if err := c.Call(ctx, admproto.OpNodeStatus, nil, &st); err != nil || st.Active != "active" || st.Since == "" {
		t.Fatalf("status %+v %v", st, err)
	}
	if os.Geteuid() == 0 {
		return // PeerUID always lets root in
	}
	refuse := &Server{Ops: o, Allow: func(net.Conn) error { return errors.New("no") }}
	ln2, err := net.Listen("unix", filepath.Join(sd, "b.sock"))
	if err != nil {
		t.Fatal(err)
	}
	go refuse.Serve(ln2)
	defer ln2.Close()
	var refuseErr error
	for i := 0; i < 25; i++ {
		refuseErr = (admproto.Client{Path: filepath.Join(sd, "b.sock")}).Call(ctx, admproto.OpPing, nil, nil)
		if refuseErr != nil && refuseErr.Error() == "not allowed" {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if refuseErr == nil || refuseErr.Error() != "not allowed" {
		t.Fatalf("peer check: %v", refuseErr)
	}
	if err := (admproto.Client{Path: filepath.Join(sd, "none.sock")}).Call(ctx, admproto.OpPing, nil, nil); err == nil || !strings.Contains(err.Error(), "not available") {
		t.Fatalf("missing socket: %v", err)
	}
}

func contains(l []string, s string) bool {
	for _, x := range l {
		if x == s {
			return true
		}
	}
	return false
}

func TestNetworkApplyReloads(t *testing.T) {
	o, fn, _, _ := setup(t)
	fn.reload = true
	cur, _ := o.NetworkGet()
	next := strings.Replace(cur.Text, "peers vlan", "peers mesh", 1)
	if _, err := o.NetworkApply(context.Background(), admproto.NetworkApply{Text: next, Expect: cur.SHA256}); err != nil {
		t.Fatal(err)
	}
	if !contains(fn.calls, "reload loft-node.service") || contains(fn.calls, "restart loft-node.service") {
		t.Fatalf("want reload, not restart: %v", fn.calls)
	}
}

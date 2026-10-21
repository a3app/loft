package adm

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/a3app/loft/server/panel/admproto"
)

type fakeNet struct {
	ran   []string
	addrs []string // extra addresses on eth0
}

func (f *fakeNet) exec(_ context.Context, name string, args ...string) (string, error) {
	line := name + " " + strings.Join(args, " ")
	f.ran = append(f.ran, line)
	switch {
	case line == "ip -j addr show":
		extra := ""
		for _, a := range f.addrs {
			ip, pl, _ := strings.Cut(a, "/")
			extra += `,{"family":"inet","local":"` + ip + `","prefixlen":` + pl + `,"scope":"global"}`
		}
		return `[{"ifname":"lo","addr_info":[{"family":"inet","local":"127.0.0.1","prefixlen":8,"scope":"host"}]},
			{"ifname":"eth0","addr_info":[{"family":"inet","local":"203.0.113.10","prefixlen":24,"scope":"global"}` + extra + `,
			{"family":"inet6","local":"2001:db8::10","prefixlen":64,"scope":"global"}]}]`, nil
	case line == "ip -j -4 route show table main":
		return `[{"dst":"default","gateway":"203.0.113.1","dev":"eth0","protocol":"static"},{"dst":"203.0.113.0/24","dev":"eth0","protocol":"kernel","scope":"link"},{"dst":"198.51.100.0/24","gateway":"203.0.113.1","dev":"eth0","protocol":"200"}]`, nil
	case line == "ip -j -6 route show table main":
		return `[]`, nil
	case line == "ss -Htlnup":
		return "tcp LISTEN 0 4096 *:443 *:* users:((\"loft-node\",pid=1,fd=6))\ntcp LISTEN 0 511 0.0.0.0:80 0.0.0.0:* users:((\"nginx\",pid=2,fd=5))\ntcp LISTEN 0 4096 127.0.0.1:8091 0.0.0.0:* users:((\"loft-panel\",pid=3,fd=3))\n", nil
	case strings.HasPrefix(line, "ip addr add "):
		f.addrs = append(f.addrs, args[2])
	case strings.HasPrefix(line, "ip addr del "):
		for i, a := range f.addrs {
			if a == args[2] {
				f.addrs = append(f.addrs[:i], f.addrs[i+1:]...)
			}
		}
	}
	return "", nil
}

func TestNetChangeHoldAndRevert(t *testing.T) {
	o, _ := pkgOps(t)
	o.PanelEtc = t.TempDir()
	f := &fakeNet{}
	o.Exec = f.exec
	ctx := context.Background()
	st, err := o.NetState(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Addrs) != 2 || !st.Addrs[0].Primary || st.Addrs[0].Gateway != "203.0.113.1" || strings.Join(st.Addrs[0].Listen, ",") != "loft-node :443/tcp,nginx :80/tcp" {
		t.Fatalf("addrs %+v", st.Addrs)
	}
	if len(st.Routes) != 2 || st.Routes[1].Source != "panel" {
		t.Fatalf("routes %+v", st.Routes)
	}
	for _, bad := range []admproto.NetChange{
		{Op: "add-addr", Addr: "203.0.113.11", Dev: "eth0"},
		{Op: "add-addr", Addr: "203.0.113.11/32", Dev: "eth9"},
		{Op: "add-addr", Addr: "203.0.113.10/24", Dev: "eth0"},
		{Op: "del-addr", Addr: "203.0.113.10/24", Dev: "eth0"},
		{Op: "add-route", Dst: "0.0.0.0/0", Via: "203.0.113.1", Dev: "eth0"},
		{Op: "add-route", Dst: "192.0.2.0/24", Via: "2001:db8::1", Dev: "eth0"},
	} {
		if _, err := o.NetChange(ctx, bad); err == nil {
			t.Fatalf("%+v accepted", bad)
		}
	}
	p, err := o.NetChange(ctx, admproto.NetChange{Op: "add-addr", Addr: "203.0.113.11/32", Dev: "eth0"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := o.NetChange(ctx, admproto.NetChange{Op: "add-route", Dst: "192.0.2.0/24", Via: "203.0.113.1", Dev: "eth0"}); err == nil {
		t.Fatal("second change while one is held")
	}
	if st, _ := o.NetState(ctx); st.Pending == nil || len(st.Addrs) != 3 || !st.Addrs[1].Panel {
		t.Fatalf("after add %+v", st)
	}
	if err := o.NetRevert(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	if len(f.addrs) != 0 {
		t.Fatalf("not undone: %v", f.addrs)
	}
	p, _ = o.NetChange(ctx, admproto.NetChange{Op: "add-route", Dst: "192.0.2.0/24", Via: "203.0.113.1", Dev: "eth0"})
	if err := o.NetConfirm(ctx, p.ID); err != nil {
		t.Fatal(err)
	}
	n, _ := o.readNet()
	if len(n.Routes) != 1 || n.Routes[0].Dst != "192.0.2.0/24" {
		t.Fatalf("net.json %+v", n)
	}
	if !strings.Contains(strings.Join(f.ran, "\n"), "ip route add 192.0.2.0/24 via 203.0.113.1 dev eth0 proto 200") {
		t.Fatalf("ran %v", f.ran)
	}
	// A change held at shutdown was never confirmed: boot puts the old one back.
	o.NetChange(ctx, admproto.NetChange{Op: "add-addr", Addr: "203.0.113.12/32", Dev: "eth0"})
	o.NetBoot(ctx)
	if n, _ := o.readNet(); len(n.Addrs) != 0 {
		t.Fatalf("boot kept an unconfirmed address: %+v", n)
	}
	if _, err := os.Stat(o.pendingFile()); err == nil {
		t.Fatal("pending left after boot")
	}
}

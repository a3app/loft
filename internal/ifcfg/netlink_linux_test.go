package ifcfg

// Root tests: they change the real routing table, so they run only as root
// AND with LOFT_IFCFG_ROOT=1, meant for a throwaway network namespace such
// as a privileged container:
//
//	CGO_ENABLED=0 GOOS=linux go test -c -o ifcfg.test ./internal/ifcfg
//	docker run --rm --privileged --sysctl net.ipv6.conf.all.disable_ipv6=0 \
//	  -e LOFT_IFCFG_ROOT=1 -v $PWD:/t debian:stable sh -c \
//	  'apt-get -qq update && apt-get -qq install -y iproute2 >/dev/null && /t/ifcfg.test -test.v'
//
// The same binary in alpine (no iproute2, so "ip" is busybox) checks the
// busybox path:
//
//	docker run --rm --privileged --sysctl net.ipv6.conf.all.disable_ipv6=0 \
//	  -e LOFT_IFCFG_ROOT=1 -v $PWD:/t alpine /t/ifcfg.test -test.v
//
// The main test plays loft's real sequence of changes once with netlink and
// once with the "ip" program, reads the kernel's state back after each
// step, and requires the two to be identical. So netlink is checked against
// the tool everyone trusts, and the "ip" backend is checked at the same time.

import (
	"errors"
	"fmt"
	"net"
	"os"
	"sort"
	"strings"
	"testing"

	"github.com/songgao/water"
	"golang.org/x/sys/unix"
)

const testDev = "lft0"

func needRoot(t *testing.T) {
	t.Helper()
	if os.Geteuid() != 0 || os.Getenv("LOFT_IFCFG_ROOT") != "1" {
		t.Skip("changes the routing table: run as root with LOFT_IFCFG_ROOT=1 in a throwaway netns (see file comment)")
	}
}

// openTUN creates the TUN device loft would use. It disappears when closed.
func openTUN(t *testing.T) *water.Interface {
	t.Helper()
	ifc, err := water.New(water.Config{
		DeviceType:             water.TUN,
		PlatformSpecificParams: water.PlatformSpecificParams{Name: testDev},
	})
	if err != nil {
		t.Fatalf("open TUN: %v", err)
	}
	return ifc
}

func mustCIDR(s string) *net.IPNet {
	ip, n, err := net.ParseCIDR(s)
	if err != nil {
		panic(err)
	}
	// Keep the host part for interface addresses (10.77.0.10/24, not .0).
	n.IP = ip
	if v4 := ip.To4(); v4 != nil {
		n.IP = v4
	}
	return n
}

// ipv6On reports whether this namespace has IPv6 (Docker turns it off by
// default unless started with the sysctl from the file comment).
func ipv6On() bool {
	b, err := os.ReadFile("/proc/sys/net/ipv6/conf/all/disable_ipv6")
	return err == nil && strings.TrimSpace(string(b)) == "0"
}

// routes lists the main routing table, IPv4 and IPv6, one line per route,
// sorted, with interface names instead of numbers (a new TUN gets a new
// number each time, the name stays the same).
func routes(t *testing.T) []string {
	t.Helper()
	var out []string
	for _, fam := range []uint8{unix.AF_INET, unix.AF_INET6} {
		req := newRequest(unix.RTM_GETROUTE, unix.NLM_F_DUMP)
		req.body(rtmsg(fam, 0, 0, 0, 0, 0))
		msgs, err := dump(req)
		if err != nil {
			t.Fatalf("dump routes: %v", err)
		}
		for _, m := range msgs {
			if m.typ != unix.RTM_NEWROUTE || len(m.data) < unix.SizeofRtMsg {
				continue
			}
			d := m.data
			table := uint32(d[4])
			attrs, err := parseAttrs(d[unix.SizeofRtMsg:])
			if err != nil {
				t.Fatal(err)
			}
			var dst, gw, oif string
			var metric uint32
			for _, a := range attrs {
				switch a.typ {
				case unix.RTA_TABLE:
					table = nativeEndian.Uint32(a.value)
				case unix.RTA_DST:
					dst = net.IP(a.value).String()
				case unix.RTA_GATEWAY:
					gw = net.IP(a.value).String()
				case unix.RTA_OIF:
					if ifi, err := net.InterfaceByIndex(int(nativeEndian.Uint32(a.value))); err == nil {
						oif = ifi.Name
					}
				case unix.RTA_PRIORITY:
					metric = nativeEndian.Uint32(a.value)
				}
			}
			if table != unix.RT_TABLE_MAIN {
				continue
			}
			if dst == "" {
				dst = "default"
			}
			out = append(out, fmt.Sprintf("af=%d %s/%d type=%d scope=%d proto=%d oif=%s gw=%s metric=%d",
				d[0], dst, d[1], d[7], d[6], d[5], oif, gw, metric))
		}
	}
	sort.Strings(out)
	return out
}

// addrs lists the addresses on dev as "ip/prefix scope=N".
func addrs(t *testing.T, dev string) []string {
	t.Helper()
	ifi, err := net.InterfaceByName(dev)
	if err != nil {
		t.Fatal(err)
	}
	req := newRequest(unix.RTM_GETADDR, unix.NLM_F_DUMP)
	req.body(ifaddrmsg(unix.AF_UNSPEC, 0, 0, 0))
	msgs, err := dump(req)
	if err != nil {
		t.Fatalf("dump addrs: %v", err)
	}
	var out []string
	for _, m := range msgs {
		if m.typ != unix.RTM_NEWADDR || len(m.data) < unix.SizeofIfAddrmsg {
			continue
		}
		if int(nativeEndian.Uint32(m.data[4:8])) != ifi.Index {
			continue
		}
		attrs, err := parseAttrs(m.data[unix.SizeofIfAddrmsg:])
		if err != nil {
			t.Fatal(err)
		}
		// IPv6 link-local addresses (fe80::/64, scope link) are made up
		// at random for each new TUN, so they would differ between runs
		// for no reason; leave them out.
		if m.data[0] == unix.AF_INET6 && m.data[3] == unix.RT_SCOPE_LINK {
			continue
		}
		for _, a := range attrs {
			if a.typ == unix.IFA_LOCAL || (a.typ == unix.IFA_ADDRESS && m.data[0] == unix.AF_INET6) {
				out = append(out, fmt.Sprintf("%s/%d scope=%d", net.IP(a.value), m.data[1], m.data[3]))
			}
		}
	}
	sort.Strings(out)
	return out
}

// snapshot is everything we compare between the two backends.
func snapshot(t *testing.T) string {
	t.Helper()
	ifi, err := net.InterfaceByName(testDev)
	if err != nil {
		t.Fatal(err)
	}
	return fmt.Sprintf("up=%v mtu=%d\naddrs:\n  %s\nroutes:\n  %s",
		ifi.Flags&net.FlagUp != 0, ifi.MTU,
		strings.Join(addrs(t, testDev), "\n  "), strings.Join(routes(t), "\n  "))
}

// scenario plays loft's changes with backend b and returns the state after
// each step, then undoes everything and checks the table is back to start.
func scenario(t *testing.T, b Backend) []string {
	t.Helper()
	ifc := openTUN(t)
	defer ifc.Close()

	before := routes(t)
	var states []string
	step := func(name string, err error) {
		t.Helper()
		if err != nil {
			t.Fatalf("%s %s: %v", b.Name(), name, err)
		}
		states = append(states, name+"\n"+snapshot(t))
	}

	// 1. Bring the TUN up as configureTUN does.
	step("addr add", b.AddrAdd(testDev, mustCIDR("10.77.0.10/24")))
	step("link up", b.LinkUp(testDev, 1280))

	// 2. Routes as addSplitDefault / addHostRoute make them, plus one with
	//    a next hop (the "via" form).
	step("route 0/1", b.RouteAdd(Route{Dst: mustCIDR("0.0.0.0/1"), Dev: testDev}))
	step("route 128/1", b.RouteAdd(Route{Dst: mustCIDR("128.0.0.0/1"), Dev: testDev}))
	step("route lab cidr", b.RouteAdd(Route{Dst: mustCIDR("198.51.100.0/24"), Dev: testDev}))
	step("route via", b.RouteAdd(Route{Dst: mustCIDR("203.0.113.0/24"), Gw: net.ParseIP("10.77.0.1"), Dev: testDev}))
	v6 := Route{Dst: mustCIDR("2000::/3"), Type: Unreachable}
	if ipv6On() {
		step("v6 unreachable", b.RouteReplace(v6))
	}

	// 3. A second add of the same address must fail like "ip" does.
	if err := b.AddrAdd(testDev, mustCIDR("10.77.0.10/24")); err == nil {
		t.Errorf("%s: duplicate addr add succeeded", b.Name())
	} else if b.Name() == "netlink" && !errors.Is(err, unix.EEXIST) {
		t.Errorf("netlink duplicate addr add: %v, want EEXIST", err)
	}

	// 4. Readdress as readdressTUN does: promote, add new, delete old.
	//    The split default must survive (it is in the snapshot).
	if err := PromoteSecondaries(testDev); err != nil {
		t.Fatal(err)
	}
	step("readdress add", b.AddrAdd(testDev, mustCIDR("10.77.0.11/24")))
	step("readdress del", b.AddrDel(testDev, mustCIDR("10.77.0.10/24")))

	// 5. Kill switch: blackhole for a test prefix, replaced twice (the
	//    second replace must not fail on "exists").
	bh := Route{Dst: mustCIDR("192.0.2.0/24"), Type: Blackhole}
	step("blackhole", b.RouteReplace(bh))
	step("blackhole again", b.RouteReplace(bh))

	// 6. Undo, newest first, the way loft's undo lists run.
	step("del blackhole", b.RouteDel(bh))
	if ipv6On() {
		step("del v6", b.RouteDel(v6))
	}
	step("del via", b.RouteDel(Route{Dst: mustCIDR("203.0.113.0/24")}))
	step("del lab cidr", b.RouteDel(Route{Dst: mustCIDR("198.51.100.0/24"), Dev: testDev}))
	step("del 128/1", b.RouteDel(Route{Dst: mustCIDR("128.0.0.0/1")}))
	step("del 0/1", b.RouteDel(Route{Dst: mustCIDR("0.0.0.0/1")}))

	// Deleting a route that is gone must fail.
	if err := b.RouteDel(Route{Dst: mustCIDR("0.0.0.0/1")}); err == nil {
		t.Errorf("%s: delete of a missing route succeeded", b.Name())
	}

	// Our address's subnet route goes with the TUN; everything else must
	// be exactly as before.
	ifc.Close()
	if after := routes(t); strings.Join(after, "\n") != strings.Join(before, "\n") {
		t.Errorf("%s: routing table not restored\nbefore:\n%s\nafter:\n%s", b.Name(),
			strings.Join(before, "\n"), strings.Join(after, "\n"))
	}
	return states
}

// TestNetlinkMatchesIP is the main check: same steps, same kernel state.
func TestNetlinkMatchesIP(t *testing.T) {
	needRoot(t)
	nl := scenario(t, Netlink())
	ip := scenario(t, IPTool())
	if len(nl) != len(ip) {
		t.Fatalf("step count differs: netlink %d, ip %d", len(nl), len(ip))
	}
	for i := range nl {
		if nl[i] != ip[i] {
			t.Errorf("state differs after step %d\n--- netlink:\n%s\n--- ip:\n%s", i, nl[i], ip[i])
		}
	}
	t.Logf("final state with routes:\n%s", nl[len(nl)/2])
}

// TestReplaceDefaultBlackhole checks the exact kill-switch call, which
// replaces the namespace's real default route. It puts the default back
// afterwards (the gateway comes from the table itself).
func TestReplaceDefaultBlackhole(t *testing.T) {
	needRoot(t)
	var orig *Route
	for _, line := range routes(t) {
		// af=2 default/0 type=1 ... oif=eth0 gw=172.17.0.1
		if strings.HasPrefix(line, "af=2 default/0 type=1 ") {
			f := strings.Fields(line)
			r := Route{Dst: mustCIDR("0.0.0.0/0")}
			for _, kv := range f {
				if v, ok := strings.CutPrefix(kv, "oif="); ok {
					r.Dev = v
				}
				if v, ok := strings.CutPrefix(kv, "gw="); ok && v != "" {
					r.Gw = net.ParseIP(v)
				}
			}
			orig = &r
		}
	}
	for _, b := range []Backend{Netlink(), IPTool()} {
		if err := b.RouteReplace(Route{Dst: mustCIDR("0.0.0.0/0"), Type: Blackhole}); err != nil {
			t.Fatalf("%s: %v", b.Name(), err)
		}
		found := false
		for _, line := range routes(t) {
			if strings.HasPrefix(line, "af=2 default/0 type=6 ") {
				found = true
			}
		}
		if !found {
			t.Errorf("%s: no blackhole default after replace", b.Name())
		}
		if orig != nil {
			if err := b.RouteReplace(*orig); err != nil {
				t.Fatalf("%s restore default: %v", b.Name(), err)
			}
		} else if err := b.RouteDel(Route{Dst: mustCIDR("0.0.0.0/0"), Type: Blackhole}); err != nil {
			t.Fatalf("%s: %v", b.Name(), err)
		}
	}
}

// TestBadInput: wrong names and mixed families are refused before anything
// is sent to the kernel, so it needs no root.
func TestBadInput(t *testing.T) {
	b := Netlink()
	if err := b.AddrAdd("no-such-if0", mustCIDR("10.0.0.1/24")); err == nil {
		t.Error("unknown interface accepted")
	}
	if err := b.RouteAdd(Route{Dst: mustCIDR("10.0.0.0/8"), Gw: net.ParseIP("fe80::1"), Dev: "lo"}); err == nil {
		t.Error("IPv6 next hop on an IPv4 route accepted")
	}
	for _, name := range []string{"", "..", "a/b", "0123456789abcdef"} {
		if err := PromoteSecondaries(name); err == nil {
			t.Errorf("PromoteSecondaries(%q) accepted", name)
		}
	}
}

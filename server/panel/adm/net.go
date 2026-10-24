package adm

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/netip"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/a3app/loft/server/panel/admproto"
)

// Server addresses and routes. The helper has no network of its own, so ip
// and ss run on the host through systemd-run. What the panel adds lives in
// /etc/loft-panel/net.json and is applied at boot by loft-panel-net.service;
// a change is live at once and is undone after NetHold unless confirmed, so
// a wrong address or route cannot lock the operator out for good.

// RouteProto marks routes the panel installs (ip route … proto 200).
const RouteProto = "200"

// NetHold is how long a change waits for confirmation.
const NetHold = 2 * time.Minute

// host runs a command on the host, outside the helper's sandbox, and
// returns its stdout; the error carries stderr.
func (o *Ops) host(ctx context.Context, name string, args ...string) (string, error) {
	if o.Exec != nil {
		return o.Exec(ctx, name, args...)
	}
	cmd := exec.CommandContext(ctx, "systemd-run", append([]string{"--wait", "--pipe", "--collect", "--quiet", name}, args...)...)
	var out, errb bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errb
	if err := cmd.Run(); err != nil {
		return out.String(), fmt.Errorf("%s: %v %s", name, err, strings.TrimSpace(errb.String()))
	}
	return out.String(), nil
}

func (o *Ops) netFile() string {
	return filepath.Join(firstNonEmpty(o.PanelEtc, "/etc/loft-panel"), "net.json")
}
func (o *Ops) pendingFile() string { return filepath.Join(o.stateDir(), "net-pending.json") }

func (o *Ops) readNet() (admproto.Net, error) {
	var n admproto.Net
	b, err := os.ReadFile(o.netFile())
	if errors.Is(err, os.ErrNotExist) {
		return n, nil
	}
	if err != nil {
		return n, err
	}
	return n, json.Unmarshal(b, &n)
}

func (o *Ops) writeNet(n admproto.Net) error {
	b, _ := json.MarshalIndent(n, "", "  ")
	if err := os.MkdirAll(filepath.Dir(o.netFile()), 0o755); err != nil {
		return err
	}
	return storeWrite(o.netFile(), append(b, '\n'), 0o600)
}

type ipAddr struct {
	IfName   string `json:"ifname"`
	AddrInfo []struct {
		Family    string `json:"family"`
		Local     string `json:"local"`
		Prefixlen int    `json:"prefixlen"`
		Scope     string `json:"scope"`
		Dynamic   bool   `json:"dynamic"`
	} `json:"addr_info"`
	Operstate string `json:"operstate"`
	LinkType  string `json:"link_type"`
}

type ipRoute struct {
	Dst      string `json:"dst"`
	Gateway  string `json:"gateway"`
	Dev      string `json:"dev"`
	Protocol string `json:"protocol"`
	Prefsrc  string `json:"prefsrc"`
	Scope    string `json:"scope"`
}

// netProbe is ip addr, both route tables and ss. Tests keep four Exec
// calls; on the host it is one systemd-run.
func (o *Ops) netProbe(ctx context.Context) (addr, r4, r6, ss string, err error) {
	if o.Exec != nil {
		if addr, err = o.host(ctx, "ip", "-j", "addr", "show"); err != nil {
			return
		}
		if r4, err = o.host(ctx, "ip", "-j", "-4", "route", "show", "table", "main"); err != nil {
			return
		}
		if r6, err = o.host(ctx, "ip", "-j", "-6", "route", "show", "table", "main"); err != nil {
			return
		}
		ss, _ = o.host(ctx, "ss", "-Htlnup")
		return
	}
	out, err := o.host(ctx, "sh", "-c", "ip -j addr show; echo @@; ip -j -4 route show table main; echo @@; ip -j -6 route show table main; echo @@; ss -Htlnup")
	if err != nil && out == "" {
		return "", "", "", "", err
	}
	p := strings.Split(out, "@@")
	for len(p) < 4 {
		p = append(p, "")
	}
	return strings.TrimSpace(p[0]), strings.TrimSpace(p[1]), strings.TrimSpace(p[2]), strings.TrimSpace(p[3]), nil
}

// NetState is the server's addresses, routes and listeners.
func (o *Ops) NetState(ctx context.Context) (admproto.NetState, error) {
	var st admproto.NetState
	out, r4, r6, ssOut, err := o.netProbe(ctx)
	if err != nil {
		return st, err
	}
	var links []ipAddr
	if err := json.Unmarshal([]byte(out), &links); err != nil {
		return st, fmt.Errorf("ip addr: %v", err)
	}
	var routes []ipRoute
	for _, raw := range []string{r4, r6} {
		if strings.TrimSpace(raw) == "" {
			continue
		}
		var l []ipRoute
		if err := json.Unmarshal([]byte(raw), &l); err != nil {
			return st, fmt.Errorf("ip route: %v", err)
		}
		routes = append(routes, l...)
	}
	mine, _ := o.readNet()
	gw := map[string]string{} // dev|family → default gateway
	defDev := ""
	for _, r := range routes {
		if r.Dst == "default" {
			fam := "inet"
			if strings.Contains(r.Gateway, ":") {
				fam = "inet6"
			}
			if gw[r.Dev+"|"+fam] == "" {
				gw[r.Dev+"|"+fam] = r.Gateway
			}
			if defDev == "" {
				defDev = r.Dev
			}
		}
	}
	listen := parseListeners(ssOut)
	chDev := map[string]string{} // interface → channel
	if cs, err := o.readChans(); err == nil {
		for _, c := range cs.Channels {
			if c.Dev != "" {
				chDev[c.Dev] = c.Name
			}
		}
	}
	primary := map[string]bool{}
	for _, l := range links {
		if l.IfName == "lo" {
			continue
		}
		st.Devs = append(st.Devs, l.IfName)
		for _, a := range l.AddrInfo {
			if a.Scope != "global" {
				continue
			}
			cidr := fmt.Sprintf("%s/%d", a.Local, a.Prefixlen)
			ad := admproto.NetAddr{Addr: cidr, Dev: l.IfName, Gateway: gw[l.IfName+"|"+a.Family],
				Panel: slices.ContainsFunc(mine.Addrs, func(x admproto.NetAddrSpec) bool { return x.Addr == cidr && x.Dev == l.IfName })}
			if l.IfName == defDev && !primary[a.Family] {
				ad.Primary, primary[a.Family] = true, true
			}
			ad.Listen = listen.forAddr(a.Local, a.Family)
			ad.Channel = chDev[l.IfName]
			st.Addrs = append(st.Addrs, ad)
		}
	}
	for _, r := range routes {
		if r.Scope == "link" && r.Protocol == "kernel" {
			continue // the subnet of an address: shown with the address
		}
		src := "system"
		switch {
		case r.Protocol == RouteProto:
			src = "panel"
		case r.Protocol == ChannelProto || chDev[r.Dev] != "":
			src = "channel"
		}
		st.Routes = append(st.Routes, admproto.NetRoute{Dst: r.Dst, Via: r.Gateway, Dev: r.Dev, Source: src})
	}
	if p, err := o.readPending(); err == nil {
		st.Pending = &p
	}
	return st, nil
}

type listenSet []struct{ addr, fam, what string }

var ssUsers = regexp.MustCompile(`\(\("([^"]+)"`)

func parseListeners(out string) listenSet {
	var ls listenSet
	seen := map[string]bool{}
	for _, ln := range strings.Split(out, "\n") {
		f := strings.Fields(ln)
		if len(f) < 5 {
			continue
		}
		proto, local := f[0], f[4]
		i := strings.LastIndex(local, ":")
		if i < 0 {
			continue
		}
		addr, port := local[:i], local[i+1:]
		addr = strings.Trim(addr, "[]")
		if j := strings.Index(addr, "%"); j >= 0 {
			addr = addr[:j]
		}
		addr = strings.TrimSuffix(addr, "]")
		proc := "?"
		if m := ssUsers.FindStringSubmatch(ln); m != nil {
			proc = m[1]
		}
		fam := "inet"
		if strings.Contains(addr, ":") {
			fam = "inet6"
		}
		if addr == "*" {
			fam = "*"
		}
		what := proc + " :" + port + "/" + proto
		if seen[addr+what] {
			continue
		}
		seen[addr+what] = true
		ls = append(ls, struct{ addr, fam, what string }{addr, fam, what})
	}
	return ls
}

func (ls listenSet) forAddr(ip, fam string) []string {
	var out []string
	for _, l := range ls {
		if l.addr == ip || l.fam == "*" || (l.fam == fam && (l.addr == "0.0.0.0" || l.addr == "::")) {
			if l.addr == "127.0.0.1" || l.addr == "::1" {
				continue
			}
			out = append(out, l.what)
		}
	}
	sort.Strings(out)
	return slices.Compact(out)
}

func validDev(name string) bool {
	if name == "" || len(name) > 15 {
		return false
	}
	for _, r := range name {
		if !(r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || r == '-' || r == '_' || r == '.' || r == '@') {
			return false
		}
	}
	return true
}

// NetChange applies one change live and holds it for NetHold.
func (o *Ops) NetChange(ctx context.Context, req admproto.NetChange) (admproto.NetPending, error) {
	o.netMu.Lock()
	defer o.netMu.Unlock()
	if _, err := o.readPending(); err == nil {
		return admproto.NetPending{}, errors.New("a network change waits for confirmation: confirm or undo it first")
	}
	st, err := o.NetState(ctx)
	if err != nil {
		return admproto.NetPending{}, err
	}
	cur, err := o.readNet()
	if err != nil {
		return admproto.NetPending{}, err
	}
	next := cur
	next.Addrs = slices.Clone(cur.Addrs)
	next.Routes = slices.Clone(cur.Routes)
	if !slices.Contains(st.Devs, req.Dev) || !validDev(req.Dev) {
		return admproto.NetPending{}, fmt.Errorf("no interface %q on this server", req.Dev)
	}
	var cmds [][]string
	switch req.Op {
	case "add-addr", "del-addr":
		p, err := netip.ParsePrefix(req.Addr)
		if err != nil || !p.Addr().IsGlobalUnicast() {
			return admproto.NetPending{}, fmt.Errorf("%q is not an address with a prefix, like 203.0.113.11/32", req.Addr)
		}
		spec := admproto.NetAddrSpec{Addr: p.String(), Dev: req.Dev}
		i := slices.Index(next.Addrs, spec)
		if req.Op == "add-addr" {
			if slices.ContainsFunc(st.Addrs, func(a admproto.NetAddr) bool { return a.Addr == spec.Addr }) {
				return admproto.NetPending{}, fmt.Errorf("%s is already on this server", spec.Addr)
			}
			next.Addrs = append(next.Addrs, spec)
			cmds = append(cmds, []string{"ip", "addr", "add", spec.Addr, "dev", spec.Dev})
		} else {
			if i < 0 {
				return admproto.NetPending{}, fmt.Errorf("%s was not added by the panel: it stays", spec.Addr)
			}
			next.Addrs = slices.Delete(next.Addrs, i, i+1)
			cmds = append(cmds, []string{"ip", "addr", "del", spec.Addr, "dev", spec.Dev})
		}
	case "add-route", "del-route":
		p, err := netip.ParsePrefix(req.Dst)
		if err != nil || p.Bits() == 0 {
			return admproto.NetPending{}, fmt.Errorf("%q is not a subnet like 198.51.100.0/24 (the default route stays the system's)", req.Dst)
		}
		spec := admproto.NetRouteSpec{Dst: p.Masked().String(), Via: req.Via, Dev: req.Dev}
		if req.Via != "" {
			v, err := netip.ParseAddr(req.Via)
			if err != nil || v.Is4() != p.Addr().Is4() {
				return admproto.NetPending{}, fmt.Errorf("%q is not a gateway address of the same family", req.Via)
			}
		}
		args := []string{spec.Dst}
		if spec.Via != "" {
			args = append(args, "via", spec.Via)
		}
		args = append(args, "dev", spec.Dev, "proto", RouteProto)
		i := slices.Index(next.Routes, spec)
		if req.Op == "add-route" {
			if i >= 0 {
				return admproto.NetPending{}, fmt.Errorf("route %s is already there", spec.Dst)
			}
			next.Routes = append(next.Routes, spec)
			cmds = append(cmds, append([]string{"ip", "route", "add"}, args...))
		} else {
			if i < 0 {
				return admproto.NetPending{}, fmt.Errorf("route %s was not added by the panel: it stays", spec.Dst)
			}
			next.Routes = slices.Delete(next.Routes, i, i+1)
			cmds = append(cmds, append([]string{"ip", "route", "del"}, args...))
		}
	default:
		return admproto.NetPending{}, fmt.Errorf("unknown change %q", req.Op)
	}
	for _, c := range cmds {
		if _, err := o.host(ctx, c[0], c[1:]...); err != nil {
			return admproto.NetPending{}, err
		}
	}
	b := make([]byte, 6)
	rand.Read(b)
	p := admproto.NetPending{ID: hex.EncodeToString(b), Change: req, Prev: cur, Next: next, Deadline: time.Now().Add(NetHold).UTC()}
	if err := o.writeNet(next); err != nil {
		o.netApply(ctx, cur, next) // put it back
		return admproto.NetPending{}, err
	}
	if err := o.savePending(p); err != nil {
		return admproto.NetPending{}, err
	}
	// The undo runs even if the panel and the helper are gone by then.
	if o.Exec == nil {
		exe := firstNonEmpty(o.Exe, mustExe())
		o.host(ctx, "systemctl", "stop", "loft-panel-net-revert.timer")
		if out, err := exec.CommandContext(ctx, "systemd-run", "--unit", "loft-panel-net-revert", "--collect", "--quiet",
			"--on-active="+strconv.Itoa(int(NetHold/time.Second)), exe, "net-revert", "-id", p.ID, "-state", o.stateDir()).CombinedOutput(); err != nil {
			o.revertLocked(ctx, p)
			return admproto.NetPending{}, fmt.Errorf("could not schedule the undo, change taken back: %v %s", err, out)
		}
	}
	return p, nil
}

func mustExe() string { e, _ := os.Executable(); return e }

func (o *Ops) readPending() (admproto.NetPending, error) {
	var p admproto.NetPending
	b, err := os.ReadFile(o.pendingFile())
	if err != nil {
		return p, err
	}
	return p, json.Unmarshal(b, &p)
}

func (o *Ops) savePending(p admproto.NetPending) error {
	if err := os.MkdirAll(o.stateDir(), 0o700); err != nil {
		return err
	}
	b, _ := json.Marshal(p)
	return storeWrite(o.pendingFile(), b, 0o600)
}

// NetConfirm keeps the held change.
func (o *Ops) NetConfirm(ctx context.Context, id string) error {
	o.netMu.Lock()
	defer o.netMu.Unlock()
	p, err := o.readPending()
	if err != nil || p.ID != id {
		return errors.New("no such change waits (it may have been undone)")
	}
	if o.Exec == nil {
		o.host(ctx, "systemctl", "stop", "loft-panel-net-revert.timer")
	}
	return os.Remove(o.pendingFile())
}

// NetRevert undoes the held change (the timer, or the operator).
func (o *Ops) NetRevert(ctx context.Context, id string) error {
	o.netMu.Lock()
	defer o.netMu.Unlock()
	p, err := o.readPending()
	if err != nil || p.ID != id {
		return errors.New("no such change waits (it may have been confirmed)")
	}
	return o.revertLocked(ctx, p)
}

func (o *Ops) revertLocked(ctx context.Context, p admproto.NetPending) error {
	o.netApply(ctx, p.Next, p.Prev)
	if err := o.writeNet(p.Prev); err != nil {
		return err
	}
	if o.Exec == nil {
		o.host(ctx, "systemctl", "stop", "loft-panel-net-revert.timer")
	}
	return os.Remove(o.pendingFile())
}

// netApply moves the host from have to want (only what the panel owns).
func (o *Ops) netApply(ctx context.Context, have, want admproto.Net) {
	for _, r := range have.Routes {
		if !slices.Contains(want.Routes, r) {
			o.host(ctx, "ip", routeArgs("del", r)...)
		}
	}
	for _, a := range have.Addrs {
		if !slices.Contains(want.Addrs, a) {
			o.host(ctx, "ip", "addr", "del", a.Addr, "dev", a.Dev)
		}
	}
	for _, a := range want.Addrs {
		if !slices.Contains(have.Addrs, a) {
			o.host(ctx, "ip", "addr", "add", a.Addr, "dev", a.Dev)
		}
	}
	for _, r := range want.Routes {
		if !slices.Contains(have.Routes, r) {
			o.host(ctx, "ip", routeArgs("add", r)...)
		}
	}
}

func routeArgs(verb string, r admproto.NetRouteSpec) []string {
	a := []string{"route", verb, r.Dst}
	if r.Via != "" {
		a = append(a, "via", r.Via)
	}
	return append(a, "dev", r.Dev, "proto", RouteProto)
}

// NetBoot applies net.json at boot (loft-panel-net.service). Addresses or
// routes already there are fine.
func (o *Ops) NetBoot(ctx context.Context) error {
	n, err := o.readNet()
	if err != nil {
		return err
	}
	// A change still held when the server went down was never confirmed.
	if p, err := o.readPending(); err == nil {
		n = p.Prev
		o.writeNet(n)
		os.Remove(o.pendingFile())
	}
	o.netApply(ctx, admproto.Net{}, n)
	o.Reconcile(ctx)
	return nil
}

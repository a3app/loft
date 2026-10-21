package adm

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/a3app/loft/internal/egress"
	"github.com/a3app/loft/server/panel/admproto"
)

const (
	privA = "yAnz5TF+lXXJte14tji3zlMNq+hd2rYUIgJBgB3fBmk="
	pubB  = "xTIBA5rboUvnH4htodjb6e697QjLERt1NAB4mZqp8Dg="
)

type fakeChanHost struct {
	ran     []string
	snap    string
	systemd []string
}

func (f *fakeChanHost) exec(_ context.Context, name string, args ...string) (string, error) {
	line := name + " " + strings.Join(args, " ")
	if name == "sh" {
		return f.snap, nil
	}
	f.ran = append(f.ran, line)
	return "", nil
}

func chanOps(t *testing.T) (*Ops, *fakeChanHost) {
	o, _ := pkgOps(t)
	o.PanelEtc = t.TempDir()
	f := &fakeChanHost{}
	o.Exec = f.exec
	o.Systemctl = func(_ context.Context, args ...string) (string, error) {
		f.systemd = append(f.systemd, strings.Join(args, " "))
		if args[0] == "show" && len(args) > 2 && args[2] == "--property=CanReload" {
			return "yes", nil
		}
		if args[0] == "is-active" {
			return "active", nil
		}
		return "", nil
	}
	os.MkdirAll(o.Etc, 0o755)
	return o, f
}

func wgChan(name string) admproto.Channel {
	return admproto.Channel{Name: name, Type: admproto.ChanWG, On: true, Exit: true, Fail: "direct",
		WG: &admproto.WGConf{PrivateKey: privA, Address: "10.8.0.2/24", Peers: []admproto.WGPeer{
			{Name: "nl1", PublicKey: pubB, Endpoint: "nl1.example.net", AllowedIPs: []string{"0.0.0.0/0", "10.20.0.1/16"}, Keepalive: 25}}}}
}

func TestChannelSaveWireGuard(t *testing.T) {
	o, f := chanOps(t)
	ctx := context.Background()
	v, err := o.ChannelSave(ctx, wgChan("wg-nl"))
	if err != nil {
		t.Fatal(err)
	}
	c := v.Channels[0]
	if c.WG.PrivateKey != "" || !c.WG.HasKey || c.WG.PublicKey == "" || c.Table != tableBase || c.Dev != "wg-nl" {
		t.Fatalf("view %+v %+v", c, c.WG)
	}
	conf, _ := os.ReadFile(filepath.Join(o.Root, "etc/wireguard/wg-nl.conf"))
	for _, want := range []string{"Table = off", "PrivateKey = " + privA, "Address = 10.8.0.2/24", "Endpoint = nl1.example.net:51820", "AllowedIPs = 0.0.0.0/0, 10.20.0.0/16", "PersistentKeepalive = 25"} {
		if !strings.Contains(string(conf), want) {
			t.Fatalf("missing %q in\n%s", want, conf)
		}
	}
	if !strings.Contains(strings.Join(f.systemd, "\n"), "restart wg-quick@wg-nl.service") {
		t.Fatalf("systemctl %v", f.systemd)
	}
	eg, _ := os.ReadFile(o.egressFile())
	if !strings.Contains(string(eg), "channel wg-nl dev wg-nl\n") {
		t.Fatalf("egress.conf:\n%s", eg)
	}
	// The key is kept when the page sends none.
	c2 := wgChan("wg-nl")
	c2.WG.PrivateKey = ""
	c2.WG.ListenPort = 51821
	if _, err := o.ChannelSave(ctx, c2); err != nil {
		t.Fatal(err)
	}
	s, _ := o.readChans()
	if s.Channels[0].WG.PrivateKey != privA || s.Channels[0].Table != tableBase {
		t.Fatalf("kept %+v", s.Channels[0])
	}
	if fi, _ := os.Stat(o.chanFile()); fi.Mode().Perm() != 0o600 {
		t.Fatalf("channels.json mode %v", fi.Mode())
	}
}

func TestChannelChecks(t *testing.T) {
	o, _ := chanOps(t)
	ctx := context.Background()
	bad := []admproto.Channel{
		{Name: "Bad", Type: admproto.ChanPX, PX: &admproto.PXConf{Kind: "http", Addr: "1.2.3.4:8080"}},
		{Name: "px", Type: admproto.ChanPX, PX: &admproto.PXConf{Kind: "ftp", Addr: "1.2.3.4:8080"}},
		{Name: "px", Type: admproto.ChanPX, PX: &admproto.PXConf{Kind: "http", Addr: "1.2.3.4"}},
		{Name: "px", Type: admproto.ChanPX, PX: &admproto.PXConf{Kind: "http", Addr: "1.2.3.4:80", UDP: "proxy"}},
		{Name: "px", Type: admproto.ChanPX, Check: &admproto.ChanCheck{Kind: "ping", Probe: "1.1.1.1"}, PX: &admproto.PXConf{Kind: "http", Addr: "1.2.3.4:80"}},
		{Name: "px", Type: admproto.ChanPX, Check: &admproto.ChanCheck{Kind: "tcp", Probe: "nohost"}, PX: &admproto.PXConf{Kind: "http", Addr: "1.2.3.4:80"}},
		{Name: "px", Type: admproto.ChanPX, Check: &admproto.ChanCheck{Kind: "tcp", Probe: "a.example:443", Every: 9999}, PX: &admproto.PXConf{Kind: "http", Addr: "1.2.3.4:80"}},
		{Name: "zt", Type: admproto.ChanZT, ZT: &admproto.ZTConf{Network: "123"}},
		{Name: "vpn", Type: admproto.ChanOVPN, OVPN: &admproto.OVPNConf{Config: "client\nremote a 1194\nup /bin/sh\n"}},
		{Name: "vpn", Type: admproto.ChanOVPN, OVPN: &admproto.OVPNConf{Config: "client\nremote a 1194\n--script-security 2\n"}},
		{Name: "s", Type: admproto.ChanSSTP, SSTP: &admproto.SSTPConf{Server: "vpn.example.org", User: "u"}},
		{Name: "x", Type: admproto.ChanXR, XR: &admproto.XRConf{Link: "http://nope"}},
		{Name: "px", Type: admproto.ChanPX, Accept: true, PX: &admproto.PXConf{Kind: "http", Addr: "1.2.3.4:80"}},
	}
	for _, c := range bad {
		if _, err := o.ChannelSave(ctx, c); err == nil {
			t.Fatalf("%+v saved", c)
		}
	}
	w := wgChan("wg")
	w.WG.Peers[0].PublicKey = "short"
	if _, err := o.ChannelSave(ctx, w); err == nil {
		t.Fatal("bad peer key saved")
	}
}

func TestChannelRulesAndDelete(t *testing.T) {
	o, _ := chanOps(t)
	ctx := context.Background()
	w := wgChan("wg-nl")
	w.Check = &admproto.ChanCheck{Kind: "ping", Probe: "1.1.1.1"}
	if _, err := o.ChannelSave(ctx, w); err != nil {
		t.Fatal(err)
	}
	px := admproto.Channel{Name: "px", Type: admproto.ChanPX, On: true, Exit: true, Via: "wg-nl",
		Check: &admproto.ChanCheck{Kind: "tcp", Probe: "example.com:443", Every: 5},
		PX:    &admproto.PXConf{Kind: "socks5", Addr: "192.0.2.40:1080", User: "u", Password: "p w", UDP: "proxy"}}
	if _, err := o.ChannelSave(ctx, px); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []admproto.Prio{{Title: ""}, {Title: "x", Use: []string{"nowhere"}}, {Title: "x", Use: []string{"px", "px"}}, {Title: "x", Last: "maybe"}} {
		if _, err := o.ChannelPrio(ctx, bad); err == nil {
			t.Fatalf("list %+v saved", bad)
		}
	}
	v, err := o.ChannelPrio(ctx, admproto.Prio{Title: "Main", Use: []string{"wg-nl", "px"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Prios) != 1 || v.Prios[0].ID != "prio1" || v.Prios[0].Last != "direct" {
		t.Fatalf("prios %+v", v.Prios)
	}
	if _, err := o.ChannelPrio(ctx, admproto.Prio{Title: "main"}); err == nil {
		t.Fatal("two lists with one name")
	}
	if _, err := o.ChannelPrio(ctx, admproto.Prio{Title: "Proxy", Use: []string{"px"}, Last: "block"}); err != nil {
		t.Fatal(err)
	}
	if _, err := o.ChannelRules(ctx, admproto.ExitRules{Rules: []admproto.ExitRule{{Match: "member", Value: "bob", Via: "px"}}}); err == nil {
		t.Fatal("a rule straight to a channel")
	}
	v, err = o.ChannelRules(ctx, admproto.ExitRules{Rules: []admproto.ExitRule{{Match: "member", Value: "bob", Via: "prio2"}, {Match: "tag", Value: "office", Via: "prio1"}}, Default: "prio1"})
	if err != nil {
		t.Fatal(err)
	}
	if len(v.Rules.Rules) != 2 || v.Channels[1].PX.Password != "" || !v.Channels[1].PX.HasPassword {
		t.Fatalf("view %+v", v)
	}
	eg, _ := os.ReadFile(o.egressFile())
	cfg, err := egress.Parse(string(eg))
	if err != nil {
		t.Fatalf("node cannot read egress.conf: %v\n%s", err, eg)
	}
	if p := cfg.Channel("px"); p.Kind != "socks5" || p.Pass != "p w" || p.Via != "wg-nl" || p.UDP != "proxy" || p.Check == nil || p.Check.Every != 5 || p.Check.Fails != 3 {
		t.Fatalf("px %+v", p)
	}
	if p := cfg.Prio("prio1"); p == nil || strings.Join(p.Use, " ") != "wg-nl px" || p.Last != "direct" || cfg.Default != "prio1" {
		t.Fatalf("prio1 %+v, default %s\n%s", p, cfg.Default, eg)
	}
	if _, err := o.ChannelDelete(ctx, "wg-nl"); err == nil {
		t.Fatal("deleted a channel a list and px use")
	}
	nr := wgChan("wg-nl")
	nr.Exit = false
	if _, err := o.ChannelSave(ctx, nr); err == nil {
		t.Fatal("exit switched off under a list")
	}
	if _, err := o.ChannelPrioDelete(ctx, "prio1"); err == nil {
		t.Fatal("deleted a list rules use")
	}
	o.ChannelRules(ctx, admproto.ExitRules{Default: "direct"})
	for _, id := range []string{"prio1", "prio2"} {
		if _, err := o.ChannelPrioDelete(ctx, id); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := o.ChannelDelete(ctx, "px"); err != nil {
		t.Fatal(err)
	}
	if _, err := o.ChannelDelete(ctx, "wg-nl"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(o.Root, "etc/wireguard/wg-nl.conf")); err == nil {
		t.Fatal("wg config left")
	}
}

// A store from before priority lists: rules name channels, channels chain on failure.
func TestMigrateFailChains(t *testing.T) {
	s := chanStore{
		Channels: []admproto.Channel{{Name: "wg0", Title: "NL", Type: admproto.ChanWG, Exit: true, Fail: "wg1"}, {Name: "wg1", Type: admproto.ChanWG, Exit: true, Fail: "block"},
			{Name: "px0", Type: admproto.ChanPX, Exit: true, PX: &admproto.PXConf{Kind: "socks5", Addr: "192.0.2.1:1080"}}},
		Rules: admproto.ExitRules{Rules: []admproto.ExitRule{{Match: "member", Value: "bob", Via: "wg0"}, {Match: "tag", Value: "x", Via: "wg0"}, {Match: "list", Value: "l", Via: "block"}}, Default: "px0"},
	}
	migrate(&s)
	if len(s.Prios) != 2 {
		t.Fatalf("prios %+v", s.Prios)
	}
	a, b := s.Prios[0], s.Prios[1]
	if a.ID != "prio1" || a.Title != "NL" || strings.Join(a.Use, " ") != "wg0 wg1" || a.Last != "block" {
		t.Fatalf("first %+v", a)
	}
	if b.Title != "px0" || strings.Join(b.Use, " ") != "px0" || b.Last != "direct" {
		t.Fatalf("second %+v", b)
	}
	if s.Rules.Rules[0].Via != "prio1" || s.Rules.Rules[1].Via != "prio1" || s.Rules.Rules[2].Via != "block" || s.Rules.Default != "prio2" {
		t.Fatalf("rules %+v", s.Rules)
	}
	for _, c := range s.Channels {
		if c.Fail != "" {
			t.Fatalf("%s keeps fail %q", c.Name, c.Fail)
		}
	}
	if _, err := EgressConf(s); err != nil {
		t.Fatal(err)
	}
	before := len(s.Prios)
	migrate(&s)
	if len(s.Prios) != before {
		t.Fatal("a second read made more lists")
	}
}

func TestOVPNConfig(t *testing.T) {
	c := admproto.Channel{Name: "ovpn-de", Type: admproto.ChanOVPN, OVPN: &admproto.OVPNConf{Config: "client\r\nproto udp\r\nremote de.example.net 1194\r\n<ca>\nX\n</ca>\n", User: "u", Password: "p"}}
	if err := checkChannel(&c, nil, chanStore{}); err != nil {
		t.Fatal(err)
	}
	s := ovpnConf(c)
	for _, want := range []string{"dev ovpn-de", "route-nopull", "pull-filter ignore \"redirect-gateway\"", "auth-user-pass /etc/openvpn/client/ovpn-de.auth", "<ca>"} {
		if !strings.Contains(s, want) {
			t.Fatalf("missing %q in\n%s", want, s)
		}
	}
}

func TestXray(t *testing.T) {
	links := map[string]string{
		"vless://0b6b7c1a-1111-2222-3333-444455556666@de.example.org:443?security=reality&flow=xtls-rprx-vision&pbk=PUBKEY&sid=ab&sni=www.example.com&fp=chrome&type=tcp#de": "vless",
		"trojan://secret@tr.example.org:443?sni=tr.example.org&type=ws&path=%2Fws#tr":                                                                                        "trojan",
		"ss://" + "YWVzLTI1Ni1nY206cGFzcw" + "@ss.example.org:8388#ss":                                                                                                       "shadowsocks",
	}
	for l, proto := range links {
		x, err := ParseXrayLink(l)
		if err != nil || x.Protocol != proto {
			t.Fatalf("%s: %+v %v", l, x, err)
		}
	}
	for _, bad := range []string{"vless://@h:1", "vless://id@h:0", "trojan://p@h:443?security=foo", "vless://id@h:443?security=reality", "ss://bad"} {
		if _, err := ParseXrayLink(bad); err == nil {
			t.Fatalf("%q parsed", bad)
		}
	}
	s := chanStore{Channels: []admproto.Channel{
		{Name: "px", Type: admproto.ChanPX, On: true, PX: &admproto.PXConf{Kind: "https", Addr: "192.0.2.40:443", User: "u", Password: "p"}},
		{Name: "xr-de", Type: admproto.ChanXR, On: true, Via: "px", XR: &admproto.XRConf{Link: "trojan://secret@tr.example.org:443", Port: 10800}},
	}}
	b, err := XrayConfig(s)
	if err != nil {
		t.Fatal(err)
	}
	var m map[string]any
	json.Unmarshal(b, &m)
	txt := string(b)
	for _, want := range []string{`"port": 10800`, `"dialerProxy": "via-px"`, `"tag": "via-px"`, `"protocol": "http"`, `"inboundTag"`} {
		if !strings.Contains(txt, want) {
			t.Fatalf("missing %s in\n%s", want, txt)
		}
	}
	if conf, _ := EgressConf(s); !strings.Contains(conf, "channel xr-de socks5 127.0.0.1:10800 udp proxy") {
		t.Fatalf("egress:\n%s", conf)
	}
}

func TestReconcile(t *testing.T) {
	o, f := chanOps(t)
	ctx := context.Background()
	o.ChannelSave(ctx, wgChan("wg-nl"))
	c := wgChan("wg-nl")
	c.Accept = true
	o.ChannelSave(ctx, c)
	f.ran = nil
	// wg-nl is up with 10.8.0.2; a stale rule of a gone channel is there.
	f.snap = `[{"ifname":"lo"},{"ifname":"wg-nl","stats64":{"rx":{"bytes":9},"tx":{"bytes":5}}}]@@` +
		`[{"ifname":"wg-nl","addr_info":[{"family":"inet","local":"10.8.0.2","prefixlen":24,"scope":"global"}]}]@@` +
		`[{"priority":0,"src":"all","table":"local"},{"priority":10010,"src":"all","oifname":"old","table":"1005"}]@@[]@@` +
		"wg-nl\tPRIV\tPUB\t51820\toff\n" + "wg-nl\t" + pubB + "\t(none)\t198.51.100.1:51820\t0.0.0.0/0\t" + "1\t9\t5\t25\n" + `@@@@@@`
	o.Reconcile(ctx)
	got := strings.Join(f.ran, "\n")
	for _, want := range []string{
		"ip route replace default dev wg-nl table 1000",
		"ip rule add pref 10000 oif wg-nl lookup 1000",
		"ip rule add pref 10001 from 10.8.0.2 lookup 1000",
		"ip rule del pref 10010 oif old",
		"ip route replace 10.20.0.0/16 dev wg-nl proto 201",
	} {
		if !strings.Contains(got, want) {
			t.Fatalf("missing %q in\n%s", want, got)
		}
	}
	st := o.channelStatus(ctx, must(o.readChans()))
	if st[0].TX != 5 || st[0].RX != 9 || st[0].Endpoint != "198.51.100.1:51820" || st[0].State != "connecting" {
		t.Fatalf("status %+v", st[0])
	}
}

func TestChannelSystemNames(t *testing.T) {
	o, _ := chanOps(t)
	ctx := context.Background()
	px := func(title string) admproto.Channel {
		return admproto.Channel{Title: title, Type: admproto.ChanPX, On: true, Exit: true, PX: &admproto.PXConf{Kind: "socks5", Addr: "192.0.2.40:1080"}}
	}
	if _, err := o.ChannelSave(ctx, px("Офис · Test1")); err != nil {
		t.Fatal(err)
	}
	v, err := o.ChannelSave(ctx, px("  Дом  "))
	if err != nil {
		t.Fatal(err)
	}
	if v.Channels[0].Name != "px0" || v.Channels[0].Title != "Офис · Test1" || v.Channels[1].Name != "px1" || v.Channels[1].Title != "Дом" {
		t.Fatalf("%+v", v.Channels)
	}
	if _, err := o.ChannelSave(ctx, px("дом")); err == nil {
		t.Fatal("same title twice")
	}
	w := wgChan("")
	w.Title = "Нидерланды"
	if v, err := o.ChannelSave(ctx, w); err != nil || v.Channels[2].Name != "wg0" || v.Channels[2].Dev != "wg0" {
		t.Fatalf("%+v %v", v.Channels, err)
	}
}

func TestPrioMultiLimitsAuto(t *testing.T) {
	o, _ := chanOps(t)
	ctx := context.Background()
	if _, err := o.ChannelSave(ctx, wgChan("wg-nl")); err != nil {
		t.Fatal(err)
	}
	if _, err := o.ChannelPrio(ctx, admproto.Prio{Title: "Main", Use: []string{"wg-nl"}, Auto: true, Multi: true,
		Limits: map[string]admproto.PrioLimit{"wg-nl": {In: 50, Out: 10}, "gone": {In: 1}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := o.ChannelPrio(ctx, admproto.Prio{Title: "Bad", Limits: map[string]admproto.PrioLimit{"wg-nl": {In: -1}}}); err == nil {
		t.Fatal("negative limit saved")
	}
	px := admproto.Channel{Type: admproto.ChanPX, On: true, Exit: true, PX: &admproto.PXConf{Kind: "socks5", Addr: "192.0.2.40:1080"}}
	v, err := o.ChannelSave(ctx, px)
	if err != nil {
		t.Fatal(err)
	}
	p := v.Prios[0]
	if strings.Join(p.Use, " ") != "wg-nl px0" || len(p.Limits) != 1 || p.Limits["wg-nl"] != (admproto.PrioLimit{In: 50, Out: 10}) {
		t.Fatalf("list %+v", p)
	}
	eg, _ := os.ReadFile(o.egressFile())
	if !strings.Contains(string(eg), "prio prio1 wg-nl px0 then direct multi limit wg-nl 50/10\n") {
		t.Fatalf("egress.conf:\n%s", eg)
	}
}

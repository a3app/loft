package adm

// This file is the "Guard" tab of loft-panel-adm (the root helper behind the
// web panel): it writes fail2ban's configuration for the panel, ssh, mail
// and web scanners, lists the bans in force and bans or unbans by hand.
// File staging (stage, hostFile) is shared with mail.go.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/a3app/loft/server/panel/admproto"
)

// The guard is fail2ban with the panel's own jail file: failed logins of the
// panel, ssh and mail, and scanners asking nginx for /wp-login.php and the
// like, are banned at the firewall on every port. The jail file is written
// whole from the panel's settings and checked with fail2ban-client -t before
// a reload; a failed check puts the old files back.

const (
	guardJail   = "/etc/fail2ban/jail.d/loft-panel.conf"
	guardLocal  = "/etc/fail2ban/fail2ban.d/loft-panel.local"
	guardPanelF = "/etc/fail2ban/filter.d/loft-panel.conf"
	guardScanF  = "/etc/fail2ban/filter.d/loft-scan.conf"
	guardNoneF  = "/etc/fail2ban/filter.d/loft-manual.conf"
	guardActF   = "/etc/fail2ban/action.d/loft-nftables.conf"
	guardMark   = "# loft-panel guard v1"
	guardDB     = "/var/lib/fail2ban/fail2ban.sqlite3"
)

// manualJails hold bans made by hand, one jail per length.
var manualJails = map[int]string{86400: "loft-manual-1d", 604800: "loft-manual-7d", -1: "loft-manual-perm"}

// scanPaths are requests nobody but a scanner makes on this server: there is
// no WordPress, PHP admin or .env here.
var scanPaths = []string{`wp-login\.php`, `wp-admin`, `wp-content`, `xmlrpc\.php`, `\.env`, `\.git/`, `phpmyadmin`, `pma/`, `myadmin`,
	`vendor/phpunit`, `cgi-bin/`, `boaform`, `HNAP1`, `\.aws/`, `actuator`, `server-status`, `solr/`, `owa/`, `shell\?`, `\.DS_Store`, `config\.json`, `etc/passwd`}

// The nginx logs that may show scanners: the panel's site, the mail names,
// and the plain site probes of the node's name land on.
var scanLogs = []string{"/var/log/nginx/loft-panel.access.log", "/var/log/nginx/loft-panel-mail.access.log", "/var/log/nginx/access.log"}

// guardInstalled reports whether the fail2ban package is on the host.
func (o *Ops) guardInstalled() bool { return exists(o.sys("/usr/bin/fail2ban-client")) }

// cleanAllow normalises the never-ban list: single addresses and subnets in
// canonical form, blanks and duplicates dropped, at most 200 entries.
func cleanAllow(in []string) ([]string, error) {
	out := []string{}
	for _, a := range in {
		a = strings.TrimSpace(a)
		if a == "" {
			continue
		}
		if ip := net.ParseIP(a); ip != nil {
			a = ip.String()
		} else if _, n, err := net.ParseCIDR(a); err == nil {
			a = n.String()
		} else {
			return nil, fmt.Errorf("%q is not an address or a subnet", a)
		}
		if !slices.Contains(out, a) {
			out = append(out, a)
		}
	}
	if len(out) > 200 {
		return nil, errors.New("at most 200 allowed addresses")
	}
	return out, nil
}

// checkGuard checks the guard settings from the page against sane bounds
// (BanSec -1 means a ban for good) and cleans the allow list in place.
func checkGuard(c *admproto.GuardConf) error {
	if c.Fails < 1 || c.Fails > 100 {
		return errors.New("failed logins: 1 to 100")
	}
	if c.FindMin < 1 || c.FindMin > 1440 {
		return errors.New("window: 1 to 1440 minutes")
	}
	if c.BanSec != -1 && (c.BanSec < 60 || c.BanSec > 365*86400) {
		return errors.New("ban: a minute to a year, or for good")
	}
	var err error
	c.Allow, err = cleanAllow(c.Allow)
	return err
}

// renderGuardJail renders the panel's jail file. mail turns on the dovecot
// and postfix jails (only when the mail server is installed); logs are the
// nginx logs that exist, watched for scanners. The manual jails never match
// anything: they only hold bans made by hand.
func renderGuardJail(c admproto.GuardConf, mail bool, logs []string) string {
	on := func(b bool) string {
		if b {
			return "true"
		}
		return "false"
	}
	var b strings.Builder
	b.WriteString(guardMark + "\n# Written by loft-panel-adm. Edits are replaced when the guard is saved in the panel.\n\n")
	fmt.Fprintf(&b, "[DEFAULT]\nbantime = %d\nfindtime = %d\nmaxretry = %d\nignoreip = %s\n", c.BanSec, c.FindMin*60, c.Fails,
		strings.Join(append([]string{"127.0.0.1/8", "::1"}, c.Allow...), " "))
	b.WriteString("# Every port, TCP and UDP: a password guesser on ssh has no business on the\n# others, QUIC and WireGuard included. Subnets can be banned (loft-nftables).\nbanaction = loft-nftables\n# jail.conf hands its own protocol (tcp) to every action.\nprotocol = tcp,udp\nbackend = systemd\n\n")
	fmt.Fprintf(&b, "[sshd]\nenabled = %s\nbackend = systemd\n\n", on(c.Logins))
	fmt.Fprintf(&b, "[dovecot]\nenabled = %s\nbackend = systemd\njournalmatch = _SYSTEMD_UNIT=dovecot.service\n\n", on(c.Logins && mail))
	fmt.Fprintf(&b, "[postfix-sasl]\nenabled = %s\nbackend = systemd\nfilter = postfix[mode=auth]\njournalmatch = _SYSTEMD_UNIT=postfix@-.service\n\n", on(c.Logins && mail))
	b.WriteString("[loft-panel]\nenabled = true\nbackend = systemd\nfilter = loft-panel\njournalmatch = _SYSTEMD_UNIT=loft-panel.service\n\n")
	if c.Scanners && len(logs) > 0 {
		fmt.Fprintf(&b, "[loft-scan]\nenabled = true\nbackend = auto\nfilter = loft-scan\nlogpath = %s\nmaxretry = 1\nfindtime = 3600\n\n", strings.Join(logs, "\n          "))
	}
	for _, sec := range []int{86400, 604800, -1} {
		fmt.Fprintf(&b, "[%s]\nenabled = true\nbackend = systemd\nfilter = loft-manual\njournalmatch = SYSLOG_IDENTIFIER=loft-manual-never\nmaxretry = 1\nbantime = %d\n\n", manualJails[sec], sec)
	}
	return b.String()
}

const guardPanelFilter = guardMark + `
# The panel writes "login failed from <address>" for every wrong password.
[Definition]
failregex = login failed from <HOST>(?: |$)
journalmatch = _SYSTEMD_UNIT=loft-panel.service
`

const guardNoneFilter = guardMark + `
# Bans here are made by hand from the panel; the filter matches nothing.
[Definition]
failregex = ^loft-manual-never <HOST>$
`

// guardAction is nftables' allports action with interval sets (a manual
// ban may be a subnet; the stock set takes single addresses only and the
// ban fails in fail2ban's own log, nowhere else) and UDP as well as TCP.
const guardAction = guardMark + `
[INCLUDES]
before = nftables.conf

[Definition]
type = allports
# The stock _nft_add_set with "flags interval" on the set; its other lines
# add the rule that matches the set, and without them nothing is blocked.
_nft_add_set = <nftables> add set <table_family> <table> <addr_set> \{ type <addr_type>\; flags interval\; \}
              <_nft_for_proto-<type>-iter>
              <nftables> add rule <table_family> <table> <chain> %(rule_stat)s
              <_nft_for_proto-<type>-done>
`

// guardLocalConf keeps bans in fail2ban's database long enough that a ban
// for good survives restarts.
const guardLocalConf = guardMark + "\n[DEFAULT]\ndbpurgeage = 400d\n"

// renderScanFilter is the fail2ban filter that matches a request for one of
// scanPaths in an nginx access log line.
func renderScanFilter() string {
	return guardMark + "\n# nginx combined log: a request for a path that exists on no loft server.\n[Definition]\n" +
		`failregex = ^<HOST> \S+ \S+ .*?"(?:GET|POST|HEAD|PUT|PROPFIND) /+(?:[^" ]*/)?(?:` + strings.Join(scanPaths, "|") + `)` + "\n"
}

// Guard is the guard's state with the addresses banned now. Bans are read
// from fail2ban's database through guardListPy; a later row for the same
// jail and address replaces the earlier one, so each ban shows once. A
// failure to read them just shows no bans, so the tab still opens.
func (o *Ops) Guard(ctx context.Context) (admproto.GuardView, error) {
	v := admproto.GuardView{Installed: o.guardInstalled(), Banned: []admproto.GuardBan{}}
	if !v.Installed {
		return v, nil
	}
	if b, err := os.ReadFile(o.sys(guardJail)); err == nil && strings.HasPrefix(string(b), guardMark) {
		v.Configured = true
	}
	st, _ := o.host(ctx, "systemctl", "is-active", "fail2ban")
	v.Active = strings.TrimSpace(st)
	if v.Active != "active" {
		return v, nil
	}
	out, err := o.host(ctx, "python3", "-c", guardListPy)
	if err != nil {
		return v, nil
	}
	var rows [][]any
	if json.Unmarshal([]byte(strings.TrimSpace(out)), &rows) != nil {
		return v, nil
	}
	idx := map[string]int{}
	for _, r := range rows {
		if len(r) != 5 {
			continue
		}
		jail, _ := r[0].(string)
		ip, _ := r[1].(string)
		at, _ := r[2].(float64)
		bt, _ := r[3].(float64)
		what, _ := r[4].(string)
		b := admproto.GuardBan{IP: ip, Jail: jail, What: strings.TrimSpace(what), Since: time.Unix(int64(at), 0).UTC().Format(time.RFC3339)}
		if bt >= 0 {
			b.Until = time.Unix(int64(at+bt), 0).UTC().Format(time.RFC3339)
		}
		k := jail + " " + ip
		if i, ok := idx[k]; ok {
			v.Banned[i] = b
		} else {
			idx[k] = len(v.Banned)
			v.Banned = append(v.Banned, b)
		}
	}
	return v, nil
}

// guardListPy reads the bans that are in force from fail2ban's database —
// it keeps when, for how long and the matched line — and keeps those the
// server still holds (an unban leaves rows behind in older versions).
const guardListPy = `
import sqlite3, json, time, subprocess, ast
now = time.time()
try:
    cur = ast.literal_eval(subprocess.run(['fail2ban-client', 'banned'], capture_output=True, text=True, timeout=15).stdout.strip() or '[]')
    held = set((j, ip) for d in cur for j, ips in d.items() for ip in ips)
except Exception:
    held = None
out = []
try:
    c = sqlite3.connect('file:` + guardDB + `?mode=ro', uri=True)
    for jail, ip, t, bt, data in c.execute('select jail, ip, timeofban, bantime, data from bans order by timeofban'):
        if bt is not None and bt >= 0 and t + bt <= now:
            continue
        if held is not None and (jail, ip) not in held:
            continue
        m = ''
        try:
            d = json.loads(data) if isinstance(data, str) else {}
            ms = d.get('matches') or []
            m = ms[-1] if ms else ''
            if isinstance(m, list):
                m = ''.join(map(str, m))
        except Exception:
            pass
        out.append([jail, ip, t, -1 if bt is None else bt, str(m)[:300]])
except Exception:
    pass
print(json.dumps(out))
`

// GuardSet writes fail2ban's configuration and reloads it.
func (o *Ops) GuardSet(ctx context.Context, c admproto.GuardConf) (admproto.GuardView, error) {
	if !o.guardInstalled() {
		return admproto.GuardView{}, errors.New("the guard package (fail2ban) is not installed")
	}
	if err := checkGuard(&c); err != nil {
		return admproto.GuardView{}, err
	}
	var logs []string
	for _, l := range scanLogs {
		if exists(o.sys(l)) {
			logs = append(logs, l)
		}
	}
	// 1. Render every file and remember the current ones (or that a file did
	// not exist), so a refused configuration can be put back exactly.
	files := []hostFile{
		{Path: guardJail, Data: renderGuardJail(c, o.mailInstalled(), logs), Mode: "0644"},
		{Path: guardLocal, Data: guardLocalConf, Mode: "0644"},
		{Path: guardPanelF, Data: guardPanelFilter, Mode: "0644"},
		{Path: guardScanF, Data: renderScanFilter(), Mode: "0644"},
		{Path: guardNoneF, Data: guardNoneFilter, Mode: "0644"},
		{Path: guardActF, Data: guardAction, Mode: "0644"},
	}
	var undo []hostFile
	var gone []string
	for _, f := range files {
		b, err := os.ReadFile(o.sys(f.Path))
		if err != nil {
			gone = append(gone, f.Path)
			continue
		}
		f.Data = string(b)
		undo = append(undo, f)
	}
	// 2. Install the files and let fail2ban test them; on an error restore the
	// old files and delete new ones.
	put, err := o.stage(files)
	if err != nil {
		return admproto.GuardView{}, err
	}
	if out, err := o.host(ctx, "sh", "-c", "set -e\n"+put+"fail2ban-client -t 2>&1 | tail -5"); err != nil || strings.Contains(out, "ERROR") {
		p, _ := o.stage(undo)
		for _, g := range gone {
			p += "rm -f " + shq(g) + "\n"
		}
		o.host(ctx, "sh", "-c", p)
		return admproto.GuardView{}, fmt.Errorf("fail2ban did not accept the configuration: %s", lastLines(out+" "+errText(err), 4))
	}
	// A restart, not a reload: the ban sets are made anew with the action's
	// flags. Bans in force come back from fail2ban's database.
	if _, err := o.host(ctx, "sh", "-c", "systemctl enable --quiet fail2ban; systemctl restart fail2ban"); err != nil {
		return admproto.GuardView{}, fmt.Errorf("fail2ban did not reload: %v", err)
	}
	return o.Guard(ctx)
}

// errText is err's message, or "" for nil.
func errText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}

// banTarget accepts an address or a subnet to ban and returns it in
// canonical form.
func banTarget(s string) (string, error) {
	if ip := net.ParseIP(s); ip != nil {
		return ip.String(), nil
	}
	if _, n, err := net.ParseCIDR(s); err == nil {
		return n.String(), nil
	}
	return "", fmt.Errorf("%q is not an address or a subnet", s)
}

// GuardBan bans an address by hand for 1 day, 7 days or for good.
func (o *Ops) GuardBan(ctx context.Context, b admproto.GuardBan) (admproto.GuardView, error) {
	ip, err := banTarget(strings.TrimSpace(b.IP))
	if err != nil {
		return admproto.GuardView{}, err
	}
	jail, ok := manualJails[b.Sec]
	if !ok {
		return admproto.GuardView{}, errors.New("ban: 1 day, 7 days or for good")
	}
	if _, err := o.host(ctx, "fail2ban-client", "set", jail, "banip", ip); err != nil {
		return admproto.GuardView{}, fmt.Errorf("ban %s: %v", ip, err)
	}
	return o.Guard(ctx)
}

// GuardUnban lifts every ban of an address.
func (o *Ops) GuardUnban(ctx context.Context, b admproto.GuardBan) (admproto.GuardView, error) {
	ip, err := banTarget(strings.TrimSpace(b.IP))
	if err != nil {
		return admproto.GuardView{}, err
	}
	if _, err := o.host(ctx, "fail2ban-client", "unban", ip); err != nil {
		return admproto.GuardView{}, fmt.Errorf("unban %s: %v", ip, err)
	}
	return o.Guard(ctx)
}

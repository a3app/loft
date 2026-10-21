// Package catalog is the closed list of packages the panel can put on the
// server. The browser names a package by Key only; what to install, which
// repository and key, which unit and which data directories come from here,
// on both sides of the root helper's socket.
package catalog

import (
	_ "embed"
	"errors"
	"fmt"
	"sort"
)

// Groups, in the order the panel shows them.
const (
	GroupBase    = "base"    // the node, nginx, the built-in SSTP client; not removable
	GroupProto   = "proto"   // protocol servers for clients of this server
	GroupVPN     = "vpn"     // VPN clients for upstream channels
	GroupMail    = "mail"    // mail server, spam filter, webmail
	GroupProtect = "protect" // fail2ban
)

// Methods.
const (
	MethodBase = "base" // installed with the panel; state only
	MethodApt  = "apt"
	// MethodRelease downloads a project's GitHub release, checked against
	// the digest file published next to it.
	MethodRelease = "release"
	MethodBin     = "bin" // GitHub release tarball; SHA-256 pinned in Bin
)

// Release is where a release package comes from and goes to.
type Release struct {
	Repo   string            // owner/name on GitHub
	Assets map[string]string // GOARCH → zip asset; the digest is ASSET.dgst (SHA2-256= …)
	Dir    string            // unpacked here
	Bin    string            // the program in Dir; "Bin version" prints its version
}

// Repo is a third-party apt repository. Key is the armored signing key,
// shipped in the binary so installing never trusts a download for it.
// The suite is the OS codename (%s in URL is replaced by it).
type Repo struct {
	Name string // file stem under /etc/apt/{sources.list.d,keyrings}
	URL  string
	Key  string
	// Codenames the repository publishes; an OS outside this list is refused
	// before apt runs, with a clear error.
	Codenames []string
}

// Bin is a binary from a GitHub release. Asset uses %s for GOARCH (amd64, arm64).
// SHA256 is hex per GOARCH, pinned here so install never trusts a checksum file.
type Bin struct {
	Repo   string            // owner/name, e.g. 9seconds/mtg
	Tag    string            // v2.2.8
	Asset  string            // mtg-2.2.8-linux-%s.tar.gz
	Name   string            // file inside the tarball
	Path   string            // install path, e.g. /usr/lib/loft-panel/mtproto/mtg
	SHA256 map[string]string // GOARCH → hex
}

// Package is one entry.
type Package struct {
	Key   string
	Group string
	// Method: base, apt, bin or release.
	Method string
	// Apt packages; the first one carries the version shown, unless Shows names another.
	Apt   []string
	Shows string
	Repo  *Repo
	Bin   *Bin
	// Unit is the systemd service, empty for a tool without one.
	Unit string
	// Ports as shown before install, e.g. "9993 udp".
	Ports string
	// Files are written after install (path → content) and removed with the package.
	Files map[string]string
	// DataDirs are deleted only when the operator asks to wipe data.
	DataDirs []string
	// Extra apt packages installed alongside and marked automatic, so a
	// remove never takes away something another program relies on.
	Extra []string
	// SizeMB is shown before install (apt knows only after the repository is added).
	SizeMB int
	// Preseed answers debconf before apt asks (noninteractive apt takes the
	// defaults, and some are wrong for this server).
	Preseed []string
	// Requires is the key of a package this one is useless without: it is
	// installed with or after it and removed with or before it.
	Requires string
	Release  *Release
}

// Version is the apt package whose version the panel shows.
func (p Package) Version() string {
	if p.Shows != "" {
		return p.Shows
	}
	if len(p.Apt) > 0 {
		return p.Apt[0]
	}
	return ""
}

// Base reports whether the package comes with the panel and stays.
func (p Package) Base() bool { return p.Method == MethodBase }

//go:embed keys/zerotier.asc
var zerotierKey string

var all = []Package{
	{Key: "node", Group: GroupBase, Method: MethodBase, Unit: "loft-node.service", Ports: "443 tcp"},
	{Key: "nginx", Group: GroupBase, Method: MethodBase, Apt: []string{"nginx"}, Unit: "nginx.service", Ports: "80, 8443"},
	{Key: "sstp", Group: GroupBase, Method: MethodBase},
	// HTTP, HTTPS and SOCKS5 proxies as channels: the node itself dials them.
	{Key: "proxy", Group: GroupBase, Method: MethodBase},

	// Telegram MTProto proxy (9seconds/mtg). Not an apt package: the helper
	// downloads the pinned GitHub release and checks SHA-256 from this file.
	// Default listen port is 8448: 443 belongs to loft-node.
	{Key: "mtproto", Group: GroupProto, Method: MethodBin,
		Unit: "loft-mtproto.service", Ports: "8448 tcp", SizeMB: 6,
		DataDirs: []string{"/var/lib/loft-mtproto"},
		Files:    map[string]string{"/etc/systemd/system/loft-mtproto.service": mtprotoUnit},
		Bin: &Bin{Repo: "9seconds/mtg", Tag: "v2.2.8", Asset: "mtg-2.2.8-linux-%s.tar.gz", Name: "mtg",
			Path: "/usr/lib/loft-panel/mtproto/mtg",
			SHA256: map[string]string{
				"amd64": "7ef19d079d85f4e00d4f8334ec1f3f3c8718e3d0ed1f3109ea9a8673138a2102",
				"arm64": "562a94dd4cafcb8f179b76cfeafb76da12747c8e230bc76235bf8746cc189644",
			}}},

	{Key: "wireguard", Group: GroupVPN, Method: MethodApt, Apt: []string{"wireguard-tools"}, SizeMB: 1},
	{Key: "openvpn", Group: GroupVPN, Method: MethodApt, Apt: []string{"openvpn"}, SizeMB: 2},
	// Xray-core for vless://, trojan:// and ss:// channels. Its own unit, as a
	// dynamic user, with the config (it holds secrets) passed as a credential.
	{Key: "xray", Group: GroupVPN, Method: MethodRelease, SizeMB: 32,
		Release: &Release{Repo: "XTLS/Xray-core", Assets: map[string]string{"amd64": "Xray-linux-64.zip", "arm64": "Xray-linux-arm64-v8a.zip"},
			Dir: "/usr/local/lib/loft-panel/xray", Bin: "xray"},
		Unit:  "loft-panel-xray.service",
		Files: map[string]string{"/etc/systemd/system/loft-panel-xray.service": xrayUnit}},
	{Key: "zerotier", Group: GroupVPN, Method: MethodApt, Apt: []string{"zerotier-one"},
		Repo: &Repo{Name: "zerotier", URL: "https://download.zerotier.com/debian/%s", Key: zerotierKey,
			Codenames: []string{"bullseye", "bookworm", "trixie", "focal", "jammy", "noble"}},
		Unit: "zerotier-one.service", Ports: "9993 udp", DataDirs: []string{"/var/lib/zerotier-one"}, SizeMB: 9},

	// Mail is three packages, not one: receiving, mailboxes and reading in a
	// browser install and break separately, and the operator must see which.
	// The Mail screen writes their configuration; the package only installs.
	{Key: "mail", Group: GroupMail, Method: MethodApt,
		Apt:   []string{"postfix", "dovecot-imapd", "dovecot-pop3d", "dovecot-lmtpd", "dovecot-sieve", "dovecot-managesieved", "opendkim"},
		Extra: []string{"opendkim-tools"},
		// Internet Site: Postfix starts and receives; the default answer
		// differs between releases.
		Preseed: []string{"postfix postfix/main_mailer_type select Internet Site"},
		// One unit for the package, Postfix: without it no mail arrives.
		Unit: "postfix.service", Ports: "25, 465, 587, 993, 995, 4190", DataDirs: []string{"/var/vmail"}, SizeMB: 23},
	{Key: "antispam", Group: GroupMail, Method: MethodApt, Requires: "mail",
		Apt: []string{"rspamd"}, Unit: "rspamd.service", DataDirs: []string{"/var/lib/rspamd"}, SizeMB: 39},
	// Not the "roundcube" package: it pulls apache2 and MariaDB, a second web
	// server next to nginx and a database for a list of settings. The core,
	// sqlite and php-fpm instead. php-zip: without it Roundcube logs an error
	// on every message (the zip-download plugin needs it at load time).
	{Key: "webmail", Group: GroupMail, Method: MethodApt, Requires: "mail", Shows: "roundcube-core",
		Apt: []string{"php-fpm", "roundcube-core", "roundcube-sqlite3", "roundcube-plugins", "php-gd", "php-enchant", "php-zip"},
		Preseed: []string{
			// nginx is configured by the panel; the default is apache2, lighttpd.
			"roundcube-core roundcube/reconfigure-webserver multiselect ",
			"roundcube-core roundcube/restart-webserver boolean false",
			// Its own database: settings and address book.
			"roundcube-core roundcube/dbconfig-install boolean true",
			"roundcube-core roundcube/database-type select sqlite3",
			"roundcube-core roundcube/hosts string localhost",
		},
		DataDirs: []string{"/var/lib/roundcube", "/var/lib/dbconfig-common/sqlite3/roundcube"}, SizeMB: 70},

	// Ubuntu 24.04 has no /var/log/auth.log (journal only): the stock sshd
	// jail would not start. The journal backend works on Debian and Ubuntu.
	{Key: "fail2ban", Group: GroupProtect, Method: MethodApt, Apt: []string{"fail2ban"}, Extra: []string{"python3-systemd"},
		Unit: "fail2ban.service", SizeMB: 6,
		Files:    map[string]string{"/etc/fail2ban/jail.d/loft-panel.conf": "# Written by loft-panel; removed with the package.\n[sshd]\nenabled = true\nbackend = systemd\n"},
		DataDirs: []string{"/var/lib/fail2ban"}},
}

// mtprotoUnit is loft-mtproto.service. The listen port and secret live in
// /var/lib/loft-mtproto/mtg.toml, so changing them does not rewrite this file.
const mtprotoUnit = `[Unit]
Description=loft-panel Telegram MTProto proxy
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
User=nobody
Group=nogroup
ExecStart=/usr/lib/loft-panel/mtproto/mtg run /var/lib/loft-mtproto/mtg.toml
Restart=on-failure
RestartSec=2
NoNewPrivileges=yes

[Install]
WantedBy=multi-user.target
`

// All returns the catalog in display order.
func All() []Package { return append([]Package(nil), all...) }

// Find returns the package with key.
func Find(key string) (Package, bool) {
	for _, p := range all {
		if p.Key == key {
			return p, true
		}
	}
	return Package{}, false
}

// Plan is one job's change: install, remove (wiping data when Purge), upgrade.
type Plan struct {
	Install []string `json:"install,omitempty"`
	Remove  []string `json:"remove,omitempty"`
	Upgrade []string `json:"upgrade,omitempty"`
	Purge   bool     `json:"purge,omitempty"`
}

// Dependents are the packages that require key.
func Dependents(key string) []string {
	var out []string
	for _, p := range all {
		if p.Requires == key {
			out = append(out, p.Key)
		}
	}
	return out
}

// Consistent checks plan against what is installed: a package goes in only
// with what it requires, and leaves only with what requires it.
func (p Plan) Consistent(installed map[string]bool) error {
	will := map[string]bool{}
	for k, v := range installed {
		will[k] = v
	}
	for _, k := range p.Remove {
		will[k] = false
	}
	for _, k := range p.Install {
		will[k] = true
	}
	for _, pk := range all {
		if pk.Requires != "" && will[pk.Key] && !will[pk.Requires] {
			if installed[pk.Key] {
				return fmt.Errorf("package %q needs %q: remove it too", pk.Key, pk.Requires)
			}
			return fmt.Errorf("package %q needs %q: install it first or together", pk.Key, pk.Requires)
		}
	}
	return nil
}

// Empty reports a plan with nothing to do.
func (p Plan) Empty() bool { return len(p.Install)+len(p.Remove)+len(p.Upgrade) == 0 }

// Check refuses unknown or base keys, duplicates and a key in two lists, and
// sorts each list into catalog order.
func (p *Plan) Check() error {
	if p.Empty() {
		return errors.New("nothing to do")
	}
	seen := map[string]bool{}
	for _, l := range []*[]string{&p.Install, &p.Remove, &p.Upgrade} {
		for _, k := range *l {
			pk, ok := Find(k)
			if !ok {
				return fmt.Errorf("unknown package %q", k)
			}
			if pk.Base() {
				return fmt.Errorf("package %q comes with the panel", k)
			}
			if seen[k] {
				return fmt.Errorf("package %q listed twice", k)
			}
			seen[k] = true
		}
		sort.Slice(*l, func(i, j int) bool { return order((*l)[i]) < order((*l)[j]) })
	}
	return nil
}

func order(key string) int {
	for i, p := range all {
		if p.Key == key {
			return i
		}
	}
	return len(all)
}

const xrayUnit = `# Written by loft-panel; removed with the Xray client package.
[Unit]
Description=Xray-core for loft-panel channels
After=network-online.target
Wants=network-online.target
ConditionPathExists=/etc/loft-panel/xray.json

[Service]
DynamicUser=yes
LoadCredential=config.json:/etc/loft-panel/xray.json
Environment=XRAY_LOCATION_ASSET=/usr/local/lib/loft-panel/xray
ExecStart=/usr/local/lib/loft-panel/xray/xray run -c ${CREDENTIALS_DIRECTORY}/config.json
Restart=on-failure
RestartSec=3
NoNewPrivileges=yes
ProtectSystem=strict
ProtectHome=yes
PrivateTmp=yes

[Install]
WantedBy=multi-user.target
`

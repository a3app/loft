// Package admproto is the wire between loft-panel (no privileges) and
// loft-panel-adm (root). One JSON request per connection, one JSON reply.
// The daemon accepts only the ops listed here.
package admproto

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/a3app/loft/server/panel/catalog"
)

// SocketPath is where loft-panel-adm listens.
const SocketPath = "/run/loft-panel-adm/adm.sock"

// Size limits for either direction.
const (
	MaxRequest = 1 << 20
	MaxReply   = 4 << 20
)

// Ops.
const (
	OpPing         = "ping"          // → Ping
	OpNetworkGet   = "network.get"   // → Network
	OpNetworkApply = "network.apply" // NetworkApply → Network
	OpMemberKey    = "member.key"    // MemberKey → IssuedKey
	OpMemberGet    = "member.get"    // MemberKey → IssuedKey (plaintext already in members.keys)
	OpNodeStatus   = "node.status"   // → NodeStatus
	OpNodeRestart  = "node.restart"  // → NodeStatus
	OpNodeInfo     = "node.info"     // → NodeInfo
	OpDomainSet    = "domain.set"    // DomainSet → NodeInfo
	OpCertInstall  = "cert.install"  // CertInstall → NodeStatus

	OpPackages      = "packages.state" // → []Package
	OpPackagesApply = "packages.apply" // catalog.Plan → Job (started)
	OpPackageUnit   = "package.unit"   // PackageUnit → Package
	OpMTProtoGet    = "mtproto.get"    // → MTProto
	OpMTProtoSet    = "mtproto.set"    // MTProtoSet → MTProto
	OpUpdateApply   = "update.apply"   // UpdateApply → Job (started)
	OpJobGet        = "job.get"        // → Job (the current or last one, with its log tail)

	OpTimezone    = "system.tz"     // → Timezone
	OpTimezoneSet = "system.tz.set" // Timezone (Zone only) → Timezone

	OpNetState   = "net.state"   // → NetState
	OpNetChange  = "net.change"  // NetChange → NetPending (live, undone after 2 min unless confirmed)
	OpNetConfirm = "net.confirm" // NetID → nothing
	OpNetRevert  = "net.revert"  // NetID → nothing

	OpPresence   = "node.presence"     // → raw node-status.json
	OpNodeConfig = "node.config"       // → NodeConfig
	OpNodeSave   = "node.save"         // NodeConfig → NodeConfig (restarts the node, or reloads for relays)
	OpNodeLists  = "node.lists"        // NodeLists → NodeConfig (reload)
	OpNodeLog    = "node.log"          // → NodeLog
	OpNodeToken  = "node.token.rotate" // → NodeConfig (restart)

	OpChannels      = "channels.get"    // → ChannelsView
	OpChannelSave   = "channels.save"   // Channel → ChannelsView
	OpChannelDelete = "channels.delete" // ChannelName → ChannelsView
	OpChannelSwitch = "channels.switch" // ChannelSwitch → ChannelsView
	OpChannelRules  = "channels.rules"  // ExitRules → ChannelsView
	// OpChannelPrio adds (empty ID) or changes a priority list.
	OpChannelPrio       = "channels.prio"        // Prio → ChannelsView
	OpChannelPrioDelete = "channels.prio.delete" // ChannelName (the list ID) → ChannelsView

	OpMail       = "mail.get"    // → MailView
	OpMailDomain = "mail.domain" // MailDomainSave → MailView
	OpMailBox    = "mail.box"    // MailBoxSave → MailView
	OpMailAlias  = "mail.alias"  // MailAliasSave → MailView
	OpMailCerts  = "mail.certs"  // MailCerts → MailView (only when they changed)
	OpMailApply  = "mail.apply"  // → MailView (writes everything again)
	OpMailStats  = "mail.stats"  // → MailStats

	OpSysUsers      = "sys.users"       // → SysUsers
	OpSysUserSave   = "sys.user.save"   // SysUserSave → SysUsers
	OpSysUserDelete = "sys.user.delete" // SysUserDelete → SysUsers

	OpGuard      = "guard.get"   // → GuardView
	OpGuardSet   = "guard.set"   // GuardConf → GuardView
	OpGuardBan   = "guard.ban"   // GuardBan → GuardView
	OpGuardUnban = "guard.unban" // GuardBan (IP) → GuardView

	OpBackupKey     = "backup.key"     // backup.Key (public half) → BackupState
	OpBackupState   = "backup.state"   // → BackupState
	OpBackupRun     = "backup.run"     // BackupJob → Job (started)
	OpBackupRestore = "backup.restore" // BackupJob → Job (started)
	OpBackupDir     = "backup.dir"     // BackupDir → []BackupFile (and checks it is writable)
)

// Request is one call. By is the panel login, for the daemon's log only.
type Request struct {
	Op   string          `json:"op"`
	By   string          `json:"by,omitempty"`
	Body json.RawMessage `json:"body,omitempty"`
}

// Reply is ok with Data, or an error string meant for the operator.
type Reply struct {
	OK    bool            `json:"ok"`
	Error string          `json:"error,omitempty"`
	Data  json.RawMessage `json:"data,omitempty"`
}

// Ping answers who is listening.
type Ping struct {
	Version string `json:"version"`
}

// Network is network.conf as text plus its SHA-256, which the next apply
// must quote back so two editors cannot overwrite each other blindly.
type Network struct {
	Text   string `json:"text"`
	SHA256 string `json:"sha256"`
	Legacy bool   `json:"legacy,omitempty"` // read from network.yaml
}

// NetworkApply replaces network.conf and restarts the node.
// Expect is Network.SHA256 seen by the editor; empty means "file must not exist".
type NetworkApply struct {
	Text   string `json:"text"`
	Expect string `json:"expect"`
}

// MemberKey issues a fresh lt_ key for member ID into members.keys.
// The caller then puts KeySHA256 into network.conf and applies.
type MemberKey struct {
	ID string `json:"id"`
}

// IssuedKey is the plaintext member secret. The panel shows it on issue;
// with keep_member_keys it may show it again from its own store.
type IssuedKey struct {
	ID        string `json:"id"`
	Key       string `json:"key"`
	KeySHA256 string `json:"key_sha256"`
}

// NodeStatus is systemd's view of loft-node.
type NodeStatus struct {
	Active  string `json:"active"` // active | inactive | failed | activating | …
	Since   string `json:"since,omitempty"`
	Version string `json:"version,omitempty"`
}

// ErrUnavailable means the daemon could not be reached or spoke garbage.
var ErrUnavailable = errors.New("loft-panel-adm is not available")

// Client calls the daemon at Path.
type Client struct {
	Path string
	By   string
}

// Call sends op with body (nil for none) and decodes Data into out (nil to skip).
func (c Client) Call(ctx context.Context, op string, body, out any) error {
	path := c.Path
	if path == "" {
		path = SocketPath
	}
	if _, ok := ctx.Deadline(); !ok {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
	}
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", path)
	if err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	defer conn.Close()
	if dl, ok := ctx.Deadline(); ok {
		conn.SetDeadline(dl)
	}
	req := Request{Op: op, By: c.By}
	if body != nil {
		if req.Body, err = json.Marshal(body); err != nil {
			return err
		}
	}
	if err := json.NewEncoder(conn).Encode(req); err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	var rep Reply
	dec := json.NewDecoder(io.LimitReader(conn, MaxReply))
	if err := dec.Decode(&rep); err != nil {
		return fmt.Errorf("%w: %v", ErrUnavailable, err)
	}
	if !rep.OK {
		if rep.Error == "" {
			rep.Error = "operation failed"
		}
		return errors.New(rep.Error)
	}
	if out != nil && len(rep.Data) > 0 {
		return json.Unmarshal(rep.Data, out)
	}
	return nil
}

// NodeInfo is what a member link needs from node.conf and the controller seed.
type NodeInfo struct {
	Advertise     string   `json:"advertise"`      // https://host[:port]
	Host          string   `json:"host"`           // host of Advertise
	GetPath       string   `json:"get_path"`       // GET front path
	WSPath        string   `json:"ws_path"`        // WebSocket front path
	RosterPath    string   `json:"roster_path"`    // signed roster, the client bootstrap
	Fronts        []string `json:"fronts"`         // enabled; empty = GET
	Decoy         []string `json:"decoy"`          // splice origins
	Mask          bool     `json:"mask"`           // pbk would be needed (not exported here)
	ControllerPub string   `json:"controller_pub"` // Ed25519 hex, for client pinning
	Panel         string   `json:"panel"`          // names the panel answers to (PANEL_HOSTS)
}

// DomainSet names the server: Name is what clients dial (node advertise);
// Panel, when different, is the panel's own name, so a probe of the node
// does not find the panel. Empty Panel = the panel answers on Name.
type DomainSet struct {
	Name  string `json:"name"`
	Panel string `json:"panel,omitempty"`
}

// CertInstall replaces a certificate: Target "node" (default; nginx uses it
// too while the panel shares the node's name) or "panel" (its own name).
type CertInstall struct {
	Fullchain string `json:"fullchain"`
	Key       string `json:"key"`
	Target    string `json:"target,omitempty"`
}

// Package is one catalog entry as it stands on the server.
type Package struct {
	Key       string `json:"key"`
	Group     string `json:"group"`
	Base      bool   `json:"base,omitempty"`
	Service   bool   `json:"service,omitempty"` // has a unit: can be switched off
	Installed bool   `json:"installed"`
	Version   string `json:"version,omitempty"`   // dpkg version or the node's
	Candidate string `json:"candidate,omitempty"` // newer apt version, when there is one
	Active    string `json:"active,omitempty"`    // systemctl is-active
	Enabled   bool   `json:"enabled,omitempty"`
	Ports     string `json:"ports,omitempty"`
	DiskMB    int    `json:"disk_mb,omitempty"` // installed size, or the size an install would take
	Busy      string `json:"busy,omitempty"`    // install | remove | upgrade while a job runs it
}

// PackageUnit switches a package's service on (enable --now) or off.
type PackageUnit struct {
	Key string `json:"key"`
	On  bool   `json:"on"`
}

// MTProto is the Telegram MTProto proxy as it stands: secret, port, TLS
// domain, and the share links for Telegram clients.
type MTProto struct {
	Installed bool   `json:"installed"`
	Host      string `json:"host,omitempty"`
	Port      int    `json:"port,omitempty"`
	Domain    string `json:"domain,omitempty"`
	Secret    string `json:"secret,omitempty"`
	Link      string `json:"link,omitempty"`       // tg://proxy?…
	LinkHTTPS string `json:"link_https,omitempty"` // https://t.me/proxy?…
	Version   string `json:"version,omitempty"`
}

// MTProtoSet changes the proxy. A new TLS domain always mints a new secret
// (the domain is part of it). RotateSecret mints one with the current domain.
type MTProtoSet struct {
	Port         int    `json:"port,omitempty"`
	Domain       string `json:"domain,omitempty"`
	RotateSecret bool   `json:"rotate_secret,omitempty"`
}

// UpdateApply installs release Tag (v1.2.3) of node and panel from GitHub.
type UpdateApply struct {
	Tag string `json:"tag"`
}

// Job is one long change run by the helper outside its sandbox, in the
// transient unit loft-panel-job. It survives restarts of the panel and of
// the helper itself (an update restarts both).
type Job struct {
	ID      string        `json:"id"`
	Kind    string        `json:"kind"` // packages | update | backup | restore
	By      string        `json:"by,omitempty"`
	Plan    *catalog.Plan `json:"plan,omitempty"`
	Tag     string        `json:"tag,omitempty"`
	Backup  *BackupJob    `json:"backup,omitempty"`
	State   string        `json:"state"`         // running | done | error | interrupted
	Now     string        `json:"now,omitempty"` // current step: install:zerotier, download, …
	Error   string        `json:"error,omitempty"`
	Started time.Time     `json:"started"`
	Ended   time.Time     `json:"ended,omitzero"`
	Log     string        `json:"log,omitempty"` // tail, in replies only
}

// Timezone is the server's zone and, in replies, the zones it knows.
type Timezone struct {
	Zone  string   `json:"zone"`
	Zones []string `json:"zones,omitempty"`
}

// NetState is the server's addresses and routes; Pending is a change that
// waits for confirmation.
type NetState struct {
	Addrs   []NetAddr   `json:"addrs"`
	Routes  []NetRoute  `json:"routes"`
	Devs    []string    `json:"devs"`
	Pending *NetPending `json:"pending,omitempty"`
}

// NetAddr is one global address.
type NetAddr struct {
	Addr    string   `json:"addr"` // CIDR
	Dev     string   `json:"dev"`
	Gateway string   `json:"gateway,omitempty"`
	Primary bool     `json:"primary,omitempty"`
	Panel   bool     `json:"panel,omitempty"`  // added by the panel: can be removed
	Listen  []string `json:"listen,omitempty"` // "loft-node :443/tcp"
	PTR     string   `json:"ptr,omitempty"`    // filled by the panel
	Channel string   `json:"channel,omitempty"`
}

// NetRoute is one route of the main table.
type NetRoute struct {
	Dst    string `json:"dst"`
	Via    string `json:"via,omitempty"`
	Dev    string `json:"dev"`
	Source string `json:"source"` // system | panel | channel
}

// Net is what the panel owns: /etc/loft-panel/net.json.
type Net struct {
	Addrs  []NetAddrSpec  `json:"addrs,omitempty"`
	Routes []NetRouteSpec `json:"routes,omitempty"`
}

// NetAddrSpec is an address the panel added.
type NetAddrSpec struct {
	Addr string `json:"addr"`
	Dev  string `json:"dev"`
}

// NetRouteSpec is a route the panel added.
type NetRouteSpec struct {
	Dst string `json:"dst"`
	Via string `json:"via,omitempty"`
	Dev string `json:"dev"`
}

// NetChange is one change: add-addr / del-addr (Addr, Dev) or add-route /
// del-route (Dst, Via, Dev).
type NetChange struct {
	Op   string `json:"op"`
	Addr string `json:"addr,omitempty"`
	Dst  string `json:"dst,omitempty"`
	Via  string `json:"via,omitempty"`
	Dev  string `json:"dev"`
}

// NetPending is a live change held until Deadline.
type NetPending struct {
	ID       string    `json:"id"`
	Change   NetChange `json:"change"`
	Prev     Net       `json:"prev"`
	Next     Net       `json:"next"`
	Deadline time.Time `json:"deadline"`
}

// NetID names a held change.
type NetID struct {
	ID string `json:"id"`
}

// Channel types.
const (
	ChanWG   = "wg"   // WireGuard (wg-quick, package)
	ChanSSTP = "sstp" // SSTP, the node's own client
	ChanOVPN = "ovpn" // OpenVPN (package)
	ChanZT   = "zt"   // ZeroTier network (package)
	ChanPX   = "px"   // HTTP, HTTPS or SOCKS5 proxy, in the node
	ChanXR   = "xr"   // Xray link (package)
)

// Channel is one upstream channel. Secrets go in and never come out: a
// reply blanks them and sets Has….
type Channel struct {
	Name   string     `json:"name"`  // system name (wg0, zt1…), set by the helper on create
	Title  string     `json:"title"` // what the panel shows; any text
	Type   string     `json:"type"`
	On     bool       `json:"on"`
	Exit   bool       `json:"exit"`             // may carry members' internet traffic (rules, default)
	Accept bool       `json:"accept,omitempty"` // take connections on the channel's external IP
	Fail   string     `json:"fail,omitempty"`   // before priority lists: direct | block | another channel; moved into a list on read
	Check  *ChanCheck `json:"check,omitempty"`  // link probe; nil = none
	Via    string     `json:"via,omitempty"`    // reach this channel's server through another channel
	Table  int        `json:"table,omitempty"`  // routing table, assigned by the helper
	Dev    string     `json:"dev,omitempty"`    // kernel interface, set by the helper

	WG   *WGConf   `json:"wg,omitempty"`
	SSTP *SSTPConf `json:"sstp,omitempty"`
	OVPN *OVPNConf `json:"ovpn,omitempty"`
	ZT   *ZTConf   `json:"zt,omitempty"`
	PX   *PXConf   `json:"px,omitempty"`
	XR   *XRConf   `json:"xr,omitempty"`
}

// ChanCheck is a channel's link probe: while it fails, priority lists skip the channel.
type ChanCheck struct {
	Kind  string `json:"kind"`            // ping (wg, ovpn, zt) | tcp
	Probe string `json:"probe"`           // ping: IP or host; tcp: host:port
	Every int    `json:"every,omitempty"` // seconds; 0 = 10
	Fails int    `json:"fails,omitempty"` // failures in a row before "no link"; 0 = 3
}

// Prio is a priority list: channels in order, then Last.
type Prio struct {
	ID    string   `json:"id"`    // set by the helper (prio1, prio2…); rules name it
	Title string   `json:"title"` // what the panel shows
	Use   []string `json:"use"`   // channel names in order
	Last  string   `json:"last"`  // direct | block
}

// WGConf is the interface and its peers.
type WGConf struct {
	PrivateKey string   `json:"private_key,omitempty"`
	HasKey     bool     `json:"has_key,omitempty"`
	PublicKey  string   `json:"public_key,omitempty"`
	Address    string   `json:"address"` // CIDR, IPv4 and/or IPv6, comma-separated
	ListenPort int      `json:"listen_port,omitempty"`
	MTU        int      `json:"mtu,omitempty"`
	Peers      []WGPeer `json:"peers"`
}

// WGPeer is one peer.
type WGPeer struct {
	Name       string   `json:"name,omitempty"`
	PublicKey  string   `json:"public_key"`
	PSK        string   `json:"psk,omitempty"`
	HasPSK     bool     `json:"has_psk,omitempty"`
	Endpoint   string   `json:"endpoint,omitempty"`
	AllowedIPs []string `json:"allowed_ips"`
	Keepalive  int      `json:"keepalive,omitempty"`
}

// SSTPConf is a server and a login.
type SSTPConf struct {
	Server      string `json:"server"` // host[:port]
	User        string `json:"user"`
	Password    string `json:"password,omitempty"`
	HasPassword bool   `json:"has_password,omitempty"`
	Auth        string `json:"auth,omitempty"` // auto | mschapv2 | pap
	Insecure    bool   `json:"insecure,omitempty"`
}

// OVPNConf is a .ovpn file and an optional login.
type OVPNConf struct {
	Config      string `json:"config"`
	User        string `json:"user,omitempty"`
	Password    string `json:"password,omitempty"`
	HasPassword bool   `json:"has_password,omitempty"`
	Routes      bool   `json:"routes,omitempty"` // take routes the server pushes
}

// ZTConf is a ZeroTier network.
type ZTConf struct {
	Network string `json:"network"` // 16 hex
	Managed bool   `json:"managed"` // take addresses and routes from the network
}

// PXConf is a proxy.
type PXConf struct {
	Kind        string `json:"kind"` // http | https | socks5
	Addr        string `json:"addr"` // host:port
	User        string `json:"user,omitempty"`
	Password    string `json:"password,omitempty"`
	HasPassword bool   `json:"has_password,omitempty"`
	UDP         string `json:"udp,omitempty"` // direct | proxy | block
}

// XRConf is an Xray link.
type XRConf struct {
	Link string `json:"link"`
	Port int    `json:"port,omitempty"` // local SOCKS port, assigned by the helper
}

// ExitRule is one rule: Match member | tag | list, Value, Via a priority list ID, direct or block.
type ExitRule struct {
	Match string `json:"match"`
	Value string `json:"value"`
	Via   string `json:"via"`
}

// ExitRules are the rules in order and where the rest goes.
type ExitRules struct {
	Rules   []ExitRule `json:"rules"`
	Default string     `json:"default"`
}

// ChannelName names a channel.
type ChannelName struct {
	Name string `json:"name"`
}

// ChannelSwitch turns a channel on or off.
type ChannelSwitch struct {
	Name string `json:"name"`
	On   bool   `json:"on"`
}

// ChannelStatus is what a channel is doing now.
type ChannelStatus struct {
	Name       string   `json:"name"`
	State      string   `json:"state"` // up | down | connecting | nolink | off | denied
	Detail     string   `json:"detail,omitempty"`
	Dev        string   `json:"dev,omitempty"`
	Addrs      []string `json:"addrs,omitempty"`
	TX         uint64   `json:"tx"`
	RX         uint64   `json:"rx"`
	Since      string   `json:"since,omitempty"`
	Handshake  int64    `json:"handshake,omitempty"` // WireGuard: unix time of the newest handshake
	Endpoint   string   `json:"endpoint,omitempty"`
	NetName    string   `json:"net_name,omitempty"` // ZeroTier
	NodeID     string   `json:"node_id,omitempty"`
	ExternalIP string   `json:"external_ip,omitempty"`
	Conns      int64    `json:"conns,omitempty"`
}

// ChannelsView is everything the External channels tab shows.
type ChannelsView struct {
	Channels []Channel       `json:"channels"`
	Status   []ChannelStatus `json:"status"`
	Rules    ExitRules       `json:"rules"`
	Prios    []Prio          `json:"prios"`
	Lists    []string        `json:"lists"` // named lists in node.conf
}

// Relay is one allowlisted relay (enrol list).
type Relay struct {
	ID     string   `json:"id"`
	URL    string   `json:"url"`
	Fronts []string `json:"fronts,omitempty"`
	Weight int      `json:"weight,omitempty"`
	Drain  bool     `json:"drain,omitempty"`
}

// NodeConfig is node.conf as the panel edits it, plus what it shows.
type NodeConfig struct {
	Role       string   `json:"role"`
	Listen     string   `json:"listen"`
	Advertise  string   `json:"advertise"`
	Fronts     []string `json:"fronts"`
	GetH2      int      `json:"get_h2"`
	Decoy      []string `json:"decoy"`
	Mask       bool     `json:"mask"`
	CoverDir   string   `json:"cover_dir"`
	GetPath    string   `json:"get_path"`
	WSPath     string   `json:"ws_path"`
	H2Path     string   `json:"h2_path"`
	RosterPath string   `json:"roster_path"`
	ListsPath  string   `json:"lists_path"`
	Enrol      string   `json:"enrol"`
	AuthShared bool     `json:"auth_shared"`
	Trace      bool     `json:"trace"`
	Relays     []Relay  `json:"relays"`
	CDNPeers   []string `json:"cdn_peers"`

	// shown only
	MaskPub       string              `json:"mask_pub,omitempty"`
	ControllerPub string              `json:"controller_pub,omitempty"`
	MemberKeys    int                 `json:"member_keys"`
	CertUntil     time.Time           `json:"cert_until,omitzero"`
	CertNames     []string            `json:"cert_names,omitempty"`
	Lists         map[string][]string `json:"lists"`
	Status        json.RawMessage     `json:"status,omitempty"` // node-status.json
	Node          NodeStatus          `json:"node"`
}

// NodeLists replaces lists.conf.
type NodeLists struct {
	Lists map[string][]string `json:"lists"`
}

// NodeLog is the service log tail.
type NodeLog struct {
	Text string `json:"text"`
}

// Mail. The helper keeps what the operator asked for in
// /etc/loft-panel/mail.json and writes Postfix, Dovecot and OpenDKIM from it;
// nothing is read back from their files.

// MailDomain is a domain this server takes mail for. Its server name is
// always mail.<domain>: MX, IMAP and SMTP for programs, webmail.
type MailDomain struct {
	Name    string `json:"name"`
	Enabled bool   `json:"enabled"` // off: boxes stay, no mail is taken
	// CatchAll gets mail for addresses that do not exist; empty refuses it.
	CatchAll []string `json:"catch_all,omitempty"`
	DKIM     bool     `json:"dkim"`
	Selector string   `json:"selector,omitempty"` // DKIM selector; default "mail"
	// Set by the helper.
	DKIMRecord string `json:"dkim_record,omitempty"` // the TXT value for <selector>._domainkey
	Cert       bool   `json:"cert,omitempty"`        // a certificate for mail.<domain> is installed
	CertUntil  string `json:"cert_until,omitempty"`  // its expiry, RFC 3339
}

// MailBox is a mailbox; the address is the login.
type MailBox struct {
	Address  string   `json:"address"`
	Owner    string   `json:"owner,omitempty"` // a panel login, for the list only
	Hash     string   `json:"hash,omitempty"`  // {ARGON2ID}…, in only: never sent back
	QuotaMB  int      `json:"quota_mb"`        // 0: no limit
	Forwards []string `json:"forwards,omitempty"`
	SendOnly bool     `json:"send_only,omitempty"` // mail to it from outside is refused
	Enabled  bool     `json:"enabled"`
	// Set by the helper.
	UsedMB  float64 `json:"used_mb"`
	Created string  `json:"created,omitempty"`
}

// MailAlias delivers From (an address in a mail domain) to To.
type MailAlias struct {
	From string   `json:"from"`
	To   []string `json:"to"`
}

// MailDomainSave adds, changes or removes a domain. Removing it removes its
// boxes and aliases; DropMail also deletes the letters on disk.
type MailDomainSave struct {
	MailDomain
	Delete   bool `json:"delete,omitempty"`
	DropMail bool `json:"drop_mail,omitempty"`
}

// MailBoxSave adds, changes or removes a box. An empty Hash on a change
// keeps the password.
type MailBoxSave struct {
	MailBox
	Delete   bool `json:"delete,omitempty"`
	DropMail bool `json:"drop_mail,omitempty"`
}

// MailAliasSave adds, changes or removes an alias. Old is the alias being
// changed (empty for a new one).
type MailAliasSave struct {
	MailAlias
	Old    string `json:"old,omitempty"`
	Delete bool   `json:"delete,omitempty"`
}

// MailCert is a certificate for a mail server name (mail.<domain>).
type MailCert struct {
	Host      string `json:"host"`
	Fullchain string `json:"fullchain"`
	Key       string `json:"key"`
}

// MailCerts is every mail certificate; names not listed lose theirs.
type MailCerts struct {
	Certs []MailCert `json:"certs"`
}

// MailService is one daemon of the mail server.
type MailService struct {
	Key   string `json:"key"` // postfix | dovecot | opendkim | rspamd
	State string `json:"state"`
}

// MailView is the Mail screen.
type MailView struct {
	Installed bool          `json:"installed"`
	Dovecot   string        `json:"dovecot,omitempty"` // version
	Helo      string        `json:"helo,omitempty"`    // the name Postfix greets with
	Domains   []MailDomain  `json:"domains"`
	Boxes     []MailBox     `json:"boxes"`
	Aliases   []MailAlias   `json:"aliases"`
	Services  []MailService `json:"services"`
	Webmail   bool          `json:"webmail"`         // Roundcube is installed
	WebHosts  []string      `json:"web_hosts"`       // where it answers
	Error     string        `json:"error,omitempty"` // the last apply failed
}

// MailStats is the queue and the last 24 hours.
type MailStats struct {
	Queue    int `json:"queue"`
	Received int `json:"received"` // delivered into boxes
	Sent     int `json:"sent"`     // sent out
	Rejected int `json:"rejected"` // refused at SMTP
}

// Linux accounts: root and the people's accounts (UID 1000 and up). System
// accounts are not shown and cannot be changed.

// SysUser is one account.
type SysUser struct {
	Login    string   `json:"login"`
	UID      int      `json:"uid"`
	Sudo     string   `json:"sudo"` // "" | password | nopasswd
	Keys     []string `json:"keys"`
	Password bool     `json:"password"` // has a usable password (for sudo)
	Locked   bool     `json:"locked"`   // the account is expired: no login at all
	Root     bool     `json:"root,omitempty"`
}

// SysUsers is the list and how sshd lets people in.
type SysUsers struct {
	Users        []SysUser `json:"users"`
	PasswordAuth bool      `json:"password_auth"` // sshd takes passwords
	RootLogin    string    `json:"root_login"`    // sshd's PermitRootLogin
}

// SysUserSave creates (New) or changes an account. An empty Password keeps
// the current one.
type SysUserSave struct {
	Login    string   `json:"login"`
	New      bool     `json:"new,omitempty"`
	Sudo     string   `json:"sudo"`
	Keys     []string `json:"keys"`
	Password string   `json:"password,omitempty"`
	Lock     bool     `json:"lock"`
}

// SysUserDelete removes an account; Home removes its home directory too.
type SysUserDelete struct {
	Login string `json:"login"`
	Home  bool   `json:"home,omitempty"`
}

// The guard is fail2ban, written by the panel: failed logins (the panel,
// ssh, mail) and scanners are banned at the firewall, on every port.

// GuardConf is what the operator sets.
type GuardConf struct {
	Fails    int      `json:"fails"`    // failed logins…
	FindMin  int      `json:"find_min"` // …within this many minutes
	BanSec   int      `json:"ban_sec"`  // ban for; -1: for good
	Scanners bool     `json:"scanners"` // ban requests for /wp-login.php, /.env and the like
	Logins   bool     `json:"logins"`   // count ssh and mail logins too (the panel's always)
	Allow    []string `json:"allow"`    // never banned: addresses and subnets
}

// GuardBan is one banned address. Sec is the ban's length for a manual
// ban (-1: for good); Until is empty for a ban for good.
type GuardBan struct {
	IP     string `json:"ip"`
	Jail   string `json:"jail,omitempty"`
	What   string `json:"what,omitempty"` // the request or line that got it banned
	Since  string `json:"since,omitempty"`
	Until  string `json:"until,omitempty"`
	Sec    int    `json:"sec,omitempty"`
	Reason string `json:"reason,omitempty"` // the operator's note (manual bans; kept by the panel)
}

// GuardView is the guard's state.
type GuardView struct {
	Installed  bool       `json:"installed"`
	Active     string     `json:"active,omitempty"`
	Configured bool       `json:"configured"` // the panel's jail file is in place
	Banned     []GuardBan `json:"banned"`
}

// Backups: the helper makes and restores copies (as root, in the job unit);
// the panel keeps the schedule and the stores and carries copies to S3 and
// SFTP. Copies are made in /var/lib/loft-panel/backup.

// BackupFile is a copy on disk.
type BackupFile struct {
	Name string    `json:"name"`
	Size int64     `json:"size"`
	At   time.Time `json:"at"`
}

// BackupState is the key and the copies kept on the server.
type BackupState struct {
	Key   *BackupKey   `json:"key,omitempty"`
	Local []BackupFile `json:"local"`
}

// BackupKey is the public half of the recovery key and its salt.
type BackupKey struct {
	Public string    `json:"public"`
	Salt   string    `json:"salt"`
	Set    time.Time `json:"set"`
}

// BackupJob is a backup or a restore. For a backup Dir is a directory store
// to copy into (keeping Keep copies); for a restore File is a copy in the
// local directory or a path in a directory store, and Phrase opens it.
type BackupJob struct {
	Parts  []string `json:"parts"`
	Dir    string   `json:"dir,omitempty"`
	Keep   int      `json:"keep,omitempty"`
	File   string   `json:"file,omitempty"`
	Phrase string   `json:"phrase,omitempty"` // restore only; dropped from the job file once read
	// Set by the job.
	Name string `json:"name,omitempty"` // the copy made
	Size int64  `json:"size,omitempty"`
	Pre  string `json:"pre,omitempty"` // the copy made before a restore
}

// BackupDir is a directory store.
type BackupDir struct {
	Dir string `json:"dir"`
}

# loft · server panel

**English** · [Русский](DESIGN_ru.md) · [中文](DESIGN_zh.md)

Interactive mock: [`index.html`](index.html) (open in a browser; UI text is Russian, English and Chinese come with the build). Brand: [`brand/`](../../../docs/brand/). Package: [../README.md](../README.md).

---

## 1. Product

One server, one owner. The panel installs and configures a loft node and what lives next to it. Nothing is installed unless the operator picks it in **Packages**. Panel users and loft members are different things: a user logs in to the panel, a member holds an `lt_…` key.

Install: one command (`curl | sh` or `.deb`), then a five-step wizard in the browser. Step 5 is the first package choice; nothing is preselected.

Languages: Russian is the source; `en.js` and `zh.js` map every Russian string to English and Simplified Chinese, and `i18n.js` swaps the drawn page. The header select EN / RU / 中文 (and the select on the sign-in card, in the phone menu, and in the wizard) switches English / Russian / 中文 per browser; the panel default is in Settings. A test fails when a Russian string has no English or Chinese.

## 2. Shell

| Part | Size |
|------|------|
| Top bar | 64 px, ink. Mark from `docs/brand/svg` only; search (⌘K / Ctrl+K), language, the “login · role” button with a menu of My account and Sign out |
| Help | (?) by the title and Help in the phone menu open a side panel: what the section is for, common tasks, what to check when something is wrong, a link to the panel guide |
| Menu | 264 px, collapses to a 72 px rail; ≤ 599 px full screen behind ☰ |
| Page | padding 0 32 56 (24 at ≤ 959, 16 on phone); title 28/36 (20/28) + (?) + description |
| Blocks | title, description, buttons (40 px, r4, gap 16), table |
| Tables | rows 48, first column sticky, side scroll inside the table only |
| Edit | dialog 500 px centered; full screen on phone with sticky footer |
| Fields | label on the border |

Palette: Ink `#1A1714`, Stone `#B7A99A`, Copper `#C67B3A`, Lamp `#E8B84A`, Snow `#F3EDE4`, Paper `#E8DFD2`, Mute `#8A7E72`. Font: Avenir Next. VLAN colours add four muted tones to Copper, Lamp and Stone.

## 3. Menu

| Group | Screens |
|-------|---------|
| Status | Overview |
| Internet | Connections, Connection priorities (access policies · applying policies), Server addresses |
| loft network | Node, Members, Segments |
| Domains and mail | Domains (zones, records, certificates, DNS providers), Mail (with the mail server package) |
| Management | Packages, Users (list · roles · sessions · log), System (guard, backups, Linux accounts), Settings |

A screen or a checkbox that needs a package not installed is **hidden**, not disabled.

## 4. Segments: a network is a managed switch

Each loft network looks like a switch. A member is a port. A port is in one VLAN (access); the node has no trunk. The model is `netconf.Network`; nothing is stored outside `network.conf`.

| Tab | What |
|-----|------|
| Ports and VLAN | Front panel: gateway port, then member ports. Colour = VLAN, LED = online, struck label = disabled. Multi-select → set VLAN, enable, disable. VLAN membership grid (VLAN × port, click to move). VLAN table. Port table |
| Access rules | Effective access grid VLAN × VLAN after the ACL and `peers`; partial allows (proto/port/host) are noted in the cell. Ordered ACL list, ↑↓, rule dialog with a live config line |
| Network settings | CIDR, gateway, pool, `peers` hub/mesh/vlan, p2p, DNS, MTU (default 1280), session timers, routes |
| Config | `network.conf` in node syntax. Two-way: text edits update the switch, switch edits rewrite the text. Error shows the line number with the node's own message |

The panel parser and renderer mirror `ParseCommands`, `RenderCommands` and `ParseACLLine` in `internal/netconf`. Output must round-trip through the Go code unchanged.

Draft vs applied: every change stays in a draft. A bar shows how many lines differ; **What changed** shows a diff with context; **Apply** has the node re-read the network live; **Revert** drops the draft.

## 5. Node

Tabs: Main (role, listen, advertise, certificate from Domains, fronts — default GET; ws/h2/quic/h3 opt-in, `get_h2`), Masking (decoy list, `mask` and `pbk`, cover site, request paths), Relays and CDN (`enrol`, `relays[]`, `cdn_peers`), Lists (signed lists for clients; egress rules reuse them), Access (controller key, shared token, token rotation, trace).

QUIC and HTTP/3 stay selectable while decoy is on: the node advertises `quic_mark` / `h3_mark` instead of raw quic/h3. Member `loft://` includes query `pbk` when mask is on.

## 6. Internet

**Addresses and routes.** Address table: address (primary, added in the panel, channel), interface, gateway, who listens, PTR. Route table: destination, via, interface, source (system, panel, channel). The panel adds and removes only its own addresses and routes; system ones are left alone. A change is live at once and undone after 2 minutes unless confirmed on the bar (a timer on the server, so it works without the panel; a reboot also drops an unconfirmed change). What the panel owns is in `/etc/loft-panel/net.json`, applied at boot.

**External channels.** Sections follow the installed packages; SSTP and proxies are built into loft and always there. A row has a switch, the title with the system name and the state under it (up, connecting, down, off, waiting for authorisation), and delete. The title is free text up to 40 characters, for the panel only; the system name (`wg0`, `ovpn0`, `zt0`, `sstp0`, `px0`, `xr0` — the next free number) is set by the panel on create and never changes.

| Section | Table | Dialog |
|---------|-------|--------|
| WireGuard (package) | connection, address, peer, sent, received, peer address and port, latest handshake | private key + key pair generation, public key, address in the tunnel, listen port; peers: name, public and preshared key, address and port, allowed subnets, keepalive; folded: MTU, link check. A `.conf` upload fills the dialog |
| VPN connections | connection, VPN type, server, sent, received, address | SSTP: server, user, password, authentication (Auto, MS-CHAP v2, PAP), skip the certificate check. OpenVPN (package): `.ovpn` text or upload, user and password; folded: take routes (never the default route) |
| ZeroTier (package) | connection, network, network ID, local ID, IP, sent, received | network ID (16 hex), authorisation hint; folded: take addresses and routes from the network |
| Proxies | connection, type, address, login, sent, received, via | HTTP / HTTPS / SOCKS5, address:port, no login or user and password; folded: connect through, UDP (direct, through SOCKS5, block) |
| Xray (package) | connection, server, via, sent, received | a `vless://` `trojan://` `ss://` link, parsed; connect through WireGuard, OpenVPN, ZeroTier or a proxy |

Every dialog has **Use for internet exit** (the channel can be picked in rules) and **Link check** (off, ping or TCP connect; address, interval, failures in a row before “no link”). WireGuard, OpenVPN and ZeroTier can **take incoming connections on the channel's external IP**. Passwords and keys are not shown after saving.

**Connections** is its own menu item: the former “External channels” page (WireGuard, VPN, ZeroTier, proxies, Xray, exit rules by address list, incoming through a channel). **Server addresses** is its own menu item: the former “Addresses and routes” page.

**Connection priorities** — its own menu item under Internet, two tabs with their own hashes (`#iface-priorities`, `#iface-priorities/use`; `#prio` still opens this page). The left column is the same on both tabs. System rows, not deletable: **Default policy** (members with no assignment leave directly through the host, eth0; channels are not in this policy) and **No internet access** (empty, members in it do not leave). Then the operator’s policies. **Internet access policies**: for a user policy, every connection with “Use for internet exit”: a drag handle (order), a “use” tick, a state dot, the name, system name · type · state, “now” on the current one (failover), the speed limit (↓/↑ Mbit/s or —) and a pencil → the “Speed limit” dialog (incoming and outgoing: off or 1…500 Mbit/s, shared by everyone who leaves by the policy). Below the table: “Add new connections automatically”, “If none of them works” (direct or block). Ticks, dragging and fields apply at once. Failover: the first working channel, the next on failure; multipath: new connections take the working channels in turn. A channel is skipped while it is off, not connected or failing its link check. The default policy’s pane can still point leftover traffic at a named policy if that was already stored. **Applying policies**: on the right “Network members” and “Tags” as chips; a chip dragged onto a policy on the left is a member or tag exit rule; a member rule beats a tag; drop on the default policy clears the rule. Address lists stay in the exit rules on “Connections”.

**Exit rules**: a member, a tag or an address list from `node.conf` → a policy (or direct, block); checked top down, arrows reorder; the “Default policy” row is where everything else goes. The end of a row is the policy’s channel order, a link to “Connection priorities”. The server itself always leaves directly; so do ICMP and DNS queries to the node.

**Incoming through a channel**: channel, external address, address in the channel, who listens.

How it works: every kernel channel has its own routing table and an `oif` rule, so only the node's sockets bound to it use it — the server's default route never changes. WireGuard subnets go into the main table. The node gets `egress.conf` and re-reads it without a restart; it holds SSTP and proxies itself, Xray is a separate unit with one local SOCKS inbound per channel.

## 6a. loft node

Tabs: **Main** (state: role, version, up since, who is on TCP/UDP 443, member keys; restart and service log; listen; role, client address and certificate read-only, changed in the wizard; fronts — with a web server in front, UDP/443 is shared: marked loft QUIC goes to the node, other packets to the web server; HTTP/2 connections for GET), **Masking** (web servers behind the node — the first is the panel’s own; handshake masking and the pbk key; site directory; request paths), **Relays and CDN** (relays with weight, fronts and retire; who may connect; state, connections and response from the node’s report; Cloudflare with ranges fetched on a button), **Lists** (named lists of domains, .suffixes and subnets; create, load from a file, edit), **Access** (controller key, shared token, node token change, session log). Changes pile up in a draft; Apply restarts the node, relays apply without a restart. The node reports who is online every 5 s: Members shows devices online / total and last seen, Segments lights the ports.

## 7. Packages

Table: package, state, version, ports / disk, actions (settings, update to, install, on/off, remove). A tool without a service (WireGuard, OpenVPN, SSTP) shows “installed” and has no on/off; the base is never switched off (the panel is reached through it). Remove asks first, with an optional “wipe data”. Groups: Base (node, web server, panel; not removable), Protocol servers (Telegram MTProto proxy on mtg, later VLESS / Trojan / Shadowsocks on Xray-core), VPN clients, Mail (server, Rspamd, webmail), Guard (guard, fail2ban). The MTProto package installs a pinned GitHub release of 9seconds/mtg (SHA-256 in the catalog), listens on TCP 8448 (443 belongs to loft-node), and its settings dialog gives the `tg://proxy` link for Telegram clients. A **Running now** card shows the job log while it runs; afterwards **Last job** stays with its log for 10 minutes after success and until the next job after a failure. Updates come from GitHub Releases: mode (default: check and notify), channel, window, SHA256SUMS, rollback; “Update to X” appears when a newer release is found. The “also update packages” option comes with Xray, which is what it would update.

## 8. Sign-in, wizard, domains, mail

- **Sign-in** (`#login`): full screen, user, password, language; “Can't sign in” → `sudo loft-panel passwd admin`.
- **Wizard** (`#setup`): Administrator → Domain and DNS (Cloudflare, DigitalOcean, Namecheap or manual; records the panel will create) → Certificate (Let's Encrypt, DNS-01 or HTTP-01, progress) → First member (QR and `loft://`) → Packages (nothing preselected; antispam and webmail appear under the mail server; with a choice the panel opens on Packages, showing the install). Settings can reopen it.
- **Domains** (`#domains`): zones table and all certificates; a zone row opens its own page `#domains/<zone>` with a “← Domains” link: how DNS is kept and the registration date, records (panel-made ones marked and updated on IP change; a manual zone adds a **Check** column — present / missing / different value — and a **Check records** button; from 9 records, search by name, type and value and a type filter), **Zone settings**, certificates for the zone’s names with **Issue certificate** for it. A zone page’s address can be bookmarked: after sign-in the panel opens it. New domain: zone (suggested from the provider’s zones), where DNS is managed — an added access or Manual, create A/AAAA for the server names and CAA right away; mail records come from the Mail section. Deleting a zone asks first: the records at the provider stay. Record: an existing one keeps its type and name, priority for MX/SRV, TTL auto / 5 minutes / 1 hour / 1 day, “Update when the server IP changes” for A/AAAA. Certificate: names, HTTP-01 / DNS-01 check, where used (loft node, panel, mail when the mail package is on; fixed once issued), **Issue again**; the node’s and the panel’s certificates cannot be deleted. PEM upload asks where to use it. DNS providers: the accesses with delete; adding checks the access (**Check and add**), Namecheap also asks for the API user and shows the server IP to allow.
- **Mail**: its own list of mail domains (typed by name, with the zones of Domains as suggestions); each has the server name mail.<domain> — MX, IMAP/SMTP for programs, webmail. Domain dialog: accept mail (a disabled domain keeps its mailboxes), DKIM signing (the panel makes the key; selector under “More”), catch-all; delete asks whether to erase the letters. Mailboxes (owner, quota bar, forward, state), aliases and the catch-all row; DNS records with their values (MX, A/AAAA of mail.<domain>, SPF of the domain and of the server name, DKIM, DMARC, PTR) with “Create missing” for zones with a DNS provider — existing SPF and DMARC are left alone; the certificate of mail.<domain> with “Issue”; services, queue and the last day’s counts, mail program settings. Mailbox dialog: password (generator; shown once after saving), quota, forward copies, send-only, enabled, client settings. If an apply fails the Postfix/Dovecot check, the previous files are restored and a bar shows the error with “Apply again”.
- **Package picker**: presets (everything, as now), search, groups; state word and checkbox on the right; base locked; antispam and webmail show only under the mail server; footer counts what will be installed and removed; “wipe data” only when something is removed.
- **System**: protection is fail2ban (the Guard package); the block shows when it is installed: failed panel logins always, ssh and mail logins by a checkbox, scanners (requests for /wp-login.php, /.env and the like) on the first hit; bans cover every port; the same numbers drive the panel’s own login lock; the allowed list says whether your address is in it; a manual ban for 24 h, 7 days or for good with a reason (your own address, the loopback and the allowed list are refused); the banned table with reason and time left, “Lift”. Backups: schedule (daily or Sundays at 04:00, or by hand), how many to keep, where (a store or this server only — the two newest always stay on the server), what goes in (node and panel always, mail by choice); a copy is encrypted to the recovery phrase’s key — the server keeps only the public half and shows the phrase once (“Written down”); history with the outcome and “Restore”; the restore dialog: the copy (from history, the copy before a restore, from a file), the parts (only those in the copy), the phrase; before swapping anything the panel makes a copy of the current state. Stores: S3 (address, bucket and path, keys), SFTP (the server’s own key, the host key remembered), a directory on the server; “Check access” writes and removes a probe. Linux accounts (root and people, UID 1000 and up): no sudo / sudo with a password / sudo without one, an optional password (for sudo; shown once), SSH keys, a temporary lock, delete with or without the home directory; the panel will not leave the server without an account that logs in by key and can become root; the caption says whether sshd takes passwords.
- **Users**: user dialog (role, password or a 24 h invitation — the link is shown once and opens an “Invitation to the panel” page, API token issue/rotate/revoke — shown once, can sign in), role dialog with a section × none/view/change matrix; the server checks the role on every request and the menu shows only visible sections. Sessions with end, API token rows with revoke; a log of every change made through the panel and the API, filtered by who.
- **Overview**: six cards — node (state, role, address, listen, fronts, other traffic, certificate, version, up since), members (on / online / off), network (main and extra IPs, IPv6, channel addresses), domains, server (OS, CPU, memory, disk, traffic over a day), events (the latest log entries, with rights to Users). The foot of the phone menu shows the real node state.
- **My account** (`#me`, from the “login · role” menu and an icon in the phone menu): login, role, last sign-in and the role’s rights; password change with the current password — the other own sessions end; own API token (issue, reissue, revoke, shown once); own sessions with End and End the others; Sign out. Open to every role without rights to Users.
- **Search** (top bar button, ⌘K / Ctrl+K): sections, members, connections, zones, mailboxes, users and packages — only what the role sees; ↑ ↓ and Enter; a pick opens the section and the object itself.
- **Log**: labels for every change; what the panel does by itself is written as “system” — certificate renewal (and failure), new server addresses in DNS, a scheduled backup, an automatic update, a sign-in block.
- **Wizard, Domain and DNS step**: the records table has a Now column — what DNS holds now (“already there”, another value or “no record”); under the name, “Zone · registered until”.
- **Segments, Config**: the node’s parser checks the text too; its error with the line blocks Apply.
- **Settings**: run the wizard again (from Domain and DNS); the panel address is read-only, names and certificates change in the wizard; default language (English, Russian, Chinese); server time zone (the update window follows it); idle sign-out and session lifetime; keep member keys (off by default — the panel then stores the secret so the operator can show the link, QR and .loft again without reissuing; turning it off wipes the panel copy, the node file stays); .loft outbound name parts (checkboxes host / member / front, default member+front, e.g. `bob-get`); notifications through an external SMTP server (recipient, sender, server, port, encryption, login, password — never shown after saving), events: certificate ends in 14 days, update or rollback, node did not start, a scheduled backup failed; a test letter.
- **Footer and “About loft panel”**: at the bottom right of every page — “Runs on loft-panel <version>” (a button that opens “About loft panel”) and “Made with love ♥ by A³” (a name, not translated). The “About loft panel” page: what the panel does, the developer, open components, the version. /api/state returns the version to signed-in users only.

## 9. Needs in the node

| Gap | Blocks |
|-----|--------|
| Status API (leases, sessions, relays, traffic) | online LEDs, last seen, counters |
| Several networks per node | second network in Segments |
| Egress via a chosen interface | per-member egress rules |
| Routes and tags used | routes pushed to members |
| Proxy dialer in loft and SSTP clients | **Connect via** for those channels |

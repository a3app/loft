# loft-panel

**English** · [Русский](README_ru.md) · [中文](README_zh.md)

Web administration of one loft server is provided: the node, its networks and members, IP addresses and upstream channels, domains with DNS records and certificates, mail, packages, and panel users. The aim is that a loft server may be installed and configured without files being edited by hand.

UX spec: [design/DESIGN.md](design/DESIGN.md). Interactive mock: [design/index.html](design/index.html).

## Status

First working slice. Real today:

- sign-in, sessions, first owner by setup code, `loft-panel passwd` / `users`;
- **Segments**: the node's `network.conf` in the switch view and the config editor, apply by live reload (the node re-reads the files; only affected members reconnect) and automatic rollback;
- new members get a real `lt_` key in `members.keys`; removed members lose theirs on apply; the panel can keep a copy of the key so the operator can show the link, QR and `.loft` again (Settings, off by default);
- node state on the overview;
- the setup wizard: owner, domain and DNS (Cloudflare, DigitalOcean, Namecheap or by hand; the panel creates A/AAAA and CAA), Let’s Encrypt certificates, first member, first packages;
- **Domains**: zones, DNS records (through a provider’s API or by hand, with a check of manual records), Let’s Encrypt certificates (HTTP-01 and DNS-01) and PEM upload; auto A/AAAA follow the server’s addresses;
- **Packages**: install, upgrade, switch off and remove packages from a closed catalog (Telegram MTProto proxy, WireGuard, OpenVPN, ZeroTier, fail2ban; the node, nginx and SSTP as the base), with the job log on the page;
- **loft node**: node.conf from the screen (listen, fronts, web servers behind the node, masking, paths, relays, CDN ranges, shared token, session log), address lists, node token change, the service log; who is online, from the node’s status file;
- **Users**: roles with a section × none/view/change matrix (checked on every request), invitations, API tokens (`Authorization: Bearer lpt_…`), sessions, a log of changes;
- **Network and IP**: server addresses and routes (a change is undone after 2 minutes unless confirmed); upstream channels — WireGuard, SSTP, OpenVPN, ZeroTier, HTTP/HTTPS/SOCKS5 proxies, Xray — and exit rules by member, tag or address list, with fall-backs; incoming through a channel;
- **Mail**: mail domains, mailboxes, DNS records and the certificate of mail.<domain> (when the mail package is installed);
- **System**: fail2ban, encrypted backups and Linux accounts;
- **Update from releases**: node and panel from GitHub Releases, checked against SHA256SUMS, rolled back if the services do not come up.

## Layout

```
server/cmd/loft-panel       web server, own user, no privileges
server/cmd/loft-panel-adm   root helper (Linux), unix socket, fixed op list
server/panel/web            HTTP: /api, sign-in, embedded UI (ui/index.html + ui/live.js)
server/panel/adm            what the helper does: network.conf, members.keys, systemctl
server/panel/admproto       panel ↔ helper wire
server/panel/catalog        the packages the panel may install (keys, apt names, GitHub binaries, repos, units)
server/panel/auth           argon2id, sessions, login limits
server/panel/store          users.json, settings.json, member-keys.json
server/panel/debian         install.sh, .deb, systemd units, nginx site
server/panel/devstand       real panel + helper on a laptop (fake systemctl)
server/panel/design         UX spec and mock
```

`web/ui/index.html` is generated: `make panel-ui` copies `design/index.html` and adds `live.js`, which swaps sample data for API calls where a backend exists. It also copies `en.js`, `zh.js` and `i18n.js` (Russian source, English and Simplified Chinese). The mock opened from disk stays a mock.

## Install

The panel comes first and brings up the node: the tarball carries loft-node in `node/` and installs it when the server has none. On a server that already runs loft-node, the panel goes on top.

```bash
make dist-panel     # dist/panel/linux-{amd64,arm64}: tarball with install.sh, and .deb
sudo sh install.sh --host panel.example.net        # from the unpacked tarball
sudo apt install ./loft-panel_VERSION_amd64.deb    # or the package (Depends: loft-node)
```

Both create the `loft-panel` user, install nginx with the panel site on 127.0.0.1:8443, point the node's decoy there with `decoy_proxy enable` (`loft-panel-adm wire-node`; a decoy that is someone else's site is left alone), start both services and print the setup code. Re-running is the upgrade; `--uninstall` / `--remove` undo it.

## Build and try

```bash
make panel          # loft-panel (host) and loft-panel-adm (linux)
make panel-dev      # http://127.0.0.1:8091, setup code 000000-000000
go test ./server/panel/...
```

## Ports

```
:443  loft-node ── loft client ──▶ overlay
          └─ everything else ──▶ nginx 127.0.0.1:8443 ──▶ panel 127.0.0.1:8091, webmail, Xray by SNI
:80   nginx (ACME HTTP-01)
:8448 Telegram MTProto proxy (when the package is installed)
```

## Safety

- The helper accepts only the panel's uid (`SO_PEERCRED`) on a 0660 socket, rejects unknown ops and unknown JSON fields, and caps request size.
- `network.conf` is checked with the node's own parser before anything is written. Writes are atomic with fsync; the old file stays as `network.conf.prev`.
- An apply must quote the SHA-256 of the file it was based on, so two editors cannot overwrite each other.
- Apply reloads the node (SIGHUP; a node unit without ExecReload is restarted instead). If `loft-node` is not active a few seconds later, the old `network.conf` and `members.keys` are put back and the node reloaded again.
- Packages: the browser sends catalog keys only; apt names, repositories, signing keys (built into the binary), GitHub tarball SHA-256, units and data directories come from the catalog. The helper runs a job in the transient unit `loft-panel-job` (`systemd-run`): one at a time, outside the helper's sandbox, surviving restarts of the panel and the helper. `job.json` and `job.log` are in `/var/lib/loft-panel-adm`.
- Updates install only the release the last check found (no older tag from the page). Downloads must match the release's `SHA256SUMS`; a tarball install keeps the old binaries and puts them back, a .deb install reinstalls the running version's .deb files, when node or panel are not active afterwards. Default mode: check and notify.
- The first owner needs the setup code from `/var/lib/loft-panel/setup.code`, so the first visitor cannot claim the panel.
- Sessions use an HttpOnly, SameSite=Strict cookie (Secure behind HTTPS) plus a CSRF token header. Unknown `Host` names get 421.
- Failed logins block the client address (5 in 10 min → 24 h). The node passes it to nginx with `decoy_proxy enable`. Without it every client looks like 127.0.0.1, and failures only slow down that login (up to 30 s) instead of locking everyone out.

## Needs in the node

| Gap | Blocks |
|-----|--------|
| Status API (leases, sessions, relays, traffic) | online LEDs, last seen, counters |
| Several networks per node | second network in Segments |

# Loft · macOS client

**English** · [Русский](DESIGN_ru.md) · [中文](DESIGN_zh.md)

Menu-bar agent. The window is secondary. Mark, palette, and Avenir Next: [`brand/`](../../../docs/brand/). Interactive mock: [`index.html`](index.html). Protocol: [docs/protocol.md](../../../../docs/protocol.md).

The Swift 5 + AppKit binary lives in [`../Sources/`](../Sources/). A live session is the Go process `loft-front` copied into `loft.app/Contents/MacOS/`. In-process `GETFront.swift` is not the path the app uses. Hello of `loft-front` is the Go client (GET, WS, H2, QUIC, H3, protocol 1.0). SSTP is a separate PPP-over-HTTPS VPN in the same GUI. VLESS is a third type (TCP+TLS, REALITY, Vision, XHTTP). The VLESS editor is Link (`vless://`) or Manual (fields); both store the DuckSoft URI. Trojan/Shadowsocks share that family later.

---

## 1. Product

The client speaks protocol 1.0. Locally it exposes SOCKS5 and TUN (already present in the Go CLI). Credentials are a member key `lt_…`. There is no password.

v1 shipped: key, outbound `get` / `ws` / `h2` / `quic` / `h3` via `loft-front`, several **connections** (profiles), import/export `.loft`, profile edit, connect/disconnect, TUN and hold. `select: auto` inside a profile remains in the Go CLI.

A connection is one named `client.yaml`. Several connections are several sessions. They are distinct from cap `multi` (several `instance_id` values on one key).

Shared file (all loft apps): a `.loft` file holds **one or several connections**, mixed kinds `loft` / `sstp` / `vless` / `trojan`. The list subtitle is **kind · transport · host**. The loft address is `loft://`. Spec: [docs/share.md](../../../docs/share.md).

---

## 2. Protocol join (1.0)

Software `1.0.22`. Protocol `1.0` (`0x0100` in Hello). SOCKS and TUN stay local. Spec: roster, decoy, mask, **named address lists**, and overlay forwarding are required of a client on a filtered path.

| In the application | On the wire |
|--------------------|-------------|
| Connection (profile, `.loft`) | One `client.yaml` **or** one entry under `connections:`: `kind`, `token` / creds, `outbounds`, `select`, optional `bootstrap` / `controller_pub` / `fronts` / `lists_url` / `use` / `default` / `p2p` |
| Key `lt_` + 48 hex | Auth blob `0x02` + HMAC; the node stores SHA-256 |
| URL + type + path | One outbound: `type` `get`\|`ws`\|`h2`\|`quic`\|`h3`, `url`, `path` |
| FRONT card in the status pane | Live outbound after Hello/Status `caps` intersection |
| Overlay IP | Status `ip` (and `mtu`, overlay `gateway`) |
| `select: auto` | Remaining fronts → next outbound → Status `replicas` → roster |
| Several outbounds | Several profiles = several sessions. Distinct from cap `multi` |
| Cap `multi` | One key, several `instance_id`. Node: `auth_already_online` / `auth_limit` |
| — | Persist `instance_id` per profile; empty → node mints, store it |
| — | Fresh `0x02` on reconnect inside `lease_hold` |
| About: fronts / mask | Caps **this client** puts in Hello |
| SNI decoy | The client does not choose. Status / roster / probe. CDN front keeps the CDN hostname |
| Roster | GET `{roster_path}` before auth (`bootstrap`); after, Status `roster`. No key in the file |
| Split routing | Product is **zones**. A zone is through Loft or around Loft; a service only belongs to a zone. Kernel still two CIDR bags (`via` / `direct`) derived from `zone.def`. Controller named lists (`use:`) are not in this UI yet. |
| Keenetic `.bat` / local add | Extra local services in a zone, not on the wire. Catalog files in loft-routes are lists, not zones. `source: me` / `repo`. |
| TUN UDP | Cap `dgram`, mux `CmdDgram` |
| Overlay to another member | Status `peers` hub\|mesh\|vlan. Cap `p2p` optional; `p2p: false` = hub splice only |

Hello of the live helper (`loft-front`, member, not a relay): fronts the profile enables (`get` / `ws` / `h2` / `quic` / `h3`), plus `status timers hello_caps reset dgram clockless ticket decoy mask roster resume lists`. Cap `p2p` only if the profile allows a listen. Cap `relay` is off. `hub:*` and `ctl:1` are never opened.

After the first Status, only the intersection of fronts, in this client’s order. A non-OK Status forbids mux. GET 404 on auth is `auth_bad_key`.

Lists: Status `lists_serial` / `lists_hash` → `GET {lists_path}` (default `/static/v3/lists.json`), same Ed25519 key as the roster. SOCKS matches hostname; TUN matches CIDR. Several hits: **drop, then direct, then via**.

Hold, failover, Packet Tunnel vs helper, “reset routes” are machine policy on top of that mapping. They are not caps.

**AppKit split matches the mock (zones).** Still behind the wire: Manual screen is GET\|WS only; no `bootstrap` / `controller_pub`; no fetch of named controller lists; no `drop`; no live-front change after failover. Extra CIDR apply into the system table is `viaSets` / `bypassSets` through loft-priv (`--apply-split`). A complete `.loft` already carries the YAML fields.

---

## 3. Profiles

A connection is a named profile: kind (`loft` / `sstp` / `vless` / `trojan`) plus credentials plus outbounds. The row subtitle is kind · transport · host (`loft · get · cdn.example.com`). Context menu on a row: Edit / Export `.loft` (the selected connection) / Delete. Export all connections as `Family.loft`.

Two modes (Settings → “Several outbounds”, the same toggle on the list):

| Mode | Behaviour |
|------|-----------|
| One active (default) | Switching profile tears the session and handshakes again. The list marks “active”. Popover: one Connect/Disconnect button. |
| Several outbounds | Each profile has its own Connect/Disconnect and status. Opening one leaves the others. The lamp in the menu is on if any session is live. SOCKS: shared `:1080` or a port per profile. |
| Simple view | Main screens: name and Connect/Disconnect. IP, front, SOCKS, TUN, spark: only when simple view is off. Settings stay complete. |

`select: auto` chooses an outbound **inside** the YAML profile.

| Where | File |
|-------|------|
| Profiles | `~/Library/Application Support/loft/profiles/<id>.yaml` |
| Pointer | `~/Library/Application Support/loft/active` (one-active mode only) |
| Sessions | `~/Library/Application Support/loft/sessions/<id>` (which are up in multi) |
| Prefs | `~/Library/Application Support/loft/prefs.yaml` (`multi: true`, `split: true`) |
| Split lists | `~/Library/Application Support/loft/routes.txt` (zones, then services) |
| Key | Keychain. The UI never shows the key as a standing plaintext field after save. |

Import: `.loft` or `client.yaml`. A `.loft` MAY be today’s single document **or** a bundle with `connections:` (one or several, mixed protocols). Each entry becomes a profile. Export the selected connection as `Office.loft`; export several as `Family.loft`. The file contains keys; the UI warns against giving it away.

The loft address field is `loft://<token>@host…`. One front → QR `loft://`. Several fronts of one connection → that connection’s `.loft`.

Edit: name, QR, kind, then the type-specific block. Tabs **Connection / Advanced**. loft and VLESS share a quiet capsule **Link / Manual**. loft: paste `loft://`, or key / host / port / GET·WS·H2·QUIC·H3 / path. Both write the same profile. VLESS: paste `vless://`, or address / port / UUID / TLS·REALITY / TCP·XHTTP and the fields that mode needs. Both write `vless://`. SOCKS5, TUN, DNS sit on Advanced. This screen has no session status and no Connect/Disconnect.

Import and export of connections use the system `NSOpenPanel` / `NSSavePanel`. The mock keeps a drop zone and a YAML preview so the file shape is visible.

---

## 4. Route / TUN

The machine has one default route. SOCKS MAY exist per profile. TUN does not.

| What | Where | Why |
|------|-------|-----|
| All traffic | on the **connection** | This session is a candidate for the default route. A SOCKS-only route is never taken. At most one live TUN owns the route. |
| Hand to a live one | **client** | The owner of the route died: the route moves to another open TUN. Three sessions, two dead → route on the last live one. |
| Keep direct Internet closed | **client** | Last TUN died: the native route stays down. The machine has no path, and does not call out on its own IP. |
| Split routing | **client** | Toggle. On: lists in the table. Off: loft installs none of those routes. |
| Shared SOCKS | **client** | One entry `:1080` for every live outbound. If split is on, the same zone-derived through/around bags apply. Off: a port per profile. |
| Simple view | **client** | Display. Popover, status, list, edit: name and Connect/Disconnect. Settings, split, import stay visible. |
| Reset routes | action | The user restores direct Internet. Shown in the popover and on the status window when hold is armed. |

Hold and failover are machine policy. Placing them on connections makes policies fight.

Behaviour:

1. Live TUN sessions: the route, advertised SOCKS, and popover sit on the last connected (freshest). The Connections list is sorted by that freshness.
2. That session dies, failover on, another live session remains → popover, SOCKS, and route jump to the previous live one by freshness.
3. No live TUN, hold on → the network is closed, lamp red, “route closed”. The popover and the status window show “Reset routes”.
4. Hold off → on the last break the native route returns (the real IP is reachable).

Copy uses *route*, *direct Internet*, *reset*. It does not use “kill switch” or “shield”.

### 4.1 Backends

One client, two engines. Choice by OS version, not a setting.

| macOS | Engine | Why |
|-------|--------|-----|
| 10.13–10.14 | privileged helper (`SMJobBless`) | Packet Tunnel with Developer ID is unavailable: appex is App Store only. |
| 10.15+ | Packet Tunnel, **system extension** | From Catalina, Apple allows Network Extension outside the store only as sysex. |

The SOCKS helper does not install. The engine starts on the first “all traffic”.

On 10.15+ the helper is not blessed, even if sysex was refused: TUN stays off, SOCKS lives. Two ways to edit the table on one machine are forbidden.

Route policy is one object (`RouteEngine`). Only the path into the system changes.

**Helper (10.13–10.14).** The application opens `utun` and runs the stack. The helper, over XPC: `ifconfig`, `route add/delete`, snapshot of the gateway, hold, reset. It does not read traffic. One admin dialog on first install.

**Packet Tunnel (10.15+).** System extension, entitlement `packet-tunnel-provider-systemextension`. First TUN: activate sysex and save the configuration. In System Settings this is a network profile; in loft UI it is “route”, profile name `loft`.

How policy maps onto `NEPacketTunnelNetworkSettings`:

| State | includedRoutes | excludedRoutes | provider |
|-------|----------------|----------------|----------|
| loft live, all traffic | default | bypass (+ front host) | started |
| loft live, split | through-loft bag (zones with `def` true) | around-loft bag (`def` false) + front host | started |
| Hold, loft dead | default (packets into utun are **dropped**) | bypass | **keep running** |
| Reset / hold off on break | — | — | stop |

Hold and “past Loft while Loft is closed” on Network Extension work only while the provider is running. Stopping it restores the native default.

App Store, if it appears: the same Packet Tunnel (appex or sysex per store rules). The helper is omitted from MAS.

---

## 5. Split routing

A client-wide mapping, not a per-profile list. Otherwise three TUN sessions argue over prefixes.

**Product.** A **zone** is a policy: through Loft or around Loft, plus an optional list URL. A **service** (name + CIDRs) only belongs to a zone. Direction is not a field on the row. Built-in zones are Local (around Loft, empty repo, seed LAN) and Global (through Loft, `global.bat`). Extra zones come from the zone editor. Files `global.bat`, `ru.bat`, `cn.bat` from [a3app/loft-routes](https://github.com/a3app/loft-routes) are catalogs, not zones: the user attaches a file to a zone’s repository.

**Kernel.** The system table has two CIDR bags, derived from `zone.def`: through Loft (`viaSets`) into utun, around Loft (`bypassSets`) onto the remembered native hop. loft-priv diffs them (`--apply-split` / `--clear-split`). Through-loft CIDRs are skipped while 0/1 already owns the default.

**Protocol lists** (named sets from the controller, `use:` → via / direct / drop) stay a later revision. They do not appear as tabs. A later revision MAY import a controller list into a zone without wiping `source: me`.

**Toggle.** Zones are configuration. They enter the table only while split is on. Off: extra routes are removed; the machine behaves as without split.

**Direct next-hop.** The same snapshot TUN already takes before claiming `0.0.0.0/0` (hold/restore need a place to return Internet). Split does not take a second snapshot. Around Loft installs narrower routes on that next-hop.

Around Loft works **while Loft is closed**: hold shut the default; LAN and regional portals still go to the remembered gateway. Through Loft without a live TUN is removed (there is nowhere to send).

| Bag | Split on, loft live | Split on, loft closed | Split off |
|-----|---------------------|-----------------------|-----------|
| **Through Loft** (`zone.def` true) | into utun | removed | removed |
| **Around Loft** (`zone.def` false) | remembered native | remembered native | removed |
| Default | TUN / hold | hold or native | as before |

Snapshot: when split turns on, if default is still native, remember it now. If loft already holds default, take the snapshot from the last open. No snapshot: “remember when loft takes default”.

**Shared SOCKS.** One entry `:1080`. If split is on, the same zone-derived bags. If off, all SOCKS into the live loft (route owner or first live). Shared off: a port per profile; the application chooses the outbound.

### 5.1 File format

Keenetic loads Windows `.bat` `route ADD`. From KeeneticOS 5.0 a comment at end of line: `:: rem` or `& rem`. loft reads both, writes `:: rem`.

**Keenetic (standard)** — loft imports as-is:

```
route ADD 142.250.0.0 MASK 255.255.0.0 0.0.0.0
route ADD 1.1.1.0 MASK 255.255.255.0 0.0.0.0 :: rem Cloudflare
```

The gateway is ignored (as Keenetic does when binding to an interface). `route add` is case-insensitive. Without a comment the service name is empty. The whole file goes into one zone: import asks which zone.

**loft** — the same `.bat`. Keenetic accepts it (REM is text to it):

```
@echo off
REM loft-routes 1.0
REM zone: global
route ADD 142.250.0.0 MASK 255.255.0.0 0.0.0.0 :: rem YouTube
REM zone: local
route ADD 10.0.0.0 MASK 255.0.0.0 0.0.0.0 :: rem LAN
```

`REM zone:` names the zone for the following `route ADD` lines until the next `REM zone:`. The service name is `:: rem` only. Direction is not written on the service line.

Legacy import still accepts `| via` / `| bypass` (`loft` / `direct` are synonyms) after the name and maps them to Global / Local. New files do not write that.

Export:

| Target | File | Contents |
|--------|------|----------|
| Keenetic | `YouTube.bat` | one zone, `:: rem YouTube`, no `REM zone:` |
| loft | `loft-routes.bat` | all zones, `REM zone: local` / `global` |

Import: `.bat`, `.txt`, `.loft-routes`. A legacy mixed file splits `| via` into Global and `| bypass` into Local. A plain Keenetic file asks which zone. A file with `REM zone:` fills that zone.

These files are local route tables. They are distinct from controller address lists (`use:` in `client.yaml`), which the protocol publishes. A later revision MAY import a controller list into the same table.

### 5.2 Repository

Each zone has its own repository: a URL of a raw `.bat` (GitHub and similar). Optional. Auto-update once a day for that zone. Lines from the repo are marked `source: repo` on that zone. An update leaves `source: me` rows in place. A zone with an empty URL has no list.

The URL field is short, with a **Lists** button on the right. That opens the catalog in [a3app/loft-routes](https://github.com/a3app/loft-routes): `catalog.json`. Picking a row fills the URL. A custom URL still pastes by hand.

```
https://raw.githubusercontent.com/a3app/loft-routes/main/catalog.json
https://raw.githubusercontent.com/a3app/loft-routes/main/global.bat
https://raw.githubusercontent.com/a3app/loft-routes/main/ru.bat
https://raw.githubusercontent.com/a3app/loft-routes/main/cn.bat
https://raw.githubusercontent.com/a3app/loft-routes/main/ir.bat
https://raw.githubusercontent.com/a3app/loft-routes/main/kz.bat
https://raw.githubusercontent.com/a3app/loft-routes/main/by.bat
```

### 5.3 Catalog

The lists window is one catalog of named services, grouped by **zone**, not by direction. Built-in zones are Local and Global. Extra zones come only from the zone editor. Regional files do not create zones.

| Code | Meaning | Direction |
|------|---------|-----------|
| **Local** | LAN and regional lists (ru, cn, ir, kz, by) | around Loft |
| **Global** | world services (YouTube, ChatGPT, …) | through Loft |

A route belongs to a zone. Direction lives only on the zone: through Loft or around Loft. Every service in the zone follows that. Flip the zone, the whole list moves. A one-off exception is another zone (or move the service).

Each zone has its own repository URL. Local ships with one service, LAN (`10.0.0.0/8` `172.16.0.0/12` `192.168.0.0/16`). A Russia user picks `ru.bat` for Local; a China user picks `cn.bat`. Built-in zones cannot be renamed or deleted; direction and repository can change. Deleting a user zone moves its services to Global.

A loft-routes catalog file is one list. The zone it fills is the zone whose repository URL points at it.

### 5.4 Windows

Three doors to the catalog: Settings → Route → **Lists**; status window footer **Lists**; menu Tools → **Routes**. Tools also has a **Split routing** check that installs the lists, same toggle as Settings.

Settings Route: a real toggle (install the lists) and a gold **Lists** ghost. The row is not a click-through.

Catalog window, 440 × ~600:

1. Title + the same split toggle. Caption of the remembered direct hop.
2. Zone tabs (Local, Global, then any added), each with a service count. **Zones** opens the zone editor.
3. Mute policy line: `Local · around Loft`. Direction is not a control on this screen.
4. Search field, filters the open zone by service name.
5. Rows: name, first CIDRs, count, source (repo / mine / ignore). Click the row to edit name, networks, zone. No through/around on the row.
6. Plus: add a service or an ignore (network, name, zone, kind Route / Ignore). An ignore CIDR stays out of the kernel table and is subtracted from imported rows of the same zone on pull. Saving a repo row after deleting CIDRs promotes those CIDRs to the zone ignore set.
7. Footer: Import / Export / Repository. Repository edits the **open zone’s** list URL (field + Lists picker), auto-update, and pull.

Zone editor: rows are name, direction, service count, built-in mark, list file if a repo is set. No extra caption — the name is the name. Plus adds a zone (name, direction, optional URL). Click a row to edit. Built-in: name locked, direction and repository editable. User zone: rename, direction, repository, delete (services → Global). Direction (Through Loft / Around Loft) lives only here and on add-zone.

Import of a legacy mixed loft-routes file puts `| via` in Global and `| bypass` in Local. A repo URL fills its zone; `| via` / `| bypass` in that file are ignored. A file with `REM zone:` fills that zone. `REM ignore 104.16.0.0/12` (and a service named `ignore`) lands in the zone ignore set, not as a route.

Empty zone: mute line, no fake rows.

### 5.5 Store

`~/Library/Application Support/loft/routes.txt`. Zone blocks first, then services. No `via:` on a set.

```
zone: local
name: Local
builtin: true
def: false
repo:
auto: true

zone: global
name: Global
builtin: true
def: true
repo: https://raw.githubusercontent.com/a3app/loft-routes/main/global.bat
auto: true

set: lan
name: LAN
zone: local
src: me
cidr: 10.0.0.0/8

set: ignore-local
name: ignore
zone: local
src: ignore
cidr: 104.16.0.0/12
```

`def: true` is through Loft. `src: ignore` is a CIDR that is never installed; a repo pull of the same zone does not put it back. A legacy file with `via:` on sets maps false→Local, true→Global. Top-level `repo_url` lands on Global if `repo_via`, else Local, only when that zone’s repo is empty.

---

## 6. Information architecture

```
NSStatusItem (L + lamp window)
  └ popover 300 × auto  (always one session)
       ├ title: Connected  0:12  |  No active connections
       ├ subtitle: lamp-dot + profile name
       ├ Connect / Disconnect  |  Retry on error
       ├ Reset routes  (when hold is armed)
       ├ Connection ›  (NSMenu of profiles)
       ├ External IP  (pref on by default)
       ├ Overlay IP + TUN badge, Route, SOCKS5
       ├ footer: Open Loft  |  Settings  |  Quit

Main / Status        — always mark + pill + action + OVERLAY / FRONT / SOCKS5
                       cards; footer Journal / Reset / Lists / Settings
Main / Connections   — list, action pill on the left, Several toggle, “+”;
                       footer Import / Export / Settings
Journal              — 520×480 log, Copy / Clear / Back
Edit                 — name, QR, kind, Connection / Advanced; no session chrome
Onboarding           — key → address, or import a file
Settings             — General: login, Dock, simple, rate, language, updates, diag
                       Route: default route, failover, hold, split + Lists
                       Client: auto, reconnect, several, shared SOCKS, port,
                       external IP, echo URL
                       footer Defaults / Journal / About
Split                — catalog tabs Local / Global (+ extras), mute `Local · around Loft`,
                       search, service rows without via/bypass, ignore entries, zone editor, Keenetic+loft import
About                — client version, caption **protocols**, three spec rows
                       (loft/1.0, SSTP, VLESS), blurb, colophon A³. Idle pill **Check for
                       updates** until the user taps it; then checking / current
                       / Update to N / download / install / relaunch. Launch
                       check (Settings) is a separate offer: Update / Later /
                       don’t ask again (turns the launch pref off).
```

The application is an `LSUIElement`.

When a Loft window is key, the menu bar is **Loft** | **Connections** | **Tools** | **Edit**. The Loft menu: About Loft, Open Loft, Language, Hide, Quit. Open Loft has a window icon. Language has a globe icon. English is **Language**; every other pack is `{native} (Language)` so the item stays findable. The submenu lists native names with a checkmark on the current pack. Tools: Settings, Split routing, Routes, Journal — gear, check (in the icon column, no extra indent), branch, and document icons. Connections is a top-level sibling: each profile by name → Connect / Disconnect, Tunnel, Default route, Edit — the same actions as the popover switcher. Simple view hides Tunnel and Default route. The application is **Loft**; the protocol is **loft** / **LOFT**.

---

## 7. Menu bar

The icon is an L as a template (the system tints the body). The window is a separate layer, not a template: it is the lamp.

| State | Body | Window |
|-------|------|--------|
| disconnected | template | dark, unlit |
| connecting | template | lamp, pulse |
| connected | template | lamp `#E8B84A`, slight glow |
| error | template | red `#E24B4B`, glow |

In multi: yellow if any session is live; red if any session is in error (red wins). Closing all: the window goes dark.

Implementation: `NSStatusItem` with a custom view or a composite image (template body + coloured rect of the window). The whole glyph is never filled copper.

### 7.1 Popover

Always one session. Title is **Connected** plus uptime, or **No active connections**. Subtitle is a lamp-dot and the profile name. The action is Connect / Disconnect / Cancel / Retry. Rows: Connection, optional External IP, Overlay (TUN badge when the tunnel is up), Route, SOCKS5. Footer: Open Loft | Settings | Quit. Reset routes appears when hold is armed.

Connection opens an `NSMenu` of profiles (check on the current one, status open/closed/failed). It is not a second popover list.

The popover follows system appearance. Brand accent is the lamp (yellow / red). The popover itself is not filled with brand colour.

---

## 8. First run

Two steps. Without a key and an address, Connect is unavailable.

1. **Access key.** “Enter the access key you were given.” Field `lt_…`. Validation: prefix `lt_` + 48 hex. Hint: it starts with `lt_`; it is shown once.
2. **Address.** “Enter the address you were given with the key.” URL, type GET | WS | H2 | QUIC | H3, optional path. “Connect” → handshake, then the popover.
3. **Or import a file** from the key screen.

This screen does not mention peer, SHA-256, or revocation.

---

## 9. Main window

Dark ink `#1A1714`, snow text, lamp only on connected. System Settings is not copied.

Always one session, including when Several is on:

- Header: mark + `loft` + pill `Connected: name 0:12` / `Not connected` (the pill opens Connections)
- Large Connect / Disconnect (lamp fill when on)
- Three cards: OVERLAY · FRONT · SOCKS5
- Sparkline up/down, no gallery of countries and no outbound list
- Footer: Journal / Reset routes / Lists / Settings

**Simple view.** Toggle in Settings. Popover, status, list, edit: name and Connect/Disconnect (plus changing connection from the menu). No overlay IP, front, SOCKS, spark, TUN badge, GET/WS, or path. List without the “Several” toggle. Settings, split, import/export stay as they are.

---

## 10. Copy

Canonical locale is English. Copy lives in [`lang/`](../lang/) — one `{code}.lang` pack per language, same files for the app and this mock (`?lang=zh`, `en`, `fr`, `de`, `it`, `ja`, `ko`, `pt`, `ru`, `vi`). Screenshots under `shots/` include Russian and English. The picker lists English first, then Latin and Cyrillic by English name, then ideographic languages (Chinese, Japanese, Korean). The app ships English, French, German, Italian, Portuguese, Russian, Vietnamese, Chinese, Japanese, and Korean. The Loft menu Language item is **Language** in English and `{native} (Language)` in every other pack.

| id | English |
|----|---------|
| pop_connect | Connect |
| connect | Connect |
| disconnect | Disconnect |
| connecting | Connecting… |
| key.title | Access key |
| key.body | Enter the access key you were given. |
| key.hint | Starts with lt_. It is shown once — keep it. |
| url.title | Address |
| url.body | Enter the address you were given with the key. |
| url.get / url.ws / url.h2 / url.quic / url.h3 | GET / WS / H2 / QUIC / H3 |
| front | Front |
| socks | SOCKS5 |
| launch | Open at login |
| auto | Connect automatically |
| quit | Quit |
| menu_quit | Quit Loft |
| open | Open Loft |
| menu_about | About Loft |
| menu_tools | Tools |
| menu_lang | Language |
| conns.sub | Each has its own key and address. One is active. |
| conns.sub.multi | Each opens on its own. |
| several | Several |
| conn.up / conn.down | open / closed |
| conn.err / conn.fail | error / failed |
| conn.retry | Retry |
| edit | Edit |
| edit.tun | All traffic |
| multi | Several outbounds |
| simple | Simple view |
| socks_share | Shared SOCKS |
| socks_port | SOCKS5 port (Settings; a connection may set its own) |
| failover | Hand to a live one |
| hold | Keep direct Internet closed |
| split | Split routing |
| split.d | A zone goes through Loft or around Loft. A service only belongs to a zone. |
| split.via | Through Loft |
| split.bypass | Around Loft |
| split.zone | Zone |
| split.zones | Zones (editor) |
| split.rdef | Direction of the zone (editor only) |
| split.repo.pick | Lists (button next to the URL) |
| split.lists | List catalog |
| split.lists.body | From the loft-routes repository. You can still paste any URL. |

| route.reset | Reset routes (Settings footer) |
| route.held | route closed |
| pop_connect / disconnect | Connect / Disconnect |
| about.ver | 1.0.22 |
| about.proto | protocols (caption; spec rows sit tight under it) |
| about.loft | loft/1.0 · get · ws · h2 · quic · h3 · mask |
| about.sstp | SSTP · ppp · mschapv2 · pap |
| about.vless | VLESS · tcp · tls |
| about.blurb | From the street, ordinary HTTPS. Inside, a private network. |
| about.sig | A3 |
| about.upd | One pill, vertically centered between the blurb and the colophon. Idle until tap: Check for updates. Then checking / current / Update to N / download % / install. No GitHub on open. No status line above it. |
| pref.upd | Check for updates at launch |
| upd.offer.title | loft update |
| upd.later | Later |
| upd.quiet | Don’t ask again (turns the launch check off) |

Vocabulary on these surfaces: overlay, loft, front, key. About is two lines and the signature A3. It does not discuss the construction of the mark. It does not say “VPN”, “protected”, or “shield”.

---

## 11. Chrome

| Surface | Size | Notes |
|---------|------|-------|
| Popover | 300 × hug | `NSPopover.behavior = .transient` |
| Main | 400 × 560 | hidden titlebar, traffic lights. Simple view — hug. |
| Onboarding | 440 × 520 | centered, not resizable |
| Edit | 440 × hug | name, type, tabs; overlay-scrolls fields if taller than the screen. Save stays pinned. VLESS TLS/REALITY and TCP/XHTTP share one row. |
| Settings | 480 × hug | sheet on main |
| About | 360 × 448 | version, protocols caption + spec rows tight under it, blurb, GitHub update, A³ |
| Offer | 360 × hug | launch found a newer DMG: Update / Later / don’t ask again |

Windows and popover **hug** their content. The client has no permanent scroll gutter. If a list outgrows the window: system overlay (`NSScrollView`, appears on gesture).

UI type: Avenir Next Medium 13 / Demi 15 for headings / Regular 13 for secondary. Mono for key and IP: SF Mono 12.

---

## 12. v1 out

Network map, country picker, account/password, Windows tray in this tree. Windows is a separate `clients/windows`. Split-tunnel as a product name is out; split routing as specified in §5 is in.

# loft · macOS client

**English** · [Русский](DESIGN_ru.md)

Menu-bar agent. The window is secondary. Mark, palette, and Avenir Next: [`brand/`](../../../brand/). Interactive mock: [`index.html`](index.html). Protocol: [docs/protocol.md](../../../../docs/protocol.md).

The Swift 5 + AppKit binary lives in [`../Sources/`](../Sources/). A live session is the Go process `loft-front` copied into `loft.app/Contents/MacOS/`. In-process `GETFront.swift` is not the path the app uses. Hello of `loft-front` is the Go client (GET, WS, H2, QUIC, H3, protocol 1.0). SSTP is a separate PPP-over-HTTPS VPN in the same GUI. VLESS is a third type (TCP+TLS, REALITY, Vision, XHTTP). The VLESS editor is Link (`vless://`) or Manual (fields); both store the DuckSoft URI. Trojan/Shadowsocks share that family later.

---

## 1. Product

The client speaks protocol 1.0. Locally it exposes SOCKS5 and TUN (already present in the Go CLI). Credentials are a member key `lt_…`. There is no password.

v1 shipped: key, outbound `get` / `ws` / `h2` / `quic` / `h3` via `loft-front`, several **connections** (profiles), import/export `.loft`, profile edit, connect/disconnect, TUN and hold. `select: auto` inside a profile remains in the Go CLI.

A connection is one named `client.yaml`. Several connections are several sessions. They are distinct from cap `multi` (several `instance_id` values on one key).

Shared file (all loft apps): a `.loft` file holds **one or several connections**, mixed kinds `loft` / `sstp` / `vless` / `trojan`. The list subtitle is **kind · transport · host**. The loft address is `loft://`. Spec: [docs/share.md](../../../docs/share.md).

---

## 2. Protocol join (1.0)

Software `1.0.9`. Protocol `1.0` (`0x0100` in Hello). SOCKS and TUN stay local. Spec: roster, decoy, mask, **named address lists**, and overlay forwarding are required of a client on a filtered path.

| In the application | On the wire |
|--------------------|-------------|
| Connection (profile, `.loft`) | One `client.yaml` **or** one entry under `connections:`: `kind`, `token` / creds, `outbounds`, `select`, optional `bootstrap` / `controller_pub` / `fronts` / `lists_url` / `use` / `default` / `p2p` |
| Key `lt_` + 48 hex | Auth blob `0x02` + HMAC; the node stores SHA-256 |
| URL + type + path | One outbound: `type` `get`\|`ws`\|`h2`\|`quic`\|`h3`, `url`, `path` |
| Fronts in the status pane | Live outbound after Hello/Status `caps` intersection |
| Overlay IP | Status `ip` (and `mtu`, overlay `gateway`) |
| `select: auto` | Remaining fronts → next outbound → Status `replicas` → roster |
| Several outbounds | Several profiles = several sessions. Distinct from cap `multi` |
| Cap `multi` | One key, several `instance_id`. Node: `auth_already_online` / `auth_limit` |
| — | Persist `instance_id` per profile; empty → node mints, store it |
| — | Ticket `0x03` on reconnect inside `lease_hold` |
| About: fronts / mask | Caps **this client** puts in Hello |
| SNI decoy | The client does not choose. Status / roster / probe. CDN front keeps the CDN hostname |
| Roster | GET `{roster_path}` before auth (`bootstrap`); after, Status `roster`. No key in the file |
| Split / via / past | Protocol **lists**: named sets from the controller. Client maps names to `via` / `direct` / `drop` (`use:`). Default unmatched: `via` |
| Keenetic `.bat` / local add | Extra local sets, not on the wire. Same three actions. Do not overwrite controller names (`source: controller` vs `me` / `repo`) |
| TUN UDP | Cap `dgram`, mux `CmdDgram` |
| Overlay to another member | Status `peers` hub\|mesh\|vlan. Cap `p2p` optional; `p2p: false` = hub splice only |

Hello of the live helper (`loft-front`, member, not a relay): fronts the profile enables (`get` / `ws` / `h2` / `quic` / `h3`), plus `status timers hello_caps reset dgram clockless ticket decoy mask roster resume lists`. Cap `p2p` only if the profile allows a listen. Cap `relay` is off. `hub:*` and `ctl:1` are never opened.

After the first Status, only the intersection of fronts, in this client’s order. A non-OK Status forbids mux. GET 404 on auth is `auth_bad_key`.

Lists: Status `lists_serial` / `lists_hash` → `GET {lists_path}` (default `/static/v3/lists.json`), same Ed25519 key as the roster. SOCKS matches hostname; TUN matches CIDR. Several hits: **drop, then direct, then via**.

Hold, failover, Packet Tunnel vs helper, “reset routes” are machine policy on top of that mapping. They are not caps.

**Mock still behind the wire:** Manual screen is GET\|WS only; no `bootstrap` / `controller_pub`; no fetch of named lists; split catalog has region + default and no `drop`; no live-front change after failover. A complete `.loft` already carries the YAML fields.

---

## 3. Profiles

A connection is a named profile: kind (`loft` / `sstp` / `vless` / `trojan`) plus credentials plus outbounds. The row subtitle is kind · transport · host (`loft · get · cdn.example.com`). Context menu on a row: Edit / Export `.loft` (the selected connection) / Delete. Export all connections as `Family.loft`.

Two modes (Settings → “Several outbounds”, the same toggle on the list):

| Mode | Behaviour |
|------|-----------|
| One active (default) | Switching profile tears the session and handshakes again. The list marks “active”. Popover: one Open/Close button. |
| Several outbounds | Each profile has its own Open/Close and status. Opening one leaves the others. The lamp in the menu is on if any session is live. SOCKS: shared `:1080` or a port per profile. |
| Simple view | Main screens: open and close. IP, front, SOCKS, TUN, spark: only when simple view is off. Settings stay complete. |

`select: auto` chooses an outbound **inside** the YAML profile.

| Where | File |
|-------|------|
| Profiles | `~/Library/Application Support/loft/profiles/<id>.yaml` |
| Pointer | `~/Library/Application Support/loft/active` (one-active mode only) |
| Sessions | `~/Library/Application Support/loft/sessions/<id>` (which are up in multi) |
| Prefs | `~/Library/Application Support/loft/prefs.yaml` (`multi: true`) |
| Key | Keychain. The UI never shows the key as a standing plaintext field after save. |

Import: `.loft` or `client.yaml`. A `.loft` MAY be today’s single document **or** a bundle with `connections:` (one or several, mixed protocols). Each entry becomes a profile. Export the selected connection as `Office.loft`; export several as `Family.loft`. The file contains keys; the UI warns against giving it away.

The loft address field is `loft://<token>@host…`. One front → QR `loft://`. Several fronts of one connection → that connection’s `.loft`.

Edit: name, kind, then the type-specific block. Tabs **Connection / Advanced**. loft and VLESS share a quiet capsule **Link / Manual** with chain and pencil inside the segments. loft: paste `loft://`, or key / host / port / GET·WS·H2·QUIC·H3 / path. Both write the same profile. VLESS: paste `vless://`, or address / port / UUID / TLS·REALITY / TCP·XHTTP and the fields that mode needs. Both write `vless://`. SOCKS5, TUN, DNS and mux sit on Advanced. In multi this screen also has status and Open/Close for this profile.

---

## 4. Route / TUN

The machine has one default route. SOCKS MAY exist per profile. TUN does not.

| What | Where | Why |
|------|-------|-----|
| All traffic | on the **connection** | This session is a candidate for the default route. A SOCKS-only route is never taken. At most one live TUN owns the route. |
| Hand to a live one | **client** | The owner of the route died: the route moves to another open TUN. Three sessions, two dead → route on the last live one. |
| Keep direct Internet closed | **client** | Last TUN died: the native route stays down. The machine has no path, and does not call out on its own IP. |
| In the menu on break | **client** | Show “Reset routes” in the menu when every connection is down. Off: Settings only. |
| Split routing | **client** | Toggle. On: lists in the table. Off: loft installs none of those routes. |
| Shared SOCKS | **client** | One entry `:1080` for every live outbound. If split is on, the same via/bypass rules apply. Off: a port per profile. |
| Simple view | **client** | Display. Popover, status, list, edit: name and Open/Close. Settings, split, import stay visible. |
| Reset routes | action | The user restores direct Internet. In the popover if “In the menu on break” is on. |

Hold and failover are machine policy. Placing them on connections makes policies fight.

Behaviour:

1. Live TUN sessions: the route, advertised SOCKS, and popover sit on the last connected (freshest). The Connections list is sorted by that freshness.
2. That session dies, failover on, another live session remains → popover, SOCKS, and route jump to the previous live one by freshness.
3. No live TUN, hold on → the network is closed, lamp red, “route closed”. If “In the menu on break” is on, the popover shows “Reset routes”; otherwise Settings only.
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
| loft live, split | via | bypass | started |
| Hold, loft dead | default (packets into utun are **dropped**) | bypass | **keep running** |
| Reset / hold off on break | — | — | stop |

Hold and “past loft while loft is closed” on Network Extension work only while the provider is running. Stopping it restores the native default.

App Store, if it appears: the same Packet Tunnel (appex or sysex per store rules). The helper is omitted from MAS.

---

## 5. Split routing

A client-wide mapping, not a per-profile list. Otherwise three TUN sessions argue over prefixes.

Protocol publishes **named** sets (banks, video, ads). The app maps each name to `via` (through loft), `direct` (past loft, remembered native GW), or `drop`. Unmatched destinations follow `default` (SHOULD be `via`). Split toggle off: do not install extra routes; unmatched = via loft as before.

Local Keenetic import and the git repo are additional sets beside the controller file. Controller rows update with serial; they do not wipe `source: me`.

**Toggle.** Lists are configuration. They enter the table only while split is on. Off: extra routes are removed; the machine behaves as without split.

**Direct next-hop.** The same snapshot TUN already takes before claiming `0.0.0.0/0` (hold/restore need a place to return Internet). Split does not take a second snapshot. “Past loft” installs narrower routes on that next-hop.

Past loft works **while loft is closed**: hold shut the default; banks and portals still go to the remembered gateway. Via loft without a live TUN is removed (there is nowhere to send).

| List | Split on, loft live | Split on, loft closed | Split off |
|------|---------------------|-----------------------|-----------|
| **Via loft** | into utun | removed | removed |
| **Past loft** | remembered native | remembered native | removed |
| Default | TUN / hold | hold or native | as before |

Snapshot: when split turns on, if default is still native, remember it now. If loft already holds default, take the snapshot from the last open. No snapshot: “remember when loft takes default”.

**Shared SOCKS.** One entry `:1080`. If split is on, the same lists (via / past / which loft). If off, all SOCKS into the live loft (route owner or first live). Shared off: a port per profile; the application chooses the outbound.

### 5.1 File format

Keenetic loads Windows `.bat` `route ADD`. From KeeneticOS 5.0 a comment at end of line: `:: rem` or `& rem`. loft reads both, writes `:: rem`.

**Keenetic (standard)** — loft imports as-is:

```
route ADD 142.250.0.0 MASK 255.255.0.0 0.0.0.0
route ADD 1.1.1.0 MASK 255.255.255.0 0.0.0.0 :: rem Cloudflare
```

The gateway is ignored (as Keenetic does when binding to an interface). `route add` is case-insensitive. Without a comment the service name is empty. The whole file goes into one list: import asks “via loft” or “past loft”.

**loft extended** — the same `.bat`. Keenetic accepts it (REM and `| via` in the comment are text to it):

```
@echo off
REM loft-routes 1.0
REM
route ADD 142.250.0.0 MASK 255.255.0.0 0.0.0.0 :: rem YouTube | via
route ADD 77.88.8.8 MASK 255.255.255.255 0.0.0.0 :: rem Yandex DNS | bypass
```

Comment: `service name | via` or `| bypass` (`loft` / `direct` are synonyms). Everything before `|` is the label in the UI. Without `|`: as Keenetic, direction from the header `REM list: via` or from the dialog.

Export:

| Target | File | Contents |
|--------|------|----------|
| Keenetic | `YouTube.bat` | one list, `:: rem YouTube`, no `\| via` |
| loft | `loft-routes.bat` | both lists, `\| via` / `\| bypass` |

Import: `.bat`, `.txt`, `.loft-routes`. A mixed loft file splits itself. A plain Keenetic file opens the “where” dialog.

These files are local route tables. They are distinct from controller address lists (`use:` in `client.yaml`), which the protocol publishes. A later revision MAY import a controller list into the same table.

### 5.2 Repository

A URL of a raw `.bat` (GitHub and similar). Auto-update once a day. Import into the chosen list (usually “via loft”). Lines from the repo are marked `source: repo`. An update leaves manually added CIDRs in place.

Published lists: [`a3app/loft-routes`](https://github.com/a3app/loft-routes) walks trees such as [RockBlack-VPN/ip-address](https://github.com/RockBlack-VPN/ip-address) and emits loft-routes files with `:: rem <service>` on every line. Global is `| via`, RU-RU is `| bypass`. The mixed file is both.

```
https://raw.githubusercontent.com/a3app/loft-routes/main/global.bat
https://raw.githubusercontent.com/a3app/loft-routes/main/ru-ru.bat
https://raw.githubusercontent.com/a3app/loft-routes/main/via.bat
```

### 5.3 Catalog

The lists window is one catalog of named services, grouped by **region** (national origin of the service), not by direction.

| Code | Meaning | Default direction |
|------|---------|-------------------|
| **RU** | Russia | around loft |
| **CH** | China | through loft |
| **Global** | the rest of the world | through loft |

Each region has a default: through loft or around loft. A service without its own mark inherits the region default. A service may override (LAN around loft while Global is through loft). Changing the region default moves every inheriting row; overrides stay.

A later loft-routes file MAY carry `REM region: ru` and `REM list: bypass`. Until then, `a3app/loft-routes` `ru-ru.bat` lands in RU, `global.bat` in Global. CH is an empty catalog until a source exists.

### 5.4 Windows

Settings: a toggle (the row opens the catalog). Catalog window, 440 × ~600:

1. Title + the same split toggle. Caption of the remembered direct hop.
2. Three region tabs: **RU** · **CH** · **Global**, each with a service count.
3. **Default** for the open region — capsule Through loft / Around loft.
4. Search field, filters the open region by service name.
5. Rows: name, first CIDRs, count, source (repo / mine), compact through / around. Gold = inherits the region. Copper = this service overrides. Click the pills to set or to clear back to inherit. Click the row to edit.
6. Plus: add a service (network, name, region, direction: as the region / through / around).
7. Footer: Import / Export / Repository.

Import of a mixed loft-routes file keeps `| via` / `| bypass` as overrides. A file with `REM list:` and `REM region:` fills one region as inherit.

Empty region: mute line, no fake rows.

---

## 6. Information architecture

```
NSStatusItem (L + lamp window)
  └ popover 300 × auto
       exclusive:
         ├ Connect / Disconnect  |  Retry on error
         ├ Reset routes  (all down, hold, “in the menu on break”)
         ├ Connection ›  (switcher)
         ├ overlay IP, front, SOCKS5
       multi:
         ├ header: loft · “open · N” | “error” | “route closed”
         ├ Reset routes  (all down, hold, “in the menu on break”)
         ├ one row per profile:
         │    lamp-dot  name
         │    overlay IP · port   |  “failed”
         │    Open / Close / Retry
         └ header lamp = yellow if any is up, red on error or hold
       ├ Open Loft…
       └ Quit

Main / Connections   — list; bottom Import / Export / Manual; multi toggle
Edit                 — name, loft:// (or SSTP/VLESS fields), SOCKS, tunnel;
                       Advanced tab — mux, DNS
Onboarding           — key → address, or import a file
Settings             — login, auto-connect, check for updates at launch, simple view, multi, shared SOCKS,
                       route (failover / hold / in the menu on break / split / reset)
Split                — catalog RU / CH / Global, region default, Keenetic+loft import, repository
About                — client version, protocol 1.0, fronts and options this
                       client implements, colophon A³. On open, checks
                       GitHub `a3app/loft` latest release; the pill installs
                       the matching macOS DMG and relaunches. Launch check
                       (Settings) opens a small offer: Update / Later / don’t
                       ask again (turns the launch pref off).
```

The application is an `LSUIElement`.

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

### 7.1 Popover · multi

This is a list of live sessions, not a switcher of one active profile.

- Header: mark with the same lamp as the menu; line “open · 2” / “open · 1 · error” / “loft closed”.
- Row: dot (lamp / grey / red), name, below overlay IP and SOCKS port. On error, “failed” in place of the IP.
- Right: one action: Close | Open | Retry.
- IP and port are what a user needs to see that the session is live and where to point an application.

The popover follows system appearance. Brand accent is the lamp (yellow / red). The popover itself is not filled with brand colour.

---

## 8. First run

Two steps. Without a key and an address, Connect is unavailable.

1. **Access key.** “Enter the access key you were given.” Field `lt_…`. Validation: prefix `lt_` + 48 hex. Hint: it starts with `lt_`; it is shown once.
2. **Address.** “Enter the address you were given with the key.” URL, type GET | WebSocket, optional path. “Open loft” → handshake, then the popover.
3. **Or import a file** from the key screen.

This screen does not mention peer, SHA-256, or revocation.

---

## 9. Main window

Dark ink `#1A1714`, snow text, lamp only on connected. System Settings is not copied.

Exclusive:

- Header: mark + `loft` + name of the active profile
- Large Connect (lamp fill when on)
- Row: overlay IP · front name (`cdn-get`) · `127.0.0.1:1080`
- Sparkline up/down, no gallery of countries
- Outbound list: name, type, URL host, dot (live / standby / dead)
- `select: auto` — tries top to bottom; the live one is marked with a lamp

Multi: instead of one Connect, a list of profiles, each with lamp, overlay IP, SOCKS port, and Open/Close. Header shows “N · open”.

**Simple view.** Toggle in Settings. Popover, status, list, edit: name and Open/Close (plus changing connection from the menu). No overlay IP, front, SOCKS, spark, TUN badge, GET/WS, or path. List without the “Several” toggle. Settings, split, import/export stay as they are.

---

## 10. Copy

Canonical locale is English. The interactive mock defaults to English (`?lang=ru`, `de`, `fr`, `pt`, `zh`). Screenshots under `shots/` include Russian and English. The app ships Russian, English, German, French, Portuguese, and Simplified Chinese.

| id | English |
|----|---------|
| connect | Open loft |
| disconnect | Disconnect |
| connecting | Opening… |
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
| quit | Quit loft |
| conns.sub | Each has its own key and address. One is active. |
| conns.sub.multi | Each opens on its own. |
| conns.multi | Several |
| conn.up / conn.down | open / closed |
| conn.err / conn.fail | error / failed |
| conn.retry | Retry |
| edit | Edit |
| edit.tun | All traffic |
| pref.multi | Several outbounds |
| pref.simple | Simple view |
| pref.socks.share | Shared SOCKS |
| pref.socks.port | SOCKS5 port (Settings; a connection may set its own) |
| pref.fail | Hand to a live one |
| pref.hold | Keep direct Internet closed |
| split | Split routing |
| split.d | Past loft uses the direct route from before loft. |
| split.via | Through loft |
| split.bypass | Around loft |
| split.region | RU / CH / Global |
| split.rdef | Default for the open region |
| split.inherit | As the region |
| route.reset | Reset routes (Settings footer) |
| route.held | route closed |
| open.one / close.one | Open / Disconnect |
| about.ver | 1.0.9 |
| about.proto | protocols |
| about.loft | loft/1.0 · get · ws · h2 · quic · h3 · mask |
| about.sstp | SSTP · ppp · mschapv2 · pap |
| about.blurb | From the street, ordinary HTTPS. Inside, a private network. |
| about.sig | A3 |
| about.upd | One pill, vertically centered between protocols and the colophon. The window checks GitHub when it opens. Check / latest / Update to N / download % / install. No status line above it. |
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
| About | 360 × 448 | version, protocols, blurb, GitHub update, A³ |
| Offer | 360 × hug | launch found a newer DMG: Update / Later / don’t ask again |

Windows and popover **hug** their content. The client has no permanent scroll gutter. If a list outgrows the window: system overlay (`NSScrollView`, appears on gesture).

UI type: Avenir Next Medium 13 / Demi 15 for headings / Regular 13 for secondary. Mono for key and IP: SF Mono 12.

---

## 12. v1 out

Network map, country picker, account/password, Windows tray in this tree. Windows is a separate `clients/windows`. Split-tunnel as a product name is out; split routing as specified in §5 is in.

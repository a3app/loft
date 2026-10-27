# loft · Android client

**English** · [Русский](DESIGN_ru.md) · [中文](DESIGN_zh.md)

Phone app plus a `VpnService`. Mark, palette, and Avenir Next: [`brand/`](../../../docs/brand/). Interactive mock: [`index.html`](index.html); every state has its own link, e.g. `index.html#edit-sstp`, `#settings-route`, `#home-off` (the address changes as you click). Protocol: [docs/protocol.md](../../../docs/protocol.md). Import URI: [docs/share.md](../../../docs/share.md).

The Java container lives in [`../app/`](../app/). A live session is Go inside `LoftVpnService`, not `loft-front`. The app implements this mock: Home / Connections / Edit / Scan / Settings.

---

## 1. Product

The inbound is the system VPN (`VpnService`). One profile at a time: loft (`lt_…`, no password), SSTP (user + password), VLESS (`vless://`), or Trojan (`trojan://`).

v1: named connections on disk, import/export `.loft`, Connect/Disconnect, hold (through the Android system switches), split from protocol lists. `select: auto` inside loft YAML. One tunnel at a time.

A `.loft` file holds **one or several connections**. Kinds mix: `loft`, `sstp`, `vless`, `trojan`. A list row shows the host and **kind · transport** (`loft · get`). The loft address string is `loft://`. Spec: [docs/share.md](../../../docs/share.md).

Share: scan a QR or load a file/image with `loft://`, `sstp://`, `vless://`, `trojan://`, or a `.loft`.

Android Settings shows the same VPN row the app starts. The app does not replace Settings.

Language of the adapter is **Java 17**. gomobile already emits Java; this process does not load the Kotlin runtime. The mock is HTML; the later GUI is Views (no Compose).

Android TV / Google TV is the same `VpnService` and AAR with a Leanback flavor. 10-foot mock: [`tv/`](tv/). VIDAA / Tizen / webOS are out of scope.

## 2. Screens

The same rooms as on iOS and macOS, in Material chrome: a bottom navigation bar, a top app bar with “←” or “✕”, a FAB, bottom sheets. System back works everywhere like “←”.

| Screen | What |
|--------|------|
| Home | Mark, `loft`, status pill (tap → list), large Connect / Disconnect. Off: pill “Not connected · Home ›”. Connecting: pill “Connecting: Home…”, a **Cancel** button, spark “handshake”. Cards follow the protocol — loft: **PROTOCOL** loft/1.0 · **FRONT** GET (“Auto” while off) · **OPTIONS** mux · mask; SSTP: **PROTOCOL** · **TRANSPORT** PPP · **AUTH** CHAP; VLESS: **PROTOCOL** · **TRANSPORT** TCP · **SECURITY** REALITY; Trojan: **PROTOCOL** · **TRANSPORT** TCP · **SECURITY** TLS. The speed chart for the last minute sits left, the current ↓ (lamp) and ↑ (stone) speeds are right-aligned to the window edge. Settings → **Speed chart**: **Lines** (default; smooth ↓ curve with a soft lamp fill, thin ↑ line, a dot on the latest value) or **Bars** (two rows of thin rounded bars, older ones fade to the left). Cards and spark are hidden in simplified interface; tapping the cards opens a **Details** sheet (loft network address, front, MTU, server). Below, while connected, rows **External IP**, **Overlay IP** and **Route** (the gateway), as in the macOS popover (not in simplified interface). Navigation bar below. |
| Connections | Title, subtitle “Each has its own key and address. One is active.” Row: compact Connect/Disconnect, name, host, side kind (`loft · get` / `sstp` / `vless` / `trojan`) and **active** on the live one. Tap opens the connection; only Connect switches the tunnel. **Swipe right** edits, **left** deletes (with a dialog), `ItemTouchHelper`. **Long-press** menu: Edit / Export `.loft` (selected only) / Delete. **FAB `+`** opens a sheet: Scan QR · Paste link · From file · Manually. No gesture hint on the screen. |
| Edit | Full-screen dialog: **✕** on top returns to where it was opened. A new connection has the title “New connection”, kind loft / SSTP / VLESS / Trojan and **Scan** in the top bar. An existing one shows its kind as one capsule (it cannot change), a **⋮** menu in the top bar (Share QR, Export `.loft`) and a red **Delete** at the end with a dialog. loft and VLESS: **Link / Manual** (icons: a chain link and a pencil); the link shows the server and hides only the key, Show reveals all of it. loft Manual: access key, host, port, method (**Auto**·GET·WS·H2·QUIC·H3·L3), path. VLESS Manual: UUID, host, port, TLS·REALITY, TCP·XHTTP, TLS hostname, fingerprint and the mode’s fields. SSTP: user, password, server, cert check, PAP/CHAP. Trojan: password, `trojan://` server, cert check. No interface toggle: the tunnel is always `VpnService`. A folding **More options**: mux (loft), DNS, DNS into the system; SSTP and Trojan keep the TLS hostname here, SSTP also “these addresses only”. Save. |
| Onboard | Start: **Scan QR Code** and **Paste link** (a copied link is added at once); “Enter the key and address by hand” leads to two steps: **1 Access key** (`lt_…`) → Continue, **2 Address**: URL; **More options** hides the method (**Auto** first) and the path → Connect. Then the system Android **Connection request** once; the screen says to tap OK. |
| Settings | One list with groups **General / Route / Connection**. Header Reset routes. Links **Defaults \| Log \| About**; Defaults asks first. General: simplified interface, speed units, language, extended log, crash reports (GitHub channel; a redacted issue on the next start). Route: default route, **No Internet without VPN** (not a toggle: on Android it is the system **Always-on VPN** and **Block connections without VPN**, with an **Open VPN settings** link), split routing with a **Lists** button. Connection: **Connect at Launch**, **Reconnect on Disconnect**, public IP. |
| Log | From Settings, **←** on top, the navigation bar stays. Front and session lines, the key shortened. Copy / Share / Clear (with a dialog). |
| About | From Settings, **←** on top. Mark, loft, version, protocols loft/1.0 · SSTP · VLESS · Trojan, blurb, “Made with love by A³”. |
| Scan | **✕** on top. Camera QR. From file (SAF) / From gallery (Photo Picker). Field “Or paste a link”, buttons **Paste** and **Add**. Confirm shows name, kind, transport, host; the secret is hidden. |
| Share QR | **✕** on top. The QR and the `loft://` line, the key warning, **Share** and **Copy link**. |

**Simplified interface**: Home is mark, pill, Connect / Disconnect, navigation bar. Cards and spark hide.

### Split routing

The macOS model (macOS DESIGN §5): a **zone** goes through loft or around it, a **service** (name + CIDRs) only belongs to a zone, an **exclusion** CIDR is never routed and survives list pulls. Built-in zones Local (bypass, LAN) and Global (through, `global.bat`); own zones come from the editor. On Android “through loft” is `VpnService.Builder.addRoute` and “bypass” is `excludeRoute` (Android 13+); on older versions “bypass” is cut out of the tunnel routes. With the tunnel off everything is direct anyway.

**Block zone.** A third built-in zone next to Local and Global, with no list by default. A zone’s direction is one of three: **Through loft / Bypass loft / Block**; any own zone can block too. The zone’s networks are routed into the tunnel and the client refuses them itself at once: TCP with a reset, the rest with ICMP “unreachable”. Apps don’t wait for a timeout. Blocking works only while the tunnel is up, like all split routing. A zone’s exclusions are not blocked. The policy line and the zone list say “Blocked”, with the line “While loft is on, connections to this zone’s networks are refused at once and its domains don’t resolve.”

**Apps around the tunnel.** Settings → Route → **Apps › All through loft**. The Apps screen (**←** on top): a three-way switch — **All** (default, every app follows the routing rules) / **Except** (selected apps go direct, around the tunnel — banks, government services) / **Only** (only selected apps use the tunnel). Except and Only keep separate lists; switching modes loses nothing. Then search, a “Show system apps” switch, “Selected: N” and rows: icon, name, package (monospace), switch; selected ones on top. The settings row shows the mode and the count (“All except selected · 3”). Implemented with `VpnService.Builder.addDisallowedApplication` / `addAllowedApplication`. A hint on the screen: apps around the tunnel don’t see zones, Block or the local DNS, and with the system “Block connections without VPN” Android gives them no network. Android only: on iOS and macOS this needs MDM, ConnMan has nothing like it.

**Domains and local DNS.** Besides networks a service has **Domains** (a domain covers its subdomains; a service may be domains only). The client answers DNS itself — a resolver in the tunnel that the OS gets instead of the node’s DNS: names from bypass zones go to the **local DNS** (the query goes around the tunnel), names from the Block zone (and any blocking zone) get NXDOMAIN at once, everything else goes to the node’s DNS. Settings → Route → **Local DNS**: **System** (the network’s DNS remembered before connecting; default), **Own address** (e.g. `77.88.8.8`) or **DoH** (a URL). Only while the tunnel is up. A service row reads “2 nets · 3 domains”; a domains-only service shows its first domains instead of CIDRs. In the Network editor the **Domains** field follows the networks; an exclusion has none.

**Route profiles.** Zones, services, exclusions and the local DNS together are a profile; one is active. At the top of Lists a row **Profile · RU direct ›** (the operator’s shows “operator” on the right) opens **Route profiles**: profile rows marked by source (“mine”, “from the operator · Home · updated today”, “from a link”) and “✓ active”; a tap makes a profile active. The operator’s profile comes from the node inside the session (protocol §10.3, `lists_inline`) and updates itself, so it is **read-only**: Lists has no add, delete, clear, import or zone editor, the editor fields are locked, and above the zones it says “The operator’s profile is read-only: it updates from the node” with **Make a copy**. The copy becomes the user’s own profile named “RU direct (copy)”, its services are “mine”, and it becomes active at once. **+ Import profile** takes a `loft://routes/…` link or a `.loft` file with a profile; it is added to the list and the user’s own rules are left alone.

Entry: Settings → Route → the toggle and the gold **Lists**. The screens open inside the Settings tab with **←** on top. They are the iOS ones: Lists (toggle, zone tabs with service counts, `Global · Through loft`, search, service rows, **+ Add**, Import · Export · Repository), Network (Route / Exclusion, networks, service, zone), Zones, Zone (name, direction, list address + **Lists**, auto-update; an own zone has Delete zone, services → Global), Repository, Catalog (World, Russia, China, Iran, Kazakhstan, Belarus), Import (from a file via SAF), Export (all zones in one loft-routes file, share sheet).

**Clearing.** Removal takes effect at once. A snackbar at the bottom shows “Deleted: YouTube · **Undo**” for 5 s and puts everything back. There is no confirmation dialog, except for clearing a zone or its exclusions.

- **Swipe left** on a service row reveals a red **Delete**.
- **Touch and hold** a row to start selection. A bar replaces the header: **✕**, “N selected”, **All** (every service in the open zone; tap again to clear), **Delete**. Rows get a check circle; a tap toggles the row.
- On the **Network** screen an existing entry has **Delete Service** at the bottom (**Delete Exclusion** for an exclusion). The screen closes and the snackbar appears.
- **⋮** in the Lists nav bar: **Select** (the same selection mode) and **Clear Zone**. The dialog “Clear Global?” offers **Only Mine (N)** and **Everything, Including the List (M)**. “Everything” unlinks the zone’s list: its address and auto-update are turned off, or the services would come back on the next update. Exclusions stay. A zone with no list services shows one row, **Delete All**.

A list service (source “list”) does not vanish without a trace: its networks move to the zone’s exclusions, or the next update would bring them back (macOS §5 rule). The snackbar says so: “… · list networks moved to exclusions”. Own services are deleted for good.

**Exclusions** have their own screen instead of sitting in the service list. Below the services is a row **Exclusions · N ›**. The screen “Global · Exclusions” (**←** on top): the line “These networks are never routed and don’t come back when the list updates”, CIDR rows (monospace) with the service name on the right; a tap edits it on the Network screen. **Swipe left** reveals **Restore**: the exclusion is deleted and the network returns with the next list update. **+ Add** opens Network with “Exclusion” selected. At the bottom, **Clear Exclusions** with a dialog.

## 3. Android chrome

Do not repaint Settings → Network & internet → VPN. The mock shows how **loft** looks in *their* VPN list: status Connected, the switch is the system tunnel, not a second toggle in our app.

Connect calls `VpnService.prepare` once, then `startForegroundService(LoftVpnService)`. If the user turns loft off in Settings, the app shows off.

VPN permission is the stock **Connection request** dialog once. After that Connect does not ask again.

Camera permission is the stock sheet on the first Scan. The gallery is the system Photo Picker (no broad media access). SAF: images, text, YAML, `.loft`.

Intent filter `loft`: tapping `loft://…` offers import. `.loft` files open through the same detector.

### List gestures

| Gesture | Action |
|---------|--------|
| Tap | Open the connection (Edit). The compact Connect button connects; if another connection was up, it goes down first. |
| Swipe left | Delete, with a confirm dialog. |
| Swipe right | Edit. |
| Long-press | Menu: Edit; Export `.loft` when the row is selected; Delete. |
| Export | Sheet: this connection as `Name.loft`; QR `loft://` of the live front; all connections as `Family.loft`. |

A live session is a **foreground service** (type `systemExempted`, the VpnService case on API 34+). The shade shows the name, kind · transport · overlay IP, Disconnect. On API 33+ there is also the notification permission sheet, once.

**No Internet without VPN** on Android is the system **Always-on VPN** and **Block connections without VPN**. The app opens that Settings page; the user flips the switches.

Quick Settings tile: one tap connects or disconnects the active profile. Lamp lit = online.

## 4. Visual

The same tokens as iOS and macOS: Ink `#1A1714`, Lamp `#E8B84A`, Paper `#E8DFD2`, Avenir Next. The app chrome is loft (dark paper), not lilac Material You; the components are Material: a bottom navigation bar (the active tab is a lamp capsule under the icon), a top app bar with “←” / “✕” and a ⋮ menu, a 56 dp FAB with 16 dp corners, bottom sheets and dialogs.

Type: Demi headings, Regular body, Roboto Mono for keys. Online = the lamp window lit. Off = the lamp dark, a stone L.

System surfaces in the mock (Connection request, Settings → VPN, shade, tile) keep stock Android chrome so the product boundary is visible.

## 5. Copy

Shared strings — buttons, settings, routes — come from `clients/macOS/lang/*.lang` and read the same on macOS, iOS and Android. Android has its own lines only where there is no counterpart: Connection request, the system hold switches, notification, tile, “From gallery”. Copy uses each language’s VPN register (Android Settings Always-on VPN, operator Russian, 分流/始终开启的 VPN in Chinese).

| id | EN | RU |
|----|----|----|
| connect | Connect | Подключить |
| connecting | Connecting… | Подключение… |
| disconnect | Disconnect | Отключить |
| pill_off | Not connected · Home › | Не подключено · Home › |
| key_title | Access Key | Ключ доступа |
| hold | No Internet without VPN | Без VPN — без интернета |
| auto | Connect at Launch | Подключаться при запуске |
| reconnect | Reconnect on Disconnect | Переподключаться при обрыве |
| split | Split Routing | Раздельная маршрутизация |
| scan | Scan QR Code | Сканировать QR-код |
| from_gallery | From gallery | Из галереи |
| open_sys | Open VPN settings | Открыть настройки VPN |
| always | Always-on VPN | VPN всегда включён |
| block | Block connections without VPN | Блокировать подключения без VPN |
| consent | Connection request | Запрос подключения |

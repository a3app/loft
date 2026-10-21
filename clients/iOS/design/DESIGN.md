# loft · iOS client

**English** · [Русский](DESIGN_ru.md) · [中文](DESIGN_zh.md)

Phone app plus a Packet Tunnel. Mark, palette, and Avenir Next: [`brand/`](../../../docs/brand/). Interactive mock: [`index.html`](index.html); every state has its own link, e.g. `index.html#edit-sstp`, `#settings-route`, `#home-off` (the address changes as you click). Protocol: [docs/protocol.md](../../../docs/protocol.md). Import URI: [docs/share.md](../../../docs/share.md).

The SwiftUI container will live in [`../App/`](../App/) later. A live session is Go inside the Network Extension, not `loft-front`.

This mock follows the **shipping macOS GUI** (`clients/macOS`, `L10n.swift`, status / list / edit / settings / onboard / journal / about) on a phone. Copy, fields, and IA match that client. iOS keeps one Packet Tunnel, camera scan, swipe/long-press on the list, and the system VPN row. It does not grow SOCKS inbound, Dock, login item, or several simultaneous tunnels.

---

## 1. Product

The inbound is the system VPN (Packet Tunnel). One profile at a time: loft (`lt_…`, no password), SSTP (user + password), or VLESS (`vless://`).

v1: named connections on disk, import/export `.loft`, Connect / Disconnect, hold, split from protocol lists. `select: auto` inside loft YAML. One tunnel at a time.

A `.loft` file holds **one or several connections**. Kinds mix: `loft`, `sstp`, `vless`. The loft address string in the UI is `loft://`. Spec: [docs/share.md](../../../docs/share.md).

Share: scan a QR or load an image/file with `loft://`, `sstp://`, `vless://`, or a `.loft`.

iOS Settings shows the same VPN row the app starts. The app does not replace Settings.

Apple TV (tvOS 17+) is the same Packet Tunnel after the iPhone GUI. [PLAN](../PLAN.md) phase 6. This mock is the phone.

---

## 2. Screens

Same rooms as the macOS windows, laid out for one column and a thumb.

| Screen | What |
|--------|------|
| Home | Status window. Mark, `loft`, status pill (tap → list), large Connect / Disconnect. Off: pill “Not connected · Home ›”, so it is clear what will open. Connecting: pill “Connecting: Home…”, a **Cancel** button, spark “handshake”. Cards **METHOD / DOWN / UP** and spark “speed, last minute” — two lines, ↓ in lamp and ↑ in stone when simplified interface is off; tapping the cards opens a **Details** sheet (loft network address, method, MTU, server). Tab bar below. |
| Connections | Title, `+`, subtitle “Each has its own key and address. One is active.” Row: compact Connect/Disconnect, name, **host** subtitle, side kind (`loft · get` / `sstp` / `vless`) and **active** on the live one. Tap opens the connection; only Connect switches the tunnel. **Leading swipe** Edit, **trailing swipe** Delete (with a sheet). **Long-press** context: Edit / Export `.loft` (selected only) / Delete. **`+`** opens a sheet: Scan QR · Paste link · From file · Manually. No gesture hint on the screen. Tab bar below. No “Several” toggle. |
| Edit | Modal: **Cancel** on top returns to where it was opened. A new connection has the title “New connection”, the **VPN type** capsule loft / SSTP / VLESS / Trojan and **Scan** in the header. An existing one shows its type as one capsule (it cannot change: the fields differ), **Share** (QR) in the header and a red **Delete** at the end with a confirm sheet. loft and VLESS: capsule **Link / Manual** (icons: a chain link and a pencil). The link shows the server and hides only the key (`loft://lt_••••@cdn…`); Show reveals all of it. loft Manual is access key, host, port, method (**Auto**·GET·WS·H2·QUIC·H3), path. Both write the same profile. VLESS Manual is UUID, host, port, TLS·REALITY, TCP·XHTTP, TLS hostname, fingerprint, and the fields that mode needs. Certificate check. No SOCKS field and no interface toggle: the tunnel is always Packet Tunnel. SSTP: user, password, server, cert check, PAP/CHAP. Trojan: password, `trojan://` server, cert check. A folding **More options**: mux (loft, “all connections share one session with the node”), DNS, DNS into the system; SSTP and Trojan keep the TLS hostname here, SSTP also “these addresses only”. Save. |
| Onboard | Start: **Scan QR** and **Paste link** (a copied link is added at once); “Enter the key and address by hand” leads to the two macOS steps. **1 Access key** (`lt_…`) → Continue. **2 Address**: URL; **More options** hides how to connect (**Auto** first, then GET/WS/H2/QUIC/H3) and the path (a placeholder, not a value) → Connect. Then iOS asks to allow the VPN once; the screen says to tap Allow. |
| Settings | One list with groups **General / Route / Connection**. Header Reset routes. Links **Defaults \| Journal \| About**; Defaults asks first. General: simplified interface, speed units (bits or bytes; the number moves to Mbit/s by itself), language, extended journal. Route: default route, **No internet without the VPN**, split with a **Lists** button (no failover). Connection: **Connect at launch**, **Reconnect if it drops**, **Turn on by itself** (Connect On Demand: other Wi‑Fi and cellular) with a **Trusted Wi‑Fi** field, external IP. Skip Dock, login item, several outbounds, SOCKS. |
| Journal | Pushed from Settings: **‹ Settings** on top, tab bar stays. Front and session lines. The key is shortened. Copy / Share / Clear (with a confirm sheet). |
| About | Pushed from Settings: **‹ Settings** on top. Mark, loft, `1.0.22`, protocols loft/1.0 · SSTP · VLESS · Trojan, blurb, “Made with love by A³”. |
| Scan | Modal: **Cancel** on top. Camera QR. From file / From Photos. Field “Or paste a link” (`loft://…, sstp://…, vless://…`), buttons **Paste** and **Add**. |
| Share QR | Modal: **Close** on top. The QR and the `loft://` line, a warning that the link holds the key, **Share** and **Copy link**. Several fronts or connections are a `.loft` file from export. |
| iOS Settings → VPN | Stock list. loft Connected, type loft. Toggle is the system tunnel. |

**Simplified interface** (Settings → General): Home is mark, pill, Connect / Disconnect, tab bar. Cards and spark hide. List still has Connect/Disconnect and name. Settings stay complete.

macOS-only chrome that this mock does not draw: menu-bar popover, SOCKS5 card and port, Dock icon, login item, several simultaneous outbounds, drag-and-drop plate.

### Split routing

The macOS model (macOS DESIGN §5): a **zone** goes through Loft or around it, a **service** (name + CIDRs) only belongs to a zone, an **ignore** CIDR is never routed and survives list pulls. Built-in zones Local (around, LAN) and Global (through, `global.bat`); own zones come from the editor. On iOS “through Loft” is the Packet Tunnel’s `includedRoutes` and “around” its `excludedRoutes`; with the tunnel off everything is direct anyway. There is no remembered direct hop as on macOS.

Entry: Settings → Route → the toggle and the gold **Lists**. The screens open inside the Settings tab, with **‹ Settings** / **‹ Back** on top.

| Screen | What |
|--------|------|
| Lists | Title and the same toggle, a line (through goes into the tunnel, around goes direct). **Zone** + **Zones** → editor. Zone capsules with the service count. Quiet line `Global · Through Loft`. Search. Rows: name, first CIDRs, network count, source (repo / own / ignore); tap edits. **+ Add**. Bottom **Import · Export · Repository**. |
| Network | Entry Route / Ignore, network or address (one CIDR per line when editing), service, zone. Add / Save. |
| Zones | Rows: name, network count · list file, direction, “Built-in”. **+ Add zone**. |
| Zone | Name (locked on a built-in), direction Through / Around Loft, list address + **Lists**, auto-update, Save; an own zone has Delete zone (services → Global). A new zone has the same fields. |
| Repository | List address of the open zone + **Lists**, auto-update, last pull, Update now. |
| Catalog | loft-routes lists: World, Russia, China, Iran, Kazakhstan, Belarus; the current one is lit. A tap sets the address and goes back. |
| Import | From file (iOS Files), detected format and line count, zone, Add. |
| Export | All zones in one loft-routes file (the zone in `REM zone:`, the service name in a comment), a sample, Save (share sheet). No Keenetic .bat format on iOS. |

---

## 3. iOS chrome

Do not restyle Settings → VPN. The mock shows how **loft** looks in *their* VPN list: status Connected, type loft, toggle is the system tunnel, not a second switch in our app.

Connect calls `NETunnelProviderManager`. If the user disables loft in Settings, the app shows down.

VPN permission is the stock sheet once. After that, Connect does not ask again.

Camera permission is the stock sheet the first time Scan opens (`NSCameraUsageDescription`). Photos uses `PHPicker` (no full library access). Files picker accepts images, text, YAML, `.loft`.

URL scheme `loft` so a tap on `loft://…` offers import.

### List gestures

Mail / Files vocabulary on the phone. The macOS list uses a ghost Edit on the row; iOS adds swipe and long-press because there is no pointer.

| Gesture | Action |
|---------|--------|
| Tap | Open the connection (Edit). Connecting is the compact Connect button; if another connection was up, it goes down first. |
| Compact Connect / Disconnect | Same as the macOS list button (`Connect` / `Disconnect`). |
| Swipe trailing (left) | Delete, with a confirm sheet. |
| Swipe leading (right) | Edit. |
| Long-press | Context menu: Edit; Export `.loft` when this row is selected; Delete. |
| Export on the selected row | Sheet: this connection as `Name.loft`; QR `loft://` of the live front; all connections as `Family.loft`. The sheet repeats the export warning. |

---

## 4. Visual

Same loft tokens as macOS: Ink `#1A1714`, Lamp `#E8B84A`, Paper `#E8DFD2`, Avenir Next. The phone chrome in the mock is loft (dark paper), not iOS grouped gray.

Type: headings Demi, body Regular, keys SF Mono. Connected = lamp window on. Down = lamp off, stone L.

Safe area: status bar + home indicator. One column. Thumb reach: Connect / Disconnect is the large control on Home. The tab bar **Home · Connections · Settings** sits on the home indicator, on Home, Connections, Settings and the screens pushed from it (Journal, About). Edit, Scanner and Share QR are modal without it; Onboarding is full screen.

Scan: dark finder, lamp-colored corner brackets, two actions under the finder (From file / From Photos).

VPN type is a **capsule** (lamp fill on the selected kind), Main/Advanced and Settings tabs are the same segment as macOS.

---

## 5. Copy

Shared strings — buttons, settings, routes — come from `clients/macOS/lang/*.lang` and read the same on macOS and iOS. iOS has its own lines only where macOS has no counterpart: the Home cards and Details sheet, “Turn on by itself” and “Trusted Wi‑Fi”, the onboarding start screen, the “+” sheet. The mock defaults to Russian (`?lang=en` or the RU/EN control). Home, onboarding and the list compact button use one word: **Connect**; while it runs, **Connecting…**. The key is **Access key**, not Member key.

| id | EN | RU |
|----|----|----|
| connect | Connect | Подключить |
| pop_connect | Connect | Подключить |
| disconnect | Disconnect | Отключить |
| connecting | Connecting… | Подключение… |
| pill_on | Connected | Подключено |
| pill_off | Not connected · Home › | Не подключено · Home › |
| journal | Journal | Журнал |
| reset | Reset routes | Сбросить маршруты |
| settings | Settings | Настройки |
| conns | Connections | Подключения |
| conns_sub | Each has its own key and address. One is active. | Каждое — свой ключ и адрес. Активное одно. |
| active | active | активно |
| edit | Edit | Править |
| delete | Delete | Удалить |
| import / export | Import / Export | Импорт / Экспорт |
| warn_export | The file contains the key. Do not give it away. | В файле есть ключ доступа. Не передавайте посторонним. |
| vpn_type | VPN type | Тип VPN |
| loft_uri | loft:// address | Адрес loft:// |
| edit_main / edit_adv | Main / Advanced | Основные / Расширенные |
| key_title | Access key | Ключ доступа |
| url_title | Address | Адрес |
| url_go | Connect | Подключить |
| simple | Simplified interface | Упрощенный интерфейс |
| hold | No internet without the VPN | Без VPN — без интернета |
| auto | Connect at launch | Подключать при запуске |
| reconnect | Reconnect if it drops | Переподключаться при обрыве |
| split | Split routing | Раздельная маршрутизация |
| default_route | Allow default route | Маршрут по умолчанию |
| blurb | From the street — ordinary HTTPS. Inside, a network of its own. | С улицы — обычный HTTPS. Внутри горит своя сеть. |

Status cards: **METHOD** (the front: GET, WS, … or “Auto” while off), **DOWN** and **UP** in the chosen units. The loft network address and MTU are in the Details sheet on tap.

---

## 6. Mapping from the macOS GUI

| macOS | iOS mock |
|-------|----------|
| Status window | Home |
| Pill tap → Connections | same |
| OVERLAY / FRONT / SOCKS5 | METHOD / DOWN / UP; overlay address and MTU in Details |
| Connections + Several toggle | Connections, exclusive only |
| Drop `client.yaml` / `.loft` / QR | Import / Scan / Files / Photos |
| Edit SOCKS5 + tunnel | Tunnel only (Packet Tunnel) |
| Settings login, Dock, SOCKS, several | omitted |
| Settings failover | omitted (one tunnel) |
| About / Journal | same copy and layout, phone column |

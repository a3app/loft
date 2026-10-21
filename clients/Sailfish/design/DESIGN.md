# loft · Sailfish OS client

**English** · [Русский](DESIGN_ru.md) · [中文](DESIGN_zh.md)

Phone app plus a ConnMan overlay row. Mark, palette, and Avenir Next: [`brand/`](../../../docs/brand/). Interactive mocks: [`index.html`](index.html) (Sailfish OS, classic Silica) and [`aurora.html`](aurora.html) (ОС Аврора 5, §7); a segmented control **Sailfish OS | ОС Аврора** in the mock sidebar, like the language switch, goes between them and keeps the screen. Every state has its own link, e.g. `index.html#edit-sstp`, `#settings-route`, `#home-off`, `#home-pulley` (the address changes as you click). Protocol: [docs/protocol.md](../../../docs/protocol.md). Import URI: [docs/share.md](../../../docs/share.md).

The QML container lives in [`../qml/`](../qml/). A live session is `loft-front`, not gomobile. The adapter is **Qt 5 / QML Silica**; the mock is HTML, the GUI is Silica pages. ОС Аврора (Aurora OS) is the OMP package of the same tree: `aurora/` (`app.loft.aurora`); the Sailfish RPM is `sailfish/` (`harbour-loft`).

This mock follows the **shipping macOS GUI** (`clients/macOS`, `L10n.swift`, status / list / edit / settings / onboard / journal / about) on a phone, through the iOS mock it was made from. Copy, fields, and IA match that client. Sailfish keeps one ConnMan tunnel, camera scan, press-and-hold on the list, the cover, and the system VPN row. It does not grow SOCKS inbound, Dock, login item, or several simultaneous tunnels.

---

## 1. Product

The inbound is the system overlay row (ConnMan). One profile at a time: loft (`lt_…`, no password), SSTP (user + password), VLESS (`vless://`), or Trojan (`trojan://`).

v1: named connections on disk, import/export `.loft`, Connect / Disconnect, hold, split from protocol lists. `select: auto` inside loft YAML. One tunnel at a time.

A `.loft` file holds **one or several connections**. Kinds mix: `loft`, `sstp`, `vless`, `trojan`. The loft address string in the UI is `loft://`. Spec: [docs/share.md](../../../docs/share.md).

Share: scan a QR or load an image/file with `loft://`, `sstp://`, `vless://`, `trojan://`, or a `.loft`.

Settings → VPN shows the same ConnMan **Loft** row the plugin starts. The app does not replace Settings.

---

## 2. Screens

Same rooms as the macOS windows, laid out for one column and a thumb, in Silica chrome: no tab bar, a page stack, pulley menus, a dialog, and remorse.

**Navigation.** Home is the root page. Its **pulley menu** (pull down) holds Settings, Connections, Scan QR code; a lit line at the top of a page marks that it has a pulley. Every other page is pushed on the stack: a back indicator **‹** at the top left (and a swipe back) returns one step. Connections and Settings back out to Home. Page titles are a Silica `PageHeader`: right-aligned, in the highlight colour (lamp). Connections, Edit, Settings, Log and Lists also keep their page actions in a pulley; no page has a row of action buttons at the bottom.

| Screen | What |
|--------|------|
| Home | Status window and root page. Mark, `loft`, status pill (tap → list), large Connect / Disconnect. Off: pill “Not connected · Home ›”, so it is clear what will open. Connecting: pill “Connecting: Home…”, a **Cancel** button, spark “handshake”. Cards follow the protocol — loft: **PROTOCOL** loft/1.0 · **FRONT** GET (“Auto” while off) · **OPTIONS** mux · mask; SSTP: **PROTOCOL** · **TRANSPORT** PPP · **AUTH** CHAP; VLESS: **PROTOCOL** · **TRANSPORT** TCP · **SECURITY** REALITY; Trojan: **PROTOCOL** · **TRANSPORT** TCP · **SECURITY** TLS. The speed chart for the last minute sits left, the current ↓ (lamp) and ↑ (stone) speeds are right-aligned to the window edge. Settings → **Speed chart**: **Lines** (default; smooth ↓ curve with a soft lamp fill, thin ↑ line, a dot on the latest value) or **Bars** (two rows of thin rounded bars, older ones fade to the left). Cards and spark are hidden in simplified interface; tapping the cards opens a **Details** sheet (loft network address, front, MTU, server). Below, while connected, rows **External IP**, **Overlay IP** and **Route** (the gateway), as in the macOS popover (not in simplified interface). When **No Internet without VPN** has closed the Internet, a banner reads **The internet is closed until Loft is back**, with a line that the setting did it and **Reset routes** (remorse) (`#home-hold`). Pulley menu: **Settings · Connections · Scan QR code** (`#home-pulley`). |
| Connections | Pushed from the Home pulley, **‹** backs out to Home. Title, subtitle “Each has its own key and address. One is active.” Row: compact Connect/Disconnect, name, **host** subtitle, side kind (`loft · get` / `sstp` / `vless`) and **active** on the live one. Tap opens the connection; only Connect switches the tunnel. No swipe actions. **Press and hold** opens the context menu under its row (Silica ContextMenu) and pushes the rows below down: **Edit / Export / Delete**, centred, in the highlight colour; Export only on the selected row. No floating card, no preview (`#list-ctx`). Adding is the pulley menu: **Scan QR code · Paste link · From file · Manually** (`#list-pulley`); it replaces the iOS “+” and its sheet. No gesture hint on the screen. No “Several” toggle. |
| Edit | A Silica dialog: on top **Cancel** left and **Save** (accept) right; both return to where it was opened. There is no Save button at the bottom. **Save** is greyed out while the form is not valid; a new connection starts empty, so Save starts off. Page actions are in the pulley (`#edit-pulley`): for an existing connection **Delete** (remorse) · **Export** · **Share QR**; for a new one **Scan QR code**. A new connection has the title “New connection” and the **VPN type** capsule loft / SSTP / VLESS / Trojan. An existing one shows its type as one capsule (it cannot change: the fields differ); there is no Share button by the title and no Delete row at the bottom. loft and VLESS: capsule **Link / Manual** (icons: a chain link and a pencil). The link shows the server and hides only the key (`loft://lt_••••@cdn…`); Show reveals all of it. loft Manual is access key, host, port, method (**Auto**·GET·WS·H2·QUIC·H3), path. Both write the same profile. VLESS Manual is UUID, host, port, TLS·REALITY, TCP·XHTTP, TLS hostname, fingerprint, and the fields that mode needs. Certificate check. No SOCKS field and no interface toggle: the tunnel is always the ConnMan row. SSTP: user, password, server, cert check, PAP/CHAP. Trojan: password, `trojan://` server, cert check. A folding **More options**: mux (loft, “all connections share one session with the node”), DNS, DNS into the system; SSTP and Trojan keep the TLS hostname here, SSTP also “these addresses only”. |
| Onboard | Start: **Scan QR Code** and **Paste link** (a copied link is added at once); “Enter the key and address by hand” leads to the two macOS steps. **1 Access key** (`lt_…`) → Continue. **2 Address**: URL; **More options** hides how to connect (**Auto** first, then GET/WS/H2/QUIC/H3) and the path (a placeholder, not a value) → Connect. Then the stock ConnMan consent, if the OS asks; the screen says: “If the system asks to allow the VPN, tap Allow. Only once.” |
| Settings | Pushed from the Home pulley, **‹** backs out to Home. One list with groups **General / Route / Connection**. Pulley: **About · Log · Defaults** (remorse) **· Reset routes** (remorse) (`#settings-pulley`); no button by the title, no row of links at the bottom. General: simplified interface, speed units (bits or bytes; the number moves to Mbit/s by itself), speed chart, language, extended log. Route: default route, **No Internet without VPN**, split routing with a **Lists** button (no failover). Connection: **Connect at Launch**, **Reconnect on Disconnect**, public IP. No Connect On Demand and no Trusted Wi‑Fi. Skip Dock, login item, several outbounds, SOCKS. |
| Log | Pushed from Settings: **‹** on top. Front and session lines (`tun connman-loft up`). The key is shortened. Pulley: **Clear** (remorse) **· Share · Copy** (`#journal-pulley`); no buttons at the bottom, the log takes the page. |
| About | Pushed from Settings: **‹** on top. Mark, loft, `1.0.22`, protocols loft/1.0 · SSTP · VLESS · Trojan, blurb, “Made with love by A³”. |
| Scan | Pushed: **‹** on top. Camera QR. From file / From gallery. Field “Or paste a link” (`loft://…, sstp://…, vless://…`), buttons **Paste** and **Add**. |
| Share QR | Pushed: **‹** on top. The QR and the `loft://` line, a warning that the link holds the key, **Share** and **Copy link**. Several fronts or connections are a `.loft` file from export. |
| Export | Its own page **Export Home**, pushed from the context menu on Connections or from the Edit pulley; **‹** backs out to Connections. The warning that the file holds the access key, then rows **Home.loft** (this connection), **QR loft://** (one front; opens Share QR), **Family.loft** (all three) (`#export`). |
| Cover | The Silica cover on the home screen: lamp mark, the connection name (**Home**), state **Connected**, overlay IP (`10.8.0.14`), and a cover action icon (power) at the bottom instead of a Disconnect button. |
| Settings → VPN | Stock ConnMan list. **Loft** Connected, type loft; OpenVPN and IPsec stay stock rows below. The switch is the ConnMan Loft row. |

**Remorse.** Deleting a connection, resetting to defaults, resetting routes and clearing the journal do not open a confirm sheet. A bar on top names the action (“Deleting Home”, “Reset”, “Clear”) with a countdown line and “Tap to cancel”; when the countdown runs out, the action runs.

**Simplified interface** (Settings → General): Home is mark, pill, Connect / Disconnect; the pulley stays. Cards and spark hide. List still has Connect/Disconnect and name. Settings stay complete.

The mock nav also has **Home · held**, **Export**, **Edit · menu**, **Settings · menu** and **Log · menu**. The mock nav has a platform section **Sailfish**: **Cover** and **Settings → VPN**.

macOS-only chrome that this mock does not draw: menu-bar popover, SOCKS5 card and port, Dock icon, login item, several simultaneous outbounds, drag-and-drop plate.

### Split routing

The macOS model (macOS DESIGN §5): a **zone** goes through loft or around it, a **service** (name + CIDRs) only belongs to a zone, an **exclusion** CIDR is never routed and survives list pulls. Built-in zones Local (bypass, LAN) and Global (through, `global.bat`); own zones come from the editor. On Sailfish “through loft” is the routes on the ConnMan Loft row and “bypass” stays on the system default route; with the tunnel off everything is direct anyway. There is no remembered direct hop as on macOS.

Entry: Settings → Route → the toggle and the gold **Lists**. The screens are pushed on the Settings stack, with **‹** on top.

| Screen | What |
|--------|------|
| Lists | The title stands alone; under it a switch row **On** / **Off** with its line (through goes into the tunnel, bypass stays direct). Pulley: **Repository · Export · Import · Zones**. **Zone** + **Zones** → editor. Zone capsules with the service count. Quiet line `Global · Through loft`. Search. Rows: name, first CIDRs, network count, source (list / own / excl.); tap edits. **+ Add**. |
| Network | Entry Route / Exclusion, network or address (one CIDR per line when editing), service, zone. Add / Save. |
| Zones | Rows: name, network count · list file, direction, “Built-in”. **+ Add zone**. |
| Zone | Name (locked on a built-in), direction Through loft / Bypass loft, list address + **Lists**, auto-update, Save; an own zone has Delete zone (services → Global). A new zone has the same fields. |
| Repository | List address of the open zone + **Lists**, auto-update, last pull, Update now. |
| Catalog | loft-routes lists: World, Russia, China, Iran, Kazakhstan, Belarus; the current one is lit. A tap sets the address and goes back. |
| Import | From file (Documents), detected format and line count, zone, Add. |
| Export | All zones in one loft-routes file (the zone in `REM zone:`, the service name in a comment), a sample, Save (share). |

---

## 3. Sailfish chrome

Do not restyle Settings → VPN. The mock shows how **Loft** looks in *their* ConnMan list: status Connected, type loft, the switch is the ConnMan Loft row, not a second switch in our app.

Our Connect writes YAML and asks ConnMan to activate the loft connection. If the plugin is missing, the app starts `loft-front` with SOCKS on loopback. If the user turns Loft off in Settings → VPN, the app shows off.

VPN consent is the stock ConnMan prompt, once, if the OS asks. After that Connect does not ask again.

Camera permission is the stock sheet the first time Scan opens. Files: Documents permission; the picker accepts images, text, YAML, `.loft`. The gallery is the system picker.

URL scheme `loft` so a tap on `loft://…` offers import.

Hold maps onto the ConnMan default staying claimed. Copy uses *route* / *direct Internet*.

The cover is the home-screen peek: lamp mark, name, state, overlay IP, one cover action (power icon) that connects / disconnects.

### List gestures

The macOS list uses a ghost Edit on the row; Sailfish uses press-and-hold because there is no pointer. There are no swipe actions; adding lives in the pulley, not on a button.

| Gesture | Action |
|---------|--------|
| Tap | Open the connection (Edit). Connecting is the compact Connect button; if another connection was up, it goes down first. |
| Compact Connect / Disconnect | Same as the macOS list button (`Connect` / `Disconnect`). |
| Press and hold | Context menu under the row (Silica ContextMenu), the rows below move down: Edit; Export when this row is selected; Delete (remorse). |
| Pull down | Pulley: Scan QR code · Paste link · From file · Manually. |
| Export on the selected row | Page **Export Home**: this connection as `Name.loft`; QR `loft://` of the live front; all connections as `Family.loft`. The page repeats the export warning. |

---

## 4. Visual

Same loft tokens as iOS, Android, and macOS: Ink `#1A1714`, Lamp `#E8B84A`, Paper `#E8DFD2`, Avenir Next. The phone chrome in the mock is loft (dark paper). Pulley, page stack, dialog header, remorse and cover are Silica; we do not copy Material or iOS nav.

Type: headings Demi, body Regular, keys SF Mono. Connected = lamp window on. Down = lamp off, stone L.

Safe area: status bar + pulley. One column. Thumb reach: Connect / Disconnect is the large control on Home. There is no tab bar: Home is the root, Connections and Settings come from its pulley, everything else is pushed with **‹** at the top left. Page titles sit right in the highlight colour. Edit is a dialog with Cancel / Save on top; Onboarding is full screen.

Scan: dark finder, lamp-colored corner brackets, two actions under the finder (From file / From gallery).

VPN type is a **capsule** (lamp fill on the selected kind), Main/Advanced and Settings tabs are the same segment as macOS.

System surfaces in the mock (cover, Settings → VPN) keep stock chrome so the product boundary is visible.

---

## 5. Copy

Shared strings — buttons, settings, routes — come from `clients/macOS/lang/*.lang` and read the same on macOS, iOS and Sailfish. Sailfish has its own lines only where macOS has no counterpart: the Home cards, hold banner and Details sheet, the onboarding start screen, the pulley menus, remorse, the cover and the ConnMan consent line. The mock defaults to Russian (`?lang=en` or the RU/EN/中文 control). Home, onboarding and the list compact button use one word: **Connect**; while it runs, **Connecting…**. The key is **Access Key**, not Member key. Copy uses each language’s VPN/routing register (operator Russian, 分流 in Chinese).

| id | EN | RU |
|----|----|----|
| connect | Connect | Подключить |
| pop_connect | Connect | Подключить |
| disconnect | Disconnect | Отключить |
| connecting | Connecting… | Подключение… |
| pill_on | Connected | Подключено |
| pill_off | Not connected · Home › | Не подключено · Home › |
| journal | Log | Журнал |
| reset | Reset routes | Сбросить маршруты |
| settings | Settings | Настройки |
| conns | Connections | Подключения |
| conns_sub | Each has its own key and address. Only one is active at a time. | У каждого свой ключ и адрес. Одновременно активно одно. |
| active | active | активно |
| edit | Edit | Править |
| delete | Delete | Удалить |
| del_remorse | Deleting %s | Удаление %s |
| remorse_cancel | Tap to cancel | Нажмите, чтобы отменить |
| import / export | Import / Export | Импорт / Экспорт |
| warn_export | The file contains the access key. Do not share it. | В файле есть ключ доступа. Никому не передавайте. |
| vpn_type | VPN type | Тип VPN |
| loft_uri | loft:// address | Адрес loft:// |
| edit_main / edit_adv | Main / Advanced | Основные / Дополнительно |
| key_title | Access key | Ключ доступа |
| url_title | Address | Адрес |
| url_go | Connect | Подключить |
| simple | Simplified interface | Упрощённый интерфейс |
| hold | No Internet without VPN | Без VPN — без интернета |
| held_title | The internet is closed until Loft is back | Интернет закрыт, пока Loft не вернётся |
| held_body | That is what “No Internet without VPN” does. | Так задано «Без VPN — без интернета». |
| exp_title | Export %s | Экспорт %s |
| auto | Connect at Launch | Подключаться при запуске |
| reconnect | Reconnect on Disconnect | Переподключаться при обрыве |
| split | Split Routing | Раздельная маршрутизация |
| default_route | Default Route | Маршрут по умолчанию |
| from_gallery | From gallery | Из галереи |
| onb_vpn | If the system asks to allow the VPN, tap Allow. Only once. | Если система спросит разрешение на VPN — нажмите «Разрешить». Это один раз. |
| blurb | On the outside, ordinary HTTPS. Inside, a network of its own. | Снаружи — обычный HTTPS. Внутри — своя сеть. |

Status cards follow the protocol (see Home); speeds are next to the spark bars in the chosen units. The loft network address and MTU are in the Details sheet on tap.

---

## 6. Mapping from the macOS GUI

| macOS | Sailfish mock |
|-------|---------------|
| Status window | Home (root page) |
| Pill tap → Connections | same |
| OVERLAY / FRONT / SOCKS5 | PROTOCOL / FRONT / OPTIONS (per protocol); Overlay IP and Route below; MTU in Details |
| Connections + Several toggle | Connections, exclusive only |
| Add (`+`) | Pulley on Connections (Aurora 5: `+` in the AppBar) |
| Drop `client.yaml` / `.loft` / QR | Import / Scan / Documents / gallery |
| Edit SOCKS5 + tunnel | Tunnel only (ConnMan row) |
| Confirm dialogs | Remorse |
| Export sheet | Export page |
| Page buttons (Delete, Share, Defaults, Log, Copy, Clear, list Import / Export) | Pulley on that page (Aurora 5: **⋮** in the AppBar → PopupMenu) |
| Settings login, Dock, SOCKS, several | omitted |
| Settings failover | omitted (one tunnel) |
| About / Journal | same copy and layout, phone column |

---

## 7. ОС Аврора 5

Mock: [`aurora.html`](aurora.html). Same package tree as Sailfish (`aurora/`, `app.loft.aurora`). Per the [Aurora OS 5.0 release notes](https://developer.auroraos.ru/release_notes/os_5.0.0.60), system apps moved from pulley and action menus to an **AppBar** with a **PopupMenu**. The mock follows that; the rest is the Sailfish mock.

| Screen | Aurora 5 |
|--------|----------|
| Home | AppBar on top with icon buttons **Connections**, **Settings** and **Scan QR code**; Home has just this one action, so it sits on the bar, no PopupMenu. No pulley, no lit line. |
| Connections | AppBar: **‹**, title “Connections”, **+** → PopupMenu with **Scan QR code · Paste link · From file · Manually** (`#list-menu`). |
| Press and hold | As on Sailfish: the context menu (Edit / Export / Delete) opens under its row and pushes the rows below down. |
| Edit, Settings, Log, Lists | The Sailfish pulleys are a **⋮** AppBar button with a PopupMenu over a dimmed page. Edit: the ⋮ is on the left of the title row, because the title is right-aligned; Settings, Log, Lists: the ⋮ is at the top right of the back row. Links use `-menu` instead of `-pulley` (`#edit-menu`, `#settings-menu`, `#journal-menu`, `#list-menu`). |
| Onboard | The consent hint reads “If Aurora asks to allow the VPN, tap Allow. Only once.” |
| Everything else | As in the Sailfish mock: Edit dialog header (Cancel / Save), remorse, page headers, the Export page, the hold banner, cover, Settings → VPN. The mock nav section is **Аврора**. |

Aurora 5 also has **SplitView** for tablets and redrawn covers; the mock does not show them.

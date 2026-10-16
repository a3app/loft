# loft · iOS client

**English** · [Русский](DESIGN_ru.md)

Phone app plus a Packet Tunnel. Mark, palette, and Avenir Next: [`brand/`](../../../brand/). Interactive mock: [`index.html`](index.html). Protocol: [docs/protocol.md](../../../docs/protocol.md). Import URI: [docs/share.md](../../../docs/share.md).

The SwiftUI container will live in [`../App/`](../App/) later. A live session is Go inside the Network Extension, not `loft-front`.

This mock follows the **shipping macOS GUI** (`clients/macOS`, `L10n.swift`, status / list / edit / settings / onboard / journal / about) on a phone. Copy, fields, and IA match that client. iOS keeps one Packet Tunnel, camera scan, swipe/long-press on the list, and the system VPN row. It does not grow SOCKS inbound, Dock, login item, or several simultaneous tunnels.

---

## 1. Product

The inbound is the system VPN (Packet Tunnel). One profile at a time: loft (`lt_…`, no password), SSTP (user + password), or VLESS (`vless://`).

v1: named connections on disk, import/export `.loft`, Open loft / Disconnect, hold, split from protocol lists. `select: auto` inside loft YAML. One tunnel at a time.

A `.loft` file holds **one or several connections**. Kinds mix: `loft`, `sstp`, `vless`. The loft address string in the UI is `loft://`. Spec: [docs/share.md](../../../docs/share.md).

Share: scan a QR or load an image/file with `loft://`, `sstp://`, `vless://`, or a `.loft`.

iOS Settings shows the same VPN row the app starts. The app does not replace Settings.

Apple TV (tvOS 17+) is the same Packet Tunnel after the iPhone GUI. [PLAN](../PLAN.md) phase 6. This mock is the phone.

---

## 2. Screens

Same rooms as the macOS windows, laid out for one column and a thumb.

| Screen | What |
|--------|------|
| Home | Status window. Mark, `loft`, status pill (tap → list), large Open loft / Disconnect. Cards **OVERLAY / FRONT / TUNNEL** and spark when simplified interface is off. Footer **Journal \| Reset routes \| Settings**. Connecting uses this screen: pill “Not connected”, action “Connecting…”, spark “handshake”. |
| Connections | Title, `+`, subtitle “Each has its own key and address. One is active.” Row: compact Connect/Disconnect, name, **host** subtitle, side **active** or kind (`get` / `sstp` / `vless`), ghost Edit. **Leading swipe** Edit, **trailing swipe** Delete. **Long-press** context: Edit / Export `.loft` (selected only) / Delete. Footer **Import \| Export \| Settings**. No “Several” toggle. |
| Edit | Title Connection, **QR** in the header. Name. Capsule **VPN type** loft / SSTP / VLESS / Trojan. Tabs **Main / Advanced**. Main loft = secret `loft://` address with Show/Hide. No SOCKS field. Tunnel interface = Packet Tunnel (forced on: there is no other inbound). SSTP: user, password, server, TLS hostname, cert check, PAP/CHAP. VLESS: UUID, `vless://` server, TLS hostname, cert check. Trojan: password, `trojan://` server, TLS hostname, cert check. Advanced: mux (loft), DNS, DNS into the system; SSTP also “these addresses only”. Save, Back, Delete. |
| Onboard | Two steps, same as macOS. **1 Access key** (`lt_…`) → Continue, or import a file/QR. **2 Address**: URL, how to connect GET/WS/H2/QUIC/H3, path → Open loft. Then the stock iOS VPN permission sheet, once. |
| Settings | Three tabs **General / Route / Client**. Header Reset routes. Links **Defaults \| Journal \| About**. iOS subset: language, simplified interface, speed units, extended journal; default route, hold, split (no failover); auto-connect, reconnect, external IP. Skip Dock, login item, several outbounds, SOCKS. |
| Journal | Front and session lines. The key is shortened. Copy / Clear / Back. |
| About | Mark, loft, `1.0.4`, protocols loft/1.0 · SSTP · VLESS · Trojan, blurb, “Made with love by A³”. |
| Scan | Camera QR. From file / From Photos. Paste `loft://` / `sstp://` / `vless://`. |
| Share QR | `loft://` of one front. Several fronts or several connections → `.loft`. Warns that the file contains the key. |
| iOS Settings → VPN | Stock list. loft Connected, type loft. Toggle is the system tunnel. |

**Simplified interface** (Settings → General): Home is mark, pill, Open loft / Disconnect, footer. Cards and spark hide. List still has Connect/Disconnect and name. Settings stay complete.

macOS-only chrome that this mock does not draw: menu-bar popover, SOCKS5 card and port, Dock icon, login item, several simultaneous outbounds, drag-and-drop plate.

---

## 3. iOS chrome

Do not restyle Settings → VPN. The mock shows how **loft** looks in *their* VPN list: status Connected, type loft, toggle is the system tunnel, not a second switch in our app.

Open loft calls `NETunnelProviderManager`. If the user disables loft in Settings, the app shows down.

VPN permission is the stock sheet once. After that, Open loft does not ask again.

Camera permission is the stock sheet the first time Scan opens (`NSCameraUsageDescription`). Photos uses `PHPicker` (no full library access). Files picker accepts images, text, YAML, `.loft`.

URL scheme `loft` so a tap on `loft://…` offers import.

### List gestures

Mail / Files vocabulary on the phone. The macOS list uses a ghost Edit on the row; iOS adds swipe and long-press because there is no pointer.

| Gesture | Action |
|---------|--------|
| Tap | Select. If another connection was up, disconnect it and connect this one. |
| Compact Connect / Disconnect | Same as the macOS list button (`Connect` / `Disconnect`). |
| Swipe trailing (left) | Delete, with a confirm sheet. |
| Swipe leading (right) | Edit. |
| Long-press | Context menu: Edit; Export `.loft` when this row is selected; Delete. |
| Export on the selected row | Sheet: this connection as `Name.loft`; QR `loft://` of the live front; all connections as `Family.loft`. The sheet repeats the export warning. |

---

## 4. Visual

Same loft tokens as macOS: Ink `#1A1714`, Lamp `#E8B84A`, Paper `#E8DFD2`, Avenir Next. The phone chrome in the mock is loft (dark paper), not iOS grouped gray.

Type: headings Demi, body Regular, keys SF Mono. Connected = lamp window on. Down = lamp off, stone L.

Safe area: status bar + home indicator. One column. Thumb reach: Open loft / Disconnect is the large control on Home. Footer three pills sit on the home indicator.

Scan: dark finder, lamp-colored corner brackets, two actions under the finder (From file / From Photos).

VPN type is a **capsule** (lamp fill on the selected kind), Main/Advanced and Settings tabs are the same segment as macOS.

---

## 5. Copy

Canonical strings are `L10n.swift` in the macOS GUI. The mock defaults to Russian (`?lang=en` or the RU/EN control). Home connect is **Open loft**, not Connect; the list compact button is **Connect**. The key is **Access key**, not Member key.

| id | EN | RU |
|----|----|----|
| connect | Open loft | Открыть лофт |
| pop_connect | Connect | Подключить |
| disconnect | Disconnect | Отключить |
| connecting | Connecting… | Подключение… |
| pill_on | Connected | Подключено |
| pill_off | Not connected | Не подключено |
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
| url_go | Open loft | Открыть лофт |
| simple | Simplified interface | Упрощенный интерфейс |
| hold | Don’t restore direct internet | Не открывать прямой интернет |
| split | Split routing | Раздельная маршрутизация |
| default_route | Allow default route | Маршрут по умолчанию |
| blurb | From the street — ordinary HTTPS. Inside, a network of its own. | С улицы — обычный HTTPS. Внутри горит своя сеть. |

Status cards: **OVERLAY** stays English. FRONT / TUNNEL take the locale (`Front` / `ФРОНТ`, `Tunnel` / `ТУННЕЛЬ`). TUNNEL stands in for the macOS SOCKS5 card: value is MTU while up, `—` while down.

---

## 6. Mapping from the macOS GUI

| macOS | iOS mock |
|-------|----------|
| Status window | Home |
| Pill tap → Connections | same |
| OVERLAY / FRONT / SOCKS5 | OVERLAY / FRONT / TUNNEL (MTU) |
| Connections + Several toggle | Connections, exclusive only |
| Drop `client.yaml` / `.loft` / QR | Import / Scan / Files / Photos |
| Edit SOCKS5 + tunnel | Tunnel only (Packet Tunnel) |
| Settings login, Dock, SOCKS, several | omitted |
| Settings failover | omitted (one tunnel) |
| About / Journal | same copy and layout, phone column |

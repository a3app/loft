# loft · Android client

**English** · [Русский](DESIGN_ru.md)

Phone app plus a `VpnService`. Mark, palette, and Avenir Next: [`brand/`](../../../brand/). Interactive mock: [`index.html`](index.html). Protocol: [docs/protocol.md](../../../docs/protocol.md). Import URI: [docs/share.md](../../../docs/share.md).

The Java container lives in [`../app/`](../app/). A live session is Go inside `LoftVpnService`, not `loft-front`. The thin host already pastes YAML; this mock is the GUI that replaces that paste screen.

---

## 1. Product

The inbound is the system VPN (`VpnService`). One profile at a time: loft (`lt_…`, no password), SSTP (user + password), or VLESS (`vless://`).

v1: named connections on disk, import/export `.loft`, Connect/Disconnect, hold, split from protocol lists. `select: auto` inside loft YAML. One tunnel at a time.

A `.loft` file holds **one or several connections**. Kinds mix: `loft`, `sstp`, `vless`. The list subtitle is **kind · transport · host** (`loft · get · cdn.example.com`). The loft address string is `loft://`. Spec: [docs/share.md](../../../docs/share.md).

Share: scan a QR or load a file/image with `loft://`, `sstp://`, `vless://`, or a `.loft`.

Android Settings shows the same VPN row the app starts. The app does not replace Settings.

Language of the adapter is **Java 17**. gomobile already emits Java; this process does not load the Kotlin runtime. The mock is HTML; the later GUI is Views (no Compose).

Android TV / Google TV is the same `VpnService` and AAR with a Leanback flavor. Not this mock; [PLAN](../PLAN.md) phase 4. VIDAA / Tizen / webOS are out of scope.

## 2. Screens

| Screen | What |
|--------|------|
| Onboard | First launch: paste `lt_…` + URL, scan QR, or pick a `.loft` / image. Then the stock VPN connection request. |
| Home | Lamp, connection name, Connect/Disconnect. Overlay IP / front / bytes when simple view is off. |
| Connecting | Lamp dim, copy “Connecting…”. Cancel returns home down. |
| Connections | List of profiles. Subtitle: kind · transport · host. Tap selects (and will reconnect). **Leading swipe** Edit, **trailing swipe** Delete (`ItemTouchHelper`). **Long-press** context: Edit / Export `.loft` (selected only) / Delete. The selected row (lamp) shows Export. Add → Scan or file. |
| Scan | Camera QR. From file (SAF). From gallery (Photo Picker). Paste `loft://` / `sstp://` / `vless://`. Confirm: name, kind, transport, host; secret hidden. |
| Edit | Name, kind (`loft` / `sstp` / `vless`), address (`loft://…`), secret (hidden after save), transport, path, optional CDN. Fronts for loft: get / ws / h2 / quic / h3. Share QR of the selected outbound. Export `.loft` of this connection. |
| Settings | Hold, split, simple view, About (software + protocol 1.0). Import a `.loft` (one or several connections). Export all as `Family.loft`. |
| Log | Last lines from the service. No key material. Behind Advanced. |
| VLESS / SSTP | Same scanner as Scan. Copy: “Same tunnel.” |

Simple view (default): Home is name + Connect/Disconnect. Settings stay complete.

## 3. Android chrome

Do not restyle Settings → Network & internet → VPN. The mock shows how **loft** looks in *their* VPN list: status Connected, type VPN, the switch is the system tunnel, not a second switch in our app.

Our Connect calls `VpnService.prepare` once, then `startForegroundService(LoftVpnService)`. If the user disables loft in Settings, the app shows down.

VPN permission is the stock **Connection request** sheet once. After that, Connect does not ask again.

Camera permission is the stock sheet the first time Scan opens. Gallery uses the system Photo Picker (no broad media permission). SAF accepts images, text, YAML, `.loft`.

Intent filter `loft` so a tap on `loft://…` offers import. `.loft` files open through the same detector.

### List gestures

Same family as iOS. Views `ItemTouchHelper` plus a long-press `PopupMenu`. Not a pencil-only row.

| Gesture | Action |
|---------|--------|
| Tap | Select. If another connection was up, disconnect it and connect this one. |
| Swipe trailing (left) | Delete, with a confirm dialog. |
| Swipe leading (right) | Edit. |
| Long-press | Menu: Edit; Export `.loft` when this row is selected; Delete. |
| Export on the selected row | Sheet: this connection as `Name.loft`; QR `loft://` of the live front; all connections as `Family.loft`. |

A connected session is a **foreground service** (type `connectedDevice`). The shade shows name, overlay IP, Disconnect. API 33+ also needs the notification permission sheet once.

Hold maps onto the system toggles **Always-on VPN** and **Block connections without VPN**. The app can deep-link into that Settings page; the user flips those switches. Copy uses *route* / *direct Internet*, not “kill switch”.

Quick Settings tile: one tap Connect/Disconnect of the active profile. Lamp on = up.

## 4. Visual

Same loft tokens as iOS and macOS: Ink `#1A1714`, Lamp `#E8B84A`, Paper `#E8DFD2`, Avenir Next. The phone chrome in the mock is loft (dark paper), not Material You purple.

Type: headings Demi, body Regular, keys SF Mono / Roboto Mono. Connected = lamp window on. Down = lamp off, stone L.

Safe area: status bar + gesture pill. One column. Thumb reach: Connect is the large control on Home.

Scan: dark finder, lamp-colored corner brackets, two actions under the finder (From file / From gallery).

System surfaces in the mock (Settings, Connection request, shade, tiles) keep stock Android chrome so the product boundary is visible.

## 5. Copy

| EN | RU |
|----|----|
| loft | loft |
| Connect | Подключить |
| Disconnect | Отключить |
| Connecting… | Подключаем… |
| Connection | Подключение |
| Member key | Ключ участника |
| All traffic | Весь трафик |
| Hold the route | Удерживать маршрут |
| Direct Internet | Прямой Интернет |
| Reset routes | Сбросить маршруты |
| Scan QR | Сканировать QR |
| From file | Из файла |
| From gallery | Из галереи |
| Share QR | QR of the import URI |
| Edit | Править |
| Delete | Удалить |
| Export | Экспорт |
| VLESS | VLESS |
| VPN is in Android Settings | VPN — в Настройках Android |
| Connection request | Запрос подключения |
| Always-on VPN | VPN всегда включён |
| Block connections without VPN | Блокировать без VPN |

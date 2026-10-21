# Loft · Aurora OS client

**English** · [Русский](DESIGN_ru.md) · [中文](DESIGN_zh.md)

Phone app plus a ConnMan overlay row. Mark, palette, and Avenir Next: [`brand/`](../../../docs/brand/). Interactive mock: [`index.html`](index.html). Protocol: [docs/protocol.md](../../../docs/protocol.md). Import URI: [docs/share.md](../../../docs/share.md).

The QML container lives in [`../qml/`](../qml/). A live session is `loft-front`, not gomobile. The thin host already pastes YAML; this mock is the GUI that replaces that paste screen.

The application is **Loft**. The protocol is **loft** / **LOFT**.

---

## 1. Product

The inbound is the system overlay row (ConnMan). One profile at a time: loft (`lt_…`, no password), SSTP (user + password), VLESS (`vless://`), or Trojan (`trojan://`).

v1: named connections on disk, import/export `.loft`, Connect/Disconnect, hold, split from zones. `select: auto` inside loft YAML. One tun at a time.

A `.loft` file holds **one or several connections**. Kinds mix: `loft`, `sstp`, `vless`, `trojan`. The list subtitle is **kind · transport · host** (`loft · get · cdn.example.com`). The loft address string is `loft://`. Spec: [docs/share.md](../../../docs/share.md).

Share: scan a QR or load a file/image with `loft://`, `sstp://`, `vless://`, `trojan://`, or a `.loft`.

Aurora Settings shows the same VPN row the plugin starts. The app does not replace Settings.

Language of the adapter is **Qt 5 / QML Silica**. The mock is HTML; the later GUI is Silica pages (no Widgets, no Compose).

## 2. Screens

| Screen | What |
|--------|------|
| Onboard | First launch: paste `lt_…` + URL, scan QR, or pick a `.loft`. Then the stock ConnMan consent if the OS asks. |
| Home | Lamp, connection name, Connect/Disconnect. Overlay IP, front, bytes. |
| Connecting | Lamp dim, copy “Connecting…”. Cancel returns home down. |
| Connections | List of profiles. Subtitle: kind · transport · host. Tap selects (and will reconnect). Push Edit, Delete with confirm. Long-press: Edit / Export `.loft` / Delete. |
| Scan | Camera QR. From file. Paste `loft://` / `sstp://` / `vless://` / `trojan://`. Confirm: name, kind, transport, host; secret hidden. |
| Edit | Name, kind (`loft` / `sstp` / `vless` / `trojan`). Fields follow the kind. loft and VLESS: capsule **Link / Manual**. loft Link is a `loft://` string; Manual is member key, host, port, transport get / ws / h2 / quic / h3, path. Both write the same profile. VLESS Link is a `vless://` string; Manual is UUID, host, port, TLS·REALITY, TCP·XHTTP, TLS hostname, fingerprint, and the fields that mode needs. Certificate check. Both write `vless://`. SSTP: user, password, server, TLS hostname, cert check, PAP/CHAP. Trojan: password, `trojan://` server, TLS hostname, cert check. Share QR of the selected outbound. Export `.loft` of this connection. |
| Settings | Hold, split (zones), About (software + protocol loft/1.0). Import a `.loft`. Export all as `Family.loft`. |
| Cover | Lamp + Connect/Disconnect. Peek from the home screen. |
| Log | Last lines from loft-front. No key material. Behind Advanced. |

## 3. Aurora chrome

Do not restyle Settings → VPN. The mock shows how **Loft** looks in *their* list: status Connected, type Loft, the switch is the system row, not a second switch in our app.

Our Connect writes YAML and asks ConnMan to activate the loft connection. If the plugin is missing, the app starts `loft-front` with SOCKS on loopback.

Camera permission is the stock sheet the first time Scan opens. Files: Documents permission.

URL scheme `loft` so a tap on `loft://…` offers import.

Hold maps onto the ConnMan default staying claimed. Copy uses *route* / *direct Internet*.

Zones: Through Loft / Around Loft. Direction lives on the zone.

## 4. Visual

Same loft tokens as iOS, Android, and macOS: Ink `#1A1714`, Lamp `#E8B84A`, Paper `#E8DFD2`, Avenir Next. The phone chrome in the mock is loft (dark paper). Silica pulley and cover are Aurora; we do not copy Material or iOS nav.

Type: headings Demi, body Regular, keys SF Mono / Sail Sans. Connected = lamp on. Down = lamp off, stone L.

Safe area: status + pulley. One column. Thumb reach: Connect is the large control on Home.

## 5. Copy

Canonical locale is English. Default UI Russian, matching macOS.

| id | English |
|----|---------|
| connect | Open Loft |
| disconnect | Disconnect |
| connecting | Connecting… |
| menu_about | About Loft |
| split.via | Through Loft |
| split.bypass | Around Loft |
| about.loft | loft/1.0 |

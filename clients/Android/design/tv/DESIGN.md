# loft · Android TV / Google TV

**English** · [Русский](DESIGN_ru.md) · [中文](DESIGN_zh.md)

Same session as the phone: `LoftVpnService` + `clients/mobile` AAR. This mock is the 10-foot shell. Mark, palette, Avenir Next: [`brand/`](../../../../docs/brand/). Interactive mock: [`index.html`](index.html); every state has its own link, e.g. `#home-on`, `#pair`, `#settings-route`. Phone mock: [`../index.html`](../index.html). Protocol: [docs/protocol.md](../../../../docs/protocol.md). Import URI: [docs/share.md](../../../../docs/share.md). Catalog: [README](../../README.md).

The later GUI is Leanback (Java 17 Views, D-pad). Not Compose. `connect` is node-only. Auto = GET.

---

## 1. Product

The box is a living-room host. YouTube, the Play Store, and the rest of the TV use the overlay because Android has one system VPN. Ethernet is the usual last mile; Wi-Fi is a failover. Path change (`NetworkCallback` → `NotifyPathChange`) keeps the overlay IP.

One profile at a time: loft / SSTP / VLESS / Trojan. Named connections on disk, import/export `.loft`, Connect / Disconnect, hold through the system Always-on VPN switches, split as **default route + LAN bypass**. The CIDR list editor stays on the phone.

Flavor `tv` in this tree. `LEANBACK_LAUNCHER`, banner 320×180, `touchscreen` not required, Leanback required. Same `applicationId` family: `app.loft.android.tv` so a phone and a stick can sit on one Google account.

v1 does not put a camera on the TV. Import is QR **on the TV** (the phone scans the screen), a `.loft` from USB / Downloads, or a link typed on the remote.

VIDAA / Tizen / webOS / Roku are out.

---

## 2. Screens

No bottom navigation, no FAB, no swipe. A left rail (Home / Connections / Settings) and a content pane. Default focus is the primary action. System Back is always pop.

| Screen | What |
|--------|------|
| Home | Mark, `loft`, status (lamp + profile name + uptime). Huge **Connect** / **Disconnect**. While off: the profile card on the right (name, `kind · front`, host). While on: overlay IP, public IP, Ethernet/Wi-Fi chip, ↓ / ↑ as two large numbers — no spark chart (unreadable from the sofa). Connecting: **Cancel**, lamp in wait. Empty box: three add tiles instead of Connect. |
| Connections | One horizontal Leanback row. Card: name, `loft · get`, host. The live one has a lamp edge. The last card is **+**. OK on a card makes it active and returns Home. Down from the row: Edit · Share QR · Delete. |
| Add | Three tiles, equal size: **Show QR on this TV** · **From USB or Downloads** · **Type the link**. |
| Pair | The TV shows a large QR and a six-character code (`K7M2P4`). Copy: open loft on the phone → Send to TV → scan this (or type the code). The phone POSTs the YAML to a short-lived listener on the LAN. Back cancels the listener. |
| Share QR | Same chrome as Pair, payload is `loft://` of the selected profile. The phone scans the TV to take the key. |
| File | Leanback picker over USB OTG and `Downloads`. `.loft`, `loft://`, YAML. Confirm: name, kind · transport, host; the secret stays hidden. |
| Paste | Leanback IME. Field `loft://` or YAML. **Add**. |
| Edit | Sparse on purpose. Name, method chips **Auto · GET · WS · H2 · QUIC · H3 · L3**, Save. Full loft / SSTP / VLESS / Trojan forms stay on the phone. A TV remote is not a YAML editor. |
| Settings | Groups **General / Route / Connection**. General: language, extended log, crash reports (GitHub channel). Route: default route, LAN bypass, **No Internet without VPN** as a link **Open VPN settings** (Always-on and Block connections without VPN are system switches). Connection: Connect at launch, Reconnect on disconnect. Footer: Log · About. No CIDR Lists editor. |
| Log | Last lines, key shortened. Clear with a confirm. |
| About | Mark, version, loft/1.0 · SSTP · VLESS · Trojan, blurb, Made with love by A³. |
| Consent | Stock Android TV **Connection request**. Once. Then Connect does not ask. |
| System VPN | Stock Settings → Network & internet → VPN. The mock shows loft in *their* list. |
| Banner | 320×180 for the Google TV / Play row: dark paper, L-mark, word `loft`, lamp on when the session is up. |

---

## 3. Remote

| Key | Action |
|-----|--------|
| ← → | Rail ↔ pane; along a row of cards. |
| ↑ ↓ | Rail icons; settings rows; the action row under Connections. |
| OK | Focused control. On Home that is Connect / Disconnect. |
| Back | Pop. Pair/Share/Paste/File/Edit close. Home Back is the system Leanback exit. |

Focus: 3 px lamp ring, 4 px offset, card scale 1.06. Minimum hit 48 dp. Overscan inset 48 dp. Title ~34 sp, body ~20 sp, hint ~16 sp at 1080p.

The mock on a laptop maps arrow keys, Enter, and Escape to that table.

---

## 4. Import without a camera

| Path | When |
|------|------|
| QR on the TV | Phone and TV on the same LAN. Preferred. |
| Six-character code | Same pairing, no camera on the phone either. |
| USB / Downloads | A stick or a file the operator already dropped on the box. |
| Type `loft://` | Last resort. Leanback IME is slow; the field accepts a paste from a Bluetooth keyboard. |

The phone Scan screen already exists. TV pairing is the inverse: the **screen** is the QR.

Hairpin, dual-stack, `protect(fd)`, FrontIP `excludeRoute` are the phone engine. This mock does not redraw them.

---

## 5. Visual

Tokens of the phone: Ink `#1A1714`, Lamp `#E8B84A`, Paper `#E8DFD2`, Avenir Next. The TV chrome is loft dark paper edge to edge. System dialogs (Connection request, Settings VPN) keep OEM chrome so the product boundary is visible.

Online = lamp window lit. Off = lamp dark, stone L. No Material You lilac. No phone tab bar, no FAB, no QS tile (Android TV has none).

---

## 6. Copy

Shared buttons come from `clients/macOS/lang/*.lang`. TV-only lines: the three add tiles, pairing, USB, remote hints. VPN register of each language (Always-on VPN, operator Russian, 始终开启的 VPN).

| id | EN | RU |
|----|----|----|
| connect | Connect | Подключить |
| disconnect | Disconnect | Отключить |
| add_qr | Show QR on this TV | QR на телевизоре |
| add_usb | From USB or Downloads | С флешки или из Загрузок |
| add_type | Type the link | Набрать ссылку |
| pair_hint | Open loft on the phone → Send to TV | На телефоне: loft → На телевизор |
| remote_ok | OK — connect | OK — подключить |
| underlying_eth | Ethernet | Ethernet |

---

## 7. Out of this mock

Play TV listing. RSS on a 2 GiB stick is an operator measurement (About shows VmRSS). Dist / VERSION untouched unless asked. Flavor `tv`, Leanback Java, banner, and the 10-foot GUI live in `app/src/tv/`.

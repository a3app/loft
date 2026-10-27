# loft · Keenetic panel

**English** · [Русский](DESIGN_ru.md) · [中文](DESIGN_zh.md)

Interactive mock: [`index.html`](index.html). Brand: [`brand/`](../../../docs/brand/). Integration: [docs/keenetic.md](../../../docs/keenetic.md).

The panel is a small LAN site. Keenetic keeps Connection Priorities, policies, and DNS-based routes. This UI only keeps loft and VLESS sessions up and writes their YAML.

---

## 1. Product

Several named connections. Each one is its own OpkgTun and its own row in Keenetic: loft profiles and VLESS alike. Every enabled connection keeps its session up at the same time; Keenetic priorities and policies decide which one carries traffic and what to fall back to.

The operator:

1. Logs into Keenetic web admin.
2. Opens `http://<lan-ip>:8090/` (cookie already valid, or stock login then return).
3. Adds a connection: a `loft://` or `vless://` link, manual fields, or an imported file.
4. In Keenetic: **Network rules → Connection priorities** puts the connection into a policy; **DNS-based routes** send domain lists to it.

No second password. No SSTP form: SSTP stays the stock Keenetic client.

## 2. Screens

| Screen | What |
|--------|------|
| Gate | Checking Keenetic session. 401 → stock `/` with `return=`. |
| Connections | List: lamp, name, kind · front · OpkgTunN, state tag (up / error / off), on/off switch. Buttons: Import, Add. |
| Status | Per connection: state, overlay IP / gateway, MTU, node (with “N more in reserve” from the roster), live front, bytes. Stop, Fronts, “Keenetic priorities”. |
| Connection · loft | Name; type segment loft / VLESS; mode segment **Link / Manual** as in the desktop clients. Link: `loft://` address. Manual: access key, host and port, how to connect (GET · WS · H2 · QUIC · H3 · L3), path. Save, Back, Delete. |
| Connection · VLESS | Same frame. Link: `vless://`. Manual: UUID, host and port, security TLS / REALITY, transport TCP / XHTTP, TLS host name, TLS fingerprint, public key, short ID, Vision. |
| Import | Drop or pick `client.yaml`, `.loft`, `.vpn` or a QR picture. Creates a new connection with the next free OpkgTun. |
| Fronts | Per loft connection: get / ws / h2 / quic / h3 / l3. Order = try order. Intersection with Status `caps`. |
| Journal | Last 80 lines from all connections, prefixed by name. Keys are shortened. |

## 3. Keenetic chrome

Do not restyle Keenetic `:80`. The mock shows two of *their* pages:

- **Other connections:** one WAN-like row per connection (Loft Home = OpkgTun0, VLESS NL = OpkgTun1). The toggle is NDM `up`, not our Start. Our Start brings the session up and then `ndmc` sets `up`.
- **Priorities:** example policies (“Laptops”: Loft Home → VLESS NL, ISP off; “TV”: VLESS NL → ISP), plus a note that split routing lives in DNS-based routes.

The panel never writes priorities, policies, or DNS-based routes. Node route profiles are not applied on the router.

Optional later: a dashboard *card* that is only a link to `:8090`. Versioned inject, never the settings form.

## 4. Page

The page is shared `lanui` v2 (`clients/go/lanui`): connection list, import, loft and VLESS editing, journal. Platform blocks: Keenetic shows OpkgTun names and the priorities link; MikroTik keeps its uplink and `uplink.rsc`. Login on Keenetic is the web-admin session only.

## 5. Visual

Same loft tokens as macOS: Ink `#1A1714`, Lamp `#E8B84A`, Paper `#E8DFD2`, Avenir Next. The Keenetic frame in the mock is light (their admin), so the operator sees both worlds.

Type: headings Demi, body Regular, keys SF Mono. Connected = lamp on. Error = red lamp. Off = stone.

Listen address shown in the footer: `http://192.168.1.1:8090/` — LAN only.

## 6. Copy

Field names come from the desktop clients (`clients/macOS/lang`).

| EN | RU |
|----|----|
| Checking Keenetic session… | Проверяем сессию Keenetic… |
| Connections | Подключения |
| Link / Manual | Ссылка / Вручную |
| loft:// address | Адрес loft:// |
| Access key | Ключ доступа |
| How to connect | Как подключаться |
| vless:// link | Ссылка vless:// |
| Every enabled connection keeps its session up. Keenetic priorities pick which one carries traffic. | Включённые подключения держат сессию все сразу. Какое из них использовать, решают приоритеты Keenetic. |
| Split routing and fallback live in Keenetic | Раздельная маршрутизация и запасной канал — в Keenetic |
| SSTP is the stock Keenetic client | SSTP — штатный клиент Keenetic |

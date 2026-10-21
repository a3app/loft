# loft · Keenetic panel

**English** · [Русский](DESIGN_ru.md) · [中文](DESIGN_zh.md)

Interactive mock: [`index.html`](index.html). Brand: [`brand/`](../../../docs/brand/). Integration: [docs/keenetic.md](../../../docs/keenetic.md).

The panel is a small LAN site. Keenetic keeps Connection Priorities, policies, and DNS routes. This UI only starts the loft session and writes YAML.

---

## 1. Product

One named connection. One OpkgTun. Several fronts inside loft (`select: auto`). VLESS is a later outbound on the same interface.

The operator:

1. Logs into Keenetic web admin.
2. Opens `http://<lan-ip>:8090/` (cookie already valid, or stock login then return).
3. Pastes member key and node URL, picks a front, saves, starts.
4. In Keenetic: **Network rules → Connection priorities** — drag **Loft**.

No second password. No SSTP form.

## 2. Screens

| Screen | What |
|--------|------|
| Gate | Checking Keenetic session. 401 → stock `/` with `return=`. |
| Status | Lamp, overlay IP / gateway / MTU, live front, up/down bytes, Start/Stop. Link: “Open in Keenetic priorities”. |
| Connection | Name, `lt_…` key (hidden after save), URL, type, path, optional CDN profile and uTLS fingerprint. |
| Fronts | Checkboxes get / ws / h2 / quic / h3. Order = try order. Intersection with Status `caps` after handshake. |
| VLESS | Disabled block. Copy: “Same tunnel, later.” |
| Log | Last 80 lines from the daemon. No key material. |

Simple view (default): Status + Connection. Fronts, log, VLESS behind “Advanced”.

## 3. Keenetic chrome

Do not restyle Keenetic `:80`. The mock shows how **Loft** looks in *their* Other connections / priorities: a WAN-like row, description Loft, toggle is NDM `up`, not our Start. Our Start brings the process and then `ndmc` sets `up`.

Optional later: a dashboard *card* that is only a link to `:8090`. Versioned inject, never the settings form.

## 4. Visual

Same loft tokens as macOS: Ink `#1A1714`, Lamp `#E8B84A`, Paper `#E8DFD2`, Avenir Next. The Keenetic frame in the mock is light (their admin), so the operator sees both worlds.

Type: headings Demi, body Regular, keys SF Mono. Connected = lamp window on. Down = lamp off, stone L.

Listen address shown in the footer: `http://192.168.1.1:8090/` — LAN only.

## 5. Copy

| EN | RU |
|----|----|
| Loft | Loft |
| Checking Keenetic session… | Проверяем сессию Keenetic… |
| Connection | Подключение |
| Member key | Ключ участника |
| Use Keenetic for split routing | Маршруты — в Keenetic |
| VLESS later | VLESS позже |
| SSTP is the stock Keenetic client | SSTP — штатный клиент Keenetic |

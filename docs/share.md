# Import URI

**English** · [Русский](share_ru.md) · [中文](share_zh.md)

A client-side import string is described. It is **not** a loft/1.0 wire feature and does not bump the protocol. The URL is never seen by the node.

The loft address is `loft://` — kind plus transport in one string. Query keys `get` `ws` `h2` `h3` `quic` list fronts of **one connection**. Without those keys, one outbound is obtained per URI (`type` plus the URI path). A `.loft` YAML file holds **one or several connections** and MAY mix every supported kind (`loft`, `sstp`, `vless`, `trojan`). File format: [§8](#8-loft-file).

Protocol: [protocol.md](protocol.md). iOS UX: [clients/iOS/design/DESIGN.md](../clients/iOS/design/DESIGN.md). Android UX: [clients/Android/design/DESIGN.md](../clients/Android/design/DESIGN.md). macOS UX: [clients/macOS/design/DESIGN.md](../clients/macOS/design/DESIGN.md). Sailfish UX: [clients/Sailfish/design/DESIGN.md](../clients/Sailfish/design/DESIGN.md).

---

## 1. Detector

A client that accepts a QR payload, a paste, a photo, or a file tries, in order:

1. Trim UTF-8. Strip a leading UTF-8 BOM.
2. `loft://routes/` → a route profile ([§9](#9-route-profiles)), not a connection. Lists import uses this path.
3. `loft://` → [§2](#2-loft).
4. `vless://` → VLESS share ([§5](#5-vless)).
5. `trojan://` → Trojan share ([§6](#6-trojan)).
6. `sstp://` → SSTP share ([§7](#7-sstp)).
7. YAML that has `token:` / `password:` / `outbounds:` / `connections:` → import as `.loft` ([§8](#8-loft-file)). YAML `kind: routes` is [§9](#9-route-profiles), not a connection.
8. Else reject.

The same detector runs on camera QR, an image from Photos or Files, a `.txt` / `.loft` / YAML file, and a paste field.

QR payload is the URI (or YAML) as UTF-8. Error-correction **M** is enough. A typical `loft://` fits QR version 10. A bundle with several connections may not fit a QR; use a file.

---

## 2. loft://

Canonical loft address:

```
loft://<token>@<host>[:port][/path][?query][#name]
```

Clients emit and accept `loft://`.

RFC 3986. Percent-encode `token` and `name` (`application/x-www-form-urlencoded` is wrong for the fragment). Default port **443**. IPv6 host in brackets.

The scheme **is** the connection kind (`loft`). Host:port is the authority (`example.com:443`). Front paths are query keys. Query `type` is the **transport** only in the old form. A list row shows `loft · get · cdn.example.com`.

| Part | Maps to `client.yaml` |
|------|------------------------|
| scheme `loft` | `kind: loft` |
| `token` (userinfo) | `token` (`lt_…`) |
| `host`[:`port`] | node host. Fronts dial it. `edge` is the only exception, and only for the first GET |
| `/path` | old form: outbound `path`. Ignored when `get=` / `ws=` / `h2=` are present |
| `#name` | connection display name; if empty, use `host` |

### Query

| Key | Values | Default | YAML |
|-----|--------|---------|------|
| `get` | HTTP path | `/static/v3/build` when the key is present and empty | GET outbound |
| `ws` | HTTP path | `/api/v1/sync` when present and empty | WS outbound |
| `h2` | HTTP path | `/api/v1/tunnel` when present and empty | H2 outbound |
| `h3` | HTTP path | `/api/v1/tunnel` when present and empty | H3 outbound |
| `quic` | empty flag | — | QUIC outbound (no path) |
| `type` | `get` `ws` `h2` `quic` `h3` `l3` | `get` | one outbound, only when none of `get`/`ws`/`h2`/`h3`/`quic` is present |
| `cdn` | profile name (`yandex`, …) | omit | outbound `cdn` |
| `edge` | absolute `http` or `https` URL | omit | client `edge`. GET only |
| `fp` | TLS fingerprint (`chrome`, …) | `chrome` | outbound `fingerprint` |
| `scheme` | `https` `http` `wss` `ws` | from the front | scheme of `url` |
| `pbk` | node X25519 public, base64url or hex, 32 bytes | omit | outbound `mask_pub` |

Presence of `get` / `ws` / `h2` / `h3` / `quic` enables that front. Empty `get=` is the default GET path. Several of those keys are several outbounds of **one** connection (`select: auto`). After Status, the node front map is source of truth.

Default `scheme`: `ws` → `wss`; everything else → `https`.

Rebuild each outbound URL as `{scheme}://{host}:{port}` plus that front's path.

`edge`, when set, is an absolute `http` or `https` URL. The first GET dials it. The path is the URL path when that path is not `/`; otherwise it is `get=`. The next GET, and `ws` / `h2` / `quic` / `h3`, dial `host`. No `edge`: every front dials `host`. `cdn` stays the legacy profile name, not this URL. Node config `edge: plain|tight` is the Status preset, not this argument. A client that does not know `edge` ignores it and dials `host`.

Omit `insecure_skip_verify`, `header_budget`, and `inflight`. The client uses its usual defaults. Do not put the member key in a query parameter.

Unknown query keys MUST be ignored (forward compatible). An unknown `type` is an error.

### Examples

One connection, several fronts:

```
loft://lt_change-me-long-random@example.com:443/?get=/static/v3/build&ws=/api/v1/sync&h2=/api/v1/tunnel&quic&fp=chrome#bob
```

Node host, CDN only for GET:

```
loft://lt_change-me-long-random@loft.example.com:444/?get=/static/v3/build&ws=/api/v1/sync&h2=/api/v1/tunnel&quic&edge=https://cdn.example.com#Home
```

`url` stays `https://loft.example.com:444`. At dial the client inserts a GET to `https://cdn.example.com` (path `/static/v3/build`) ahead of the node GET. `ws`, `h2`, and `quic` stay on `loft.example.com:444`.

GET on a CDN (old form, still accepted):

```
loft://lt_change-me-long-random@cdn.example.com/static/v3/build?type=get&cdn=yandex&fp=chrome#Home
```

becomes:

```yaml
name: Home
kind: loft
token: "lt_change-me-long-random"
select: auto
outbounds:
  - name: Home
    type: get
    cdn: yandex
    url: https://cdn.example.com
    path: /static/v3/build
    fingerprint: chrome
```

WebSocket:

```
loft://lt_change-me-long-random@app.example.com/api/v1/sync?type=ws&fp=chrome#Office
```

→ `url: wss://app.example.com/api/v1/sync`.

QUIC, no path:

```
loft://lt_change-me-long-random@app.example.com:443?type=quic#Fast
```

→ `url: https://app.example.com:443`.

### Export

A client that can share emits `loft://` for the **selected** connection (query keys for every loft front). Several connections, or mixed kinds: export `.loft` ([§8](#8-loft-file)). The URI is a secret: it contains the member key. Show it on-device; do not log it.

iOS registers URL scheme `loft`. Android registers the same intent filter. Sailfish (and Aurora) registers `x-scheme-handler/loft`. A tap on `loft://…` in Messages opens the app and offers import.

---

## 3. Camera and file

| Source | API |
|--------|-----|
| Live QR (iOS) | `AVCaptureMetadataOutput` object type QR. `NSCameraUsageDescription`. |
| Photo | iOS: `PHPicker`. Android: Photo Picker. macOS: open panel. QR on the still image. |
| File | Images, `public.plain-text`, YAML, `.loft`, `.vpn`. Decode QR from a still image; otherwise treat bytes as §1. |

After a hit: confirm screen with **name**, **kind**, **transport**, **host**. Token stays hidden. Save writes Keychain + App Group YAML. Connect is a separate tap.

---

## 4. What this is not

Not a Hello/Status cap. Not a new mux command. Not a replacement for `.loft`. Not a subscription URL that the client polls.

---

## 5. VLESS

Standard share used by existing VLESS clients. Kind `vless`. Transport is the VLESS `type` / `security`. Supported: TCP+TLS, REALITY, Vision (`xtls-rprx-vision`), XHTTP. List subtitle: `vless · reality · host.example.com`.

```
vless://<uuid>@<host>[:port]?security=…&sni=…&type=…&flow=…&pbk=…&sid=…&fp=…#Name
```

Parse with the same rules as `internal/vless` (`ParseURL`): `security` `tls` / `xtls` / `reality`, `type` (`tcp`, `xhttp`), `flow` containing `vision`, `pbk` / `sid` imply Reality. Default port 443.

The editor has two views of that same share, as in v2rayN / NekoBox / Hiddify / 3x-ui:

- **Link** — one `vless://` line (paste from a panel or Telegram).
- **Manual** — address, port, UUID, TLS/REALITY, TCP/XHTTP, then the fields that mode needs (SNI, fingerprint, `pbk` / `sid`, Vision, XHTTP path/Host).

Manual emits `vless://`. A pasted link that this build does not speak (`ws`, `grpc`, `kcp`) stays intact until the user switches to Manual.

On iOS / Android this becomes a loft connection on the **same** system VPN (plan phase 5). It is not a second VPN row.

---

## 6. Trojan

Standard share used by existing Trojan clients (trojan-gfw / Xray). Kind `trojan`. Password is the URI userinfo. Supported: TCP+TLS, REALITY, XHTTP stream-one, UDP ASSOCIATE. Skip WS / gRPC / kcp. List subtitle: `trojan · reality · host.example.com`.

```
trojan://<password>@<host>[:port]?security=…&sni=…&type=…&pbk=…&sid=…&fp=…#Name
```

Parse with the same rules as `internal/trojan` (`ParseURL`): `security` `tls` / `reality`, `type` (`tcp`, `xhttp`), `pbk` / `sid` imply Reality, `allowInsecure=1` skips dest cert check. Default port 443. Vision is VLESS-only.

On iOS / Android this is the same system VPN row as loft. Code: `internal/trojan`.

---

## 7. SSTP

Same system VPN as loft. Kind `sstp`. Transport is TLS + PPP. List subtitle: `sstp · tls · vpn.example.com`. Code: `internal/sstp`. Not a loft/1.0 cap.

```
sstp://<user>:<password>@<host>[:port][/path]?sni=…&pap=1#Name
```

Default port **443**. Default path empty (the client uses the usual SSTP Connect URI). `user` and `password` are percent-encoded. Query `sni` is TLS ServerName; `pap=1` allows PAP/CHAP-MD5 (default is MSCHAPv2 only).

YAML mapping (same as macOS export):

```yaml
name: Office
kind: sstp
username: alice
password: "secret"
outbounds:
  - name: Office
    type: sstp
    url: https://vpn.example.com
```

A `.vpn` JSON blob that already has host / user / password is accepted as this connection. Path change redials; IPCP may mint a new IP.

---

## 8. `.loft` file

Family format for every loft app (iOS, Android, macOS, Sailfish, later TV). YAML. The file **is** the secret.

Two shapes. Both are valid.

### One connection

Today’s `client.yaml`, plus optional `kind`. Omit `kind` and infer it from `outbounds[].type` (`get`/`ws`/`h2`/`quic`/`h3`/`l3` → `loft`, `sstp` → `sstp`, `vless` → `vless`, `trojan` → `trojan`).

```yaml
name: Home
kind: loft
token: "lt_change-me-long-random"
select: auto
outbounds:
  - name: cdn
    type: get
    url: https://cdn.example.com
    path: /static/v3/build
```

### Several connections

Top-level `connections:` is a list. Each item is one connection of any kind. One file MAY mix loft, SSTP, VLESS, and Trojan.

```yaml
connections:
  - name: Home
    kind: loft
    token: "lt_change-me-long-random"
    select: auto
    outbounds:
      - name: cdn
        type: get
        url: https://cdn.example.com
        path: /static/v3/build
  - name: Office
    kind: sstp
    username: alice
    password: "secret"
    outbounds:
      - name: Office
        type: sstp
        url: https://vpn.example.com
  - name: Travel
    kind: vless
    outbounds:
      - name: Travel
        type: vless
        url: vless://uuid@de1.example.com:443?security=reality&sni=www.example.com
  - name: Edge
    kind: trojan
    password: secret
    outbounds:
      - name: Edge
        type: trojan
        url: trojan://secret@de1.example.com:443?security=reality&sni=www.example.com
```

A document MUST NOT combine a top-level `token:` / `outbounds:` with a non-empty `connections:`.

Import: each list item becomes a profile on disk. Phone apps still run **one** tunnel at a time. macOS may keep several sessions.

### Export

| What is selected | File |
|------------------|------|
| One connection, one front | QR / paste `loft://` (or `sstp://` / `vless://` / `trojan://`) |
| One connection, several fronts | `Name.loft` (single document) |
| The selected connection | `Name.loft` (single document) |
| Several / all connections | `Family.loft` (`connections:`) |

Export of the selected connection is a list action (iOS/Android: chip on the lamp row; long-press; macOS: row context menu). Settings MAY still export all.

The UI description of a connection is **kind · transport · host**, never transport alone.

---

## 9. Route profiles

A signed lists blob (§10.3) maps to a SplitStore JSON document (`src=op`). Share of that document is a **route profile**, not a loft connection. The connection detector MUST refuse it (`loft://routes` has an empty token, so it must not become a connection named `routes`).

```
loft://routes/<base64url(SplitJSON)>
```

`ParseRoutes` also accepts the SplitJSON object itself and YAML:

```yaml
kind: routes
name: RU direct
dns: 77.88.8.8
src: op
zones: […]
services: […]
```

or `kind: routes` plus `profile:` holding the same base64url payload. Lists **+ Import profile** adds it to the catalog; the active profile is left alone. The operator profile that arrives inside the session (`lists_inline`) overwrites the `op` slot and is read-only.

# loft 1.0.23

**English** · [Русский](#русский) · [中文](#中文)

loft is an overlay that runs inside TLS 1.3. Apps talk to local SOCKS5 or TUN. Streams share one mux, sealed with ChaCha20-Poly1305, and ride ordinary HTTPS: GET, WebSocket, HTTP/2, QUIC, or HTTP/3. A CDN or reverse proxy can sit in front of the node. Protocol **loft/1.0** (unchanged). Share URI `loft://`, YAML `kind: loft`.

Typical use is operator access: a lab LAN from outside, a server fleet, site-to-site.

## In this Dist

| | Apple Silicon / arm64 | Intel / amd64 |
|--|--|--|
| macOS app (10.13+, ad-hoc signed) | `loft-macos-arm64.dmg` | `loft-macos-x86_64.dmg` |
| macOS CLI | `loft-darwin-arm64.tar.gz` | `loft-darwin-amd64.tar.gz` |
| Linux CLI | `loft-linux-arm64.tar.gz` | `loft-linux-amd64.tar.gz` |
| Node | `loft-node-linux-arm64.tar.gz` · `loft-node_arm64.deb` | `loft-node-linux-amd64.tar.gz` · `loft-node_amd64.deb` |
| Panel | `loft-panel-linux-arm64.tar.gz` · `loft-panel_arm64.deb` | `loft-panel-linux-amd64.tar.gz` · `loft-panel_amd64.deb` |

Checksums: `loft-clients-SHA256SUMS.txt`, `loft-node-SHA256SUMS.txt`, `loft-panel-SHA256SUMS.txt`.

CLI archives contain `loft`, `loft-front`, `loft-probe`, `loft-bench`.

Not in Dist: iOS / Android / Aurora / Keenetic / MikroTik clients (catalog and mocks).

## What it does

- **macOS app** — menu bar. Named connections, import `loft://` or `.loft`, local SOCKS5 and TUN, split routes, reconnect, failover.
- **loft / loft-front** — CLI session. Same fronts as the app.
- **loft-node** — controller or relay. Debian package or POSIX tarball. Decoy splice, borrowed-cert mask, signed roster and address lists, member keys.
- **loft-panel** — web admin on the node: wizard, members, packages, release updates.
- **loft-probe / loft-bench** — overlay ICMP/TCP/HTTP checks and GET/WS throughput.

## 1.0.23

Protocol stays **loft/1.0**. Auth is DH 0x02 only.

- Omitted `fronts` is GET only. Empty GET path is `/static/v3/build` (`DefaultGETPath`). An explicit `/static/js` is left as written.
- GET rebinds after an egress change without Park.
- Mux ingest never blocks on one hog stream: datagrams drop, parked Data then RST.
- TUN keeps reading utun when mux or TUN write is congested.
- HMAC-only 0x02 and 0x03 tickets from 1.0.16 are gone. A 1.0.16 client cannot join.
- HTTP/2 and HTTP/3 CONNECT require Match on the tunnel path.

macOS:

- `-path` is GET only. A leftover GET cover is not sent as h2 CONNECT.
- Boot reaps bundled `loft-front` via libproc (no `ps` pipe stall).
- `--probe` walks every saved profile and fails closed on an empty overlay.
- GitHub `/releases/latest` 404 is “current”, not “no network”.

## 1.0.22

- Origin CONNECT is no longer an open TCP proxy. Cap `connect` is off unless `fronts` lists `connect`.
- Hold resume parks the mux by the authenticated member plus `instance_id`.
- `rate_bps` is a per-session token bucket on bulk Data/Dgram. Node reloads `network.conf` and `members.keys` on SIGHUP.
- macOS split ignore entries; loft-priv asks for the admin password once.
- Panel first Dist packages: wizard, members, mail, MTProto (`mtg`).

---

## Русский

Оверлей внутри TLS 1.3. Приложения ходят в локальные SOCKS5 или TUN. Потоки в одном mux, ChaCha20-Poly1305, снаружи обычный HTTPS: GET, WebSocket, HTTP/2, QUIC или HTTP/3. Перед нодой может стоять CDN. Протокол **loft/1.0**. Ссылка `loft://`.

В Dist: приложение macOS (DMG arm64 и x86_64), CLI Linux/macOS (`loft`, `loft-front`, `loft-probe`, `loft-bench`), нода (tar.gz и .deb amd64/arm64), панель (tar.gz и .deb). iOS, Android, Aurora, Keenetic, MikroTik в этот Dist не входят.

1.0.23: публичный путь по умолчанию только GET; пустой GET-path `/static/v3/build`; GET перепривязывается при смене egress; mux не стопорит ingest; utun читается при заторе; dual-stack 1.0.16 снят; H2/H3 CONNECT только на tunnel path. macOS: `-path` только у GET, reap через libproc, `--probe` по всем профилям, 404 GitHub Release — текущая версия. Протокол loft/1.0.

---

## 中文

loft 是跑在 TLS 1.3 里面的覆盖网。应用走本机 SOCKS5 或 TUN。流复用，ChaCha20-Poly1305，外面是普通 HTTPS：GET、WebSocket、HTTP/2、QUIC、HTTP/3。节点前面可以接 CDN。协议 **loft/1.0**。分享 URI `loft://`。

本 Dist：macOS 应用（arm64 / x86_64 DMG）、Linux/macOS CLI（`loft`、`loft-front`、`loft-probe`、`loft-bench`）、节点（tar.gz 和 .deb）、面板（tar.gz 和 .deb）。iOS / Android / Aurora / Keenetic / MikroTik 不在这次包里。

1.0.23：默认公开路径只有 GET；空 GET path 为 `/static/v3/build`；出口变化时 GET 重绑；mux 不再堵住 ingest；utun 在拥塞时继续读；去掉 1.0.16 dual-stack；H2/H3 CONNECT 必须 Match 隧道路径。macOS：`-path` 只给 GET，libproc 收尸，`--probe` 扫全部配置，GitHub Release 404 视为已是最新。协议仍是 loft/1.0。

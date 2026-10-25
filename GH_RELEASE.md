# loft 1.0.29

**English** · [Русский](#русский) · [中文](#中文)

loft is an overlay that runs inside TLS 1.3. Apps talk to local SOCKS5 or TUN. Streams share one mux, sealed with ChaCha20-Poly1305, and ride ordinary HTTPS: GET, WebSocket, HTTP/2, QUIC, or HTTP/3. A CDN or reverse proxy can sit in front of the node. Protocol **loft/1.0** (unchanged). Share URI `loft://`, YAML `kind: loft`.

Typical use is operator access: a lab LAN from outside, a server fleet, site-to-site.

## In this Dist

| | Apple Silicon / arm64 | Intel / amd64 |
|--|--|--|
| macOS app (10.13+, ad-hoc signed) | `loft-1.0.29-macos-arm64.dmg` | `loft-1.0.29-macos-x86_64.dmg` |
| macOS CLI | `loft-1.0.29-darwin-arm64.tar.gz` | `loft-1.0.29-darwin-amd64.tar.gz` |
| Linux CLI | `loft-1.0.29-linux-arm64.tar.gz` | `loft-1.0.29-linux-amd64.tar.gz` |
| Node | `loft-node-1.0.29-linux-arm64.tar.gz` · `loft-node_1.0.29_arm64.deb` | `loft-node-1.0.29-linux-amd64.tar.gz` · `loft-node_1.0.29_amd64.deb` |
| Panel | `loft-panel-1.0.29-linux-arm64.tar.gz` · `loft-panel_1.0.29_arm64.deb` | `loft-panel-1.0.29-linux-amd64.tar.gz` · `loft-panel_1.0.29_amd64.deb` |
| Android (sideload, GitHub channel) | `loft-1.0.29-android.apk` · `loft-1.0.29-android-tv.apk` | same fat APKs |

Checksums: `loft-clients-1.0.29-SHA256SUMS.txt`, `loft-node-1.0.29-SHA256SUMS.txt`, `loft-panel-1.0.29-SHA256SUMS.txt`, `loft-android-1.0.29-SHA256SUMS.txt`.

CLI archives contain `loft`, `loft-front`, `loft-probe`, `loft-bench`.

Not in Dist: iOS / Sailfish / Keenetic / MikroTik clients (catalog and mocks).

## What it does

- **macOS app** — menu bar. Named connections, import `loft://` or `.loft`, local SOCKS5 and TUN, split routes, reconnect, failover.
- **Android** — phone `VpnService` and Leanback TV. Named connections, `loft://` / `.loft` import, overlay as one system VPN. GitHub APKs self-update from Dist; Play builds compile the updater out.

| Home | Connections |
|:--:|:--:|
| <img src="https://raw.githubusercontent.com/a3app/loft/main/docs/img/android/en-home.png" alt="loft Android home" width="240"> | <img src="https://raw.githubusercontent.com/a3app/loft/main/docs/img/android/en-conns.png" alt="loft Android connections" width="240"> |

<img src="https://raw.githubusercontent.com/a3app/loft/main/docs/img/android/en-tv.png" alt="loft on Android TV" width="640">

- **loft / loft-front** — CLI session. Same fronts as the app.
- **loft-node** — controller or relay. Debian package or POSIX tarball. Decoy splice, borrowed-cert mask, signed roster and address lists, member keys.
- **loft-panel** — web admin on the node: wizard, members, packages, domains, release updates.
- **loft-probe / loft-bench** — overlay ICMP/TCP/HTTP checks and GET/WS throughput.

## 1.0.29

Protocol stays **loft/1.0**.

Inner data cipher:

- A direct front may confirm 0 (clear), 1 (ChaCha20-Poly1305), or 2 (AES-256-GCM) after Status. A missing byte is ChaCha. GET stays ChaCha. Node `inner` lists extra ids. ChaCha cannot be turned off.

Spec:

- §13 registries for cap bits, mux commands, and status codes, with reserved and experimental ranges.
- §11 Security Considerations. §12 Privacy.
- RFC 10024 title is the PQ/T hybrid. Group X25519MLKEM768 is 4588 (0x11EC).
- Handshake test vectors.

GET / transport:

- Mux lanes attach on the live session, including after GET moves to another front.
- A tight edge asks for four HTTP/2 connections and answers before the 5s origin cut. Status carries the edge byte.
- Socket buffers stay on kernel autotune. The unsent cap is 16 KiB. A full stream parks 8 MiB before Reset.
- WebSocket dial URLs are sanitized. A hold that already moved does not send Leave.

Share:

- `loft://` authority is the node. Optional `edge` is the absolute URL of the first GET.

Panel / egress:

- Segments open as tabs, with a per-segment IP. A VLAN has its own L3. VLAN ID is a property.
- Ping check. Egress checks are ping, TLS, auto, and direct.

macOS / Android:

- SOCKS leaves the 32-slot copy gate. The app learns `mask_pub` and does not put the edge host on 0/1.
- Android splash uses the lockup. TV settings have language chips. `loft-tv.app` and `loft-gtv.app` pack beside `loft.app`.

## 1.0.28

Protocol stays **loft/1.0**.

Android:

- Dist publishes signed GitHub-channel APKs: phone `loft-1.0.28-android.apk` (`app.loft.android`) and TV `loft-1.0.28-android-tv.apk` (`app.loft.android.tv`). Flavor `github` has the in-app updater; `store` compiles it out. Asset names include the version.

GET / fronts:

- Extra mux fronts attach in the background so overlay STATUS is the GET session.
- A CDN GET does not attach origin WS/H2/QUIC/H3.
- Tune404 shares Calm: inflight drops after repeated 404.
- loft-front `-fronts` offers Hello mux fronts on a single GET outbound.

macOS:

- One GET outbound; Hello fronts from `-fronts`. A CDN host stays GET-only.
- Per-front enable switches. Share `sstp://`.

Panel:

- Internet: Adapters, External channels, Connection priorities.
- Channel snapshot cache. SSTP save does not wait on the host snapshot. Node reload retries the save.

Node:

- Origin GET uses the node `cdn:` profile.
- `egress-status.json` is written on load. SSTP starts as connecting.

## 1.0.27

Protocol stays **loft/1.0**.

Android:

- Phone GUI is native Java Views matching the DESIGN mock (Home spark, connections, FAB sheets, split lists).
- Flavor `tv` (`app.loft.android.tv`) is Leanback 10-foot UI. Same AAR and VpnService.
- `make release` writes ad-hoc-signed sideload APKs. PLAN.md dropped.
- Emulator `loft.app` pins loft, Browser, Apps (launcher drawer), and Settings.

GET / CI:

- Origin 404 drops the GET sid. A radio gap replaces the GET pool. Pages deploys from Actions.

Panel:

- Settings pick which parts go into the `.loft` outbound name: host, member, front (default member+front).

## 1.0.26

Protocol stays **loft/1.0**.

Android phone (GET overlay):

- Overlay DNS on gateway:53 is answered off-TUN so the browser resolves while GET hold is 80ms. AAAA is NODATA until overlay IPv6 exists.
- PreferGo resolver bypasses overlay/private nameservers on protected sockets.
- RoutePlan omits `::/0` without overlay IPv6 (gVisor is IPv4-only).
- `instance_id` persists across process kill. Path watch uses NOT_VPN. Protect before TUN is skipped. Fatal auth is not retried.
- systemExempted foreground service; emulator `loft.app`.

Front / macOS:

- Overlay sockets pin to the physical uplink. `auth_bad_key` surfaces on the Home pill.
- Through-loft no longer blackholes the default when tun-up misses.

Identity / panel:

- Old member tokens stay until network apply.
- Member and node edits apply without a Save button.

## 1.0.25

Protocol stays **loft/1.0**.

macOS:

- loft-front loads on 10.13. Go 1.22+ bound `SecTrustCopyCertificateChain` (macOS 12); dyld aborted before `main`. A stdlib overlay uses `SecTrustEvaluate` so those 12.0 Security symbols are not in the binary.
- Through-loft routes stay on utun when 0/1 owns default.

Fronts:

- Opt-in front `l3`: HTTP/2 CONNECT carrying corked AEAD IP datagrams. Not in FrontOrder. GET stays the default.
- A GET-only node no longer starts a UDP listen.

Panel:

- Overview, My account, zone page, search, help, and the system log are live.
- Connection priorities: multipath lists, per-channel speed limits, link checks.
- Node fronts list `l3`.

## 1.0.24

Protocol stays **loft/1.0**.

Panel:

- Keep member keys is opt-in. The panel can store a copy of the issued secret so the operator can show the link, QR, or `.loft` again without reissuing. Default off. Turning it off wipes the panel copy; `members.keys` on the node stays.
- Wizard step Domain and DNS: Cloudflare, DigitalOcean, Namecheap, or manual. Creates the provider, zone, A/AAAA, and CAA before names are saved.
- Domains page is live: zones, records, HTTP-01 and DNS-01.

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

В Dist: приложение macOS (DMG arm64 и x86_64), CLI Linux/macOS (`loft`, `loft-front`, `loft-probe`, `loft-bench`), нода (tar.gz и .deb amd64/arm64), панель (tar.gz и .deb), Android sideload APK (телефон и TV). iOS, Sailfish, Keenetic, MikroTik в этот Dist не входят.

1.0.29: прямой фронт может подтвердить clear или AES-256-GCM после Status, GET остаётся ChaCha20-Poly1305; реестры номеров, вопросы безопасности и конфиденциальность; буферы сокета на autotune, unsent 16 КиБ, park потока 8 МиБ; линии mux на живой сессии; tight — четыре HTTP/2; в loft:// authority — нода, edge — URL первого GET; панель — сегменты вкладками, у VLAN свой L3, проверка ping. Протокол loft/1.0.

1.0.28: Dist публикует подписанные GitHub APK, имена файлов с версией; GET вешает лишние фронты в фоне и не цепляет их на CDN; macOS — один GET outbound и `sstp://`; панель — Адаптеры / Внешние каналы. Протокол loft/1.0.

---

## 中文

loft 是跑在 TLS 1.3 里面的覆盖网。应用走本机 SOCKS5 或 TUN。流复用，ChaCha20-Poly1305，外面是普通 HTTPS：GET、WebSocket、HTTP/2、QUIC、HTTP/3。节点前面可以接 CDN。协议 **loft/1.0**。分享 URI `loft://`。

本 Dist：macOS 应用（arm64 / x86_64 DMG）、Linux/macOS CLI（`loft`、`loft-front`、`loft-probe`、`loft-bench`）、节点（tar.gz 和 .deb）、面板（tar.gz 和 .deb）、Android 旁路 APK（手机和电视）。iOS / Sailfish / Keenetic / MikroTik 不在这次包里。

1.0.29：直连前置可在 Status 之后确认 clear 或 AES-256-GCM，GET 仍是 ChaCha20-Poly1305；编号表、安全考虑与隐私；套接字缓冲交给内核 autotune，unsent 上限 16 KiB，流 park 8 MiB；mux 线路挂在活会话上；tight 要四条 HTTP/2；loft:// 的 authority 是节点，edge 只是第一次 GET 的 URL；面板里网段以标签打开，VLAN 自有 L3，有 ping 检查。协议仍是 loft/1.0。

1.0.28：Dist 发布已签名 GitHub APK，文件名带版本；GET 在后台挂额外前置、CDN 上不挂；macOS 单 GET outbound 和 `sstp://`；面板「适配器 / 外部通道」。协议仍是 loft/1.0。

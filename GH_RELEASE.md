# loft 1.0.35

**English** · [Русский](#русский) · [中文](#中文)

loft is an overlay that runs inside TLS 1.3. Apps talk to local SOCKS5 or TUN. Streams share one mux, sealed with ChaCha20-Poly1305, and ride ordinary HTTPS: GET, WebSocket, HTTP/2, QUIC, or HTTP/3. A CDN or reverse proxy can sit in front of the node. Protocol **loft/1.0** (unchanged). Share URI `loft://`, YAML `kind: loft`.

Typical use is operator access: a lab LAN from outside, a server fleet, site-to-site.

## In this Dist

| | Apple Silicon / arm64 | Intel / amd64 |
|--|--|--|
| macOS app (10.13+, ad-hoc signed) | `loft-1.0.35-macos-arm.dmg` | `loft-1.0.35-macos-intel.dmg` |
| macOS CLI | `loft-1.0.35-darwin-arm64.tar.gz` | `loft-1.0.35-darwin-amd64.tar.gz` |
| Linux CLI | `loft-1.0.35-linux-arm64.tar.gz` | `loft-1.0.35-linux-amd64.tar.gz` |
| Node | `loft-node-1.0.35-linux-arm64.tar.gz` · `loft-node_1.0.35_arm64.deb` | `loft-node-1.0.35-linux-amd64.tar.gz` · `loft-node_1.0.35_amd64.deb` |
| Panel | `loft-panel-1.0.35-linux-arm64.tar.gz` · `loft-panel_1.0.35_arm64.deb` | `loft-panel-1.0.35-linux-amd64.tar.gz` · `loft-panel_1.0.35_amd64.deb` |
| Android (sideload, GitHub channel) | `loft-1.0.35-android.apk` · `loft-1.0.35-android-tv.apk` | same fat APKs |
| Windows portable (GitHub) | `loft-1.0.35-windows-arm64.zip` | `loft-1.0.35-windows-x86-64.zip` · `loft-1.0.35-windows-x86.zip` |
| MikroTik container | `loft-mikrotik-1.0.35-arm64.tar` · `loft-mikrotik-1.0.35-armv7.tar` | `loft-mikrotik-1.0.35-amd64.tar` |

Checksums: `loft-clients-1.0.35-SHA256SUMS.txt`, `loft-node-1.0.35-SHA256SUMS.txt`, `loft-panel-1.0.35-SHA256SUMS.txt`, `loft-android-1.0.35-SHA256SUMS.txt`, `loft-windows-1.0.35-SHA256SUMS.txt`, `loft-mikrotik-1.0.35-SHA256SUMS.txt`.

CLI archives contain `loft`, `loft-front`, `loft-probe`, `loft-bench`. Windows x86-64 and x86 are Win7 SP1+; arm64 is Win10/11 ARM. MikroTik image is also `ghcr.io/a3app/loft-mikrotik:1.0.35`.

Not in Dist: iOS / Sailfish / Keenetic clients (catalog and mocks).

## What it does

- **macOS app** — menu bar. Named connections, import `loft://` or `.loft`, local SOCKS5 and TUN, split routes, reconnect, failover.
- **Android** — phone `VpnService` and Leanback TV. Named connections, `loft://` / `.loft` import, overlay as one system VPN. GitHub APKs self-update from Dist; Play builds compile the updater out.
- **Windows** — tray client. x86-64 and x86: Windows 7 SP1 and later. arm64: Windows 10/11 ARM. Unzip and run. The GitHub build replaces its own directory from the next release zip. The Store layout is not in this Dist.
- **MikroTik** — RouterOS container. Upload a docker-save tarball, or pull `ghcr.io/a3app/loft-mikrotik`.

| Home | Connections |
|:--:|:--:|
| <img src="https://raw.githubusercontent.com/a3app/loft/main/docs/img/android/en-home.png" alt="loft Android home" width="240"> | <img src="https://raw.githubusercontent.com/a3app/loft/main/docs/img/android/en-conns.png" alt="loft Android connections" width="240"> |

<img src="https://raw.githubusercontent.com/a3app/loft/main/docs/img/android/en-tv.png" alt="loft on Android TV" width="640">

- **loft / loft-front** — CLI session. Same fronts as the app.
- **loft-node** — controller or relay. Debian package or POSIX tarball. Decoy splice, borrowed-cert mask, signed roster and address lists, member keys.
- **loft-panel** — web admin on the node: wizard, members, packages, domains, release updates.
- **loft-probe / loft-bench** — overlay ICMP/TCP/HTTP checks and GET/WS throughput.

## 1.0.35

Protocol stays **loft/1.0**.

Android:

- Apps no longer crash while loft is connected with split routing on. Big lists gave the VPN about 7,000 routes, and Android could not deliver a route table that large to apps watching the network. The route table now stays under 1,200 routes: lists that fit are unchanged, larger ones are merged into wider blocks at the edges. Blocked addresses are still checked exactly.

## 1.0.34

Protocol stays **loft/1.0**.

Android — smoothness and stability (UI hangs on Xiaomi HyperOS):

- The VPN session runs on its own thread. The once-a-second poll no longer re-reads the route lists, re-saves the route profiles or rebuilds the notification on the UI thread.
- Route lists and route profiles moved out of shared preferences into their own files, written atomically in the background. Settings changes no longer rewrite megabytes.
- Settings changes are merged into one tunnel rebuild. Screens redraw only what changed, at most ten times a second.
- The TUN reader no longer leaks a thread on every tunnel rebuild, allocates nothing per packet, and checks the block list by binary search.
- Saved connections are written atomically. A keystore hiccup at start can no longer wipe them.
- Apps, Lists, Scan, Share QR, TV and the self-updater do disk, network and system work in the background. On TV, D-pad focus no longer jumps every second.
- Fixed: quick-settings tile on Android 14+, duplicate import after rotation, stop during the handshake, list download racing with edits.
- Every Android source file is commented for newcomers.

Engine:

- No more retrying interface lookups that Android 11+ denies once a second.

## 1.0.33

Protocol stays **loft/1.0**.

Dist:

- GUI assets: `macos-arm` / `macos-intel` DMG, `windows-x86-64` / `x86` / `arm64` zip. CLI and Debian names stay Go/ISA. The updater still accepts the old suffixes.
- MikroTik container tarballs (amd64, arm64, armv7) and `ghcr.io/a3app/loft-mikrotik`.

Android:

- Scan paste is a textarea. The add-connection sheet is two floating cards, as in the mock.
- Settings, About, and Scan reflow at a large system font.
- The connections list no longer shows the SOCKS port.

MikroTik / LAN page:

- SOCKS5 can be off. LAN gateway is nat (default) or route.

Node:

- Overlay leases survive a restart. Linux TUN is configured over netlink.
- Ping to a VLAN member's own gateway; member internet ping says when it cannot.

macOS:

- Georgian UI language.

Panel:

- HELO name and one-click Roundcube login.

## 1.0.32

Protocol stays **loft/1.0**.

Panel:

- Deleting a mail domain can also delete the DNS records the panel made for it (MX, mail A/AAAA, SPF, DKIM, DMARC); a box in the confirmation, on by default. Hand-made or hand-edited records stay. The mail.<domain> certificate goes with the domain.
- Namecheap: record ids are HostIds, not positions in the zone. Saved ids no longer point at other records after a deletion (leftover ACME challenges, the address-change update). Old saved ids are repaired on the next read of the zone.

Node:

- Built with Go 1.27+, the node turns on HTTP/2 extended CONNECT itself (re-exec once with GODEBUG=http2xconnect=1), so the h2 and l3 fronts keep working.

Dist:

- SHA256SUMS of node, panel and CLI list bare file names, so `sha256sum -c` works in the download folder. CI checks it.

Android:

- Redacted crash reports as GitHub issues. Chrome reflows at a large system font; launcher mark inside the safe zone; the catalog no longer opens blank.

## 1.0.31

Protocol stays **loft/1.0**.

Route profiles:

- lists_inline is cap bit 30. Mux CmdLists is 13. The node signs the lists blob with a profile header. Kinds 4–6 are metadata (`dir`, `label`, `dns`). An empty-name set is the profile header.
- Operator copy is read-only (`src=op`); Make a copy. Share URI `loft://routes/`. Panel editor on node lists. Android `ProfileStore`.

Android:

- Block zone, Local DNS, and Apps around the tunnel.
- Lists extras: swipe, select, undo, exclusions.

Congestion:

- Hello asks, Status answers (`off`, `bbr`, `fixed`). QUIC fronts switch via the a3app/quic-go fork. TCP stays on the kernel default. The installer and `.deb` load `tcp_bbr` and allow it for user loft.

Windows:

- Win32 tray client, Windows 7 SP1. GitHub portable zip replaces its own directory. App, `.loft`, installer, loft-svc and loft-front icons.

Mux / panel / macOS:

- Data pushed while a stream dies no longer sends on its closed channel.
- Panel: VLAN isolate, port sort, switch LEDs. MTProto TLS domain defaults to the node name.
- macOS: leave the other session's default when one utun exits.

P2P / VLESS:

- Sealed P2P handshake: no LFT preface, data under ChaCha20-Poly1305, replayed or stale hellos refused. Witness reports inside TLS.
- VLESS ALPN from the share URL. XHTTP auto/stream-one on one h2 ClientConn.

## 1.0.30

Protocol stays **loft/1.0**.

VLESS client:

- XHTTP `stream-up` is a GET for download and a POST for upload on one HTTP/2 connection. `stream-one` stays a single POST. `packet-up` is refused.
- Vision writes the VLESS header and the empty UUID padding frame in one TLS record. Application data ends padding with command end. Command direct is not sent: Xray would read the TCP under REALITY and the site would see ciphertext. When the server sends direct, the client reads the raw socket.
- Ordinary TLS uses the fingerprint from the share link. The macOS status line follows `xhttp`, `reality`, `vision`, or `tcp`, and the route pin is the host after `@`.

Fleet:

- Draining or removing a relay closes its trunk. Clients on that relay fail over. A drained relay cannot open a trunk again.
- Hello carries `controller_pin`, the controller key the client trusts. The node marks members current or old.
- Takeover is the signed controller generation after a standby promotion.
- The panel key roll shows how many connected devices already trust the new key.

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

В Dist: приложение macOS (DMG `macos-arm` и `macos-intel`), CLI Linux/macOS (`loft`, `loft-front`, `loft-probe`, `loft-bench`), нода (tar.gz и .deb amd64/arm64), панель (tar.gz и .deb), Android sideload APK (телефон и TV), Windows portable (`windows-x86-64`, `windows-x86`, `windows-arm64`), контейнер MikroTik. Сборка GitHub заменяет свой каталог из zip следующего релиза. iOS, Sailfish, Keenetic в этот Dist не входят.

1.0.35: Android — с включённой раздельной маршрутизацией приложения больше не вылетают: большие списки давали VPN около 7000 маршрутов, и Android не мог передать такую таблицу приложениям; теперь маршрутов не больше 1200, списки, которые помещаются, не меняются, большие объединяются в более широкие блоки по краям; блок-лист по-прежнему проверяется точно. Протокол loft/1.0.

1.0.34: Android — плавность и стабильность (подвисания на Xiaomi HyperOS): сессия VPN на своём потоке, ежесекундный опрос больше не перечитывает списки и не пересохраняет профили на потоке интерфейса; списки и профили маршрутов — в отдельных файлах с атомарной записью в фоне; изменения настроек сливаются в одну пересборку туннеля, экраны перерисовывают только изменившееся; поток чтения TUN не утекает при пересборке и не выделяет память на пакет; сохранённые подключения пишутся атомарно и не теряются при сбое хранилища ключей; плитка на Android 14+, повторный импорт после поворота, стоп во время рукопожатия исправлены; на TV фокус пульта больше не прыгает. Движок не повторяет запрещённые Android 11+ запросы интерфейсов. Протокол loft/1.0.

1.0.33: Dist — имена GUI `macos-arm`/`macos-intel` и `windows-x86-64`/`x86`/`arm64`, контейнер MikroTik; Android — многострочная вставка ссылки, шит добавления двумя карточками, крупный шрифт на Настройках/О программе/Scan, без порта SOCKS в списке; MikroTik — SOCKS можно выключить, LAN nat или route; нода — аренды overlay после рестарта, TUN через netlink; macOS — грузинский UI; панель — HELO и вход в Roundcube. Протокол loft/1.0.

1.0.32: панель — при удалении почтового домена можно удалить и его DNS-записи, созданные панелью (галочка, включена по умолчанию), сертификат mail.<домен> уходит вместе с доменом; Namecheap — id записей теперь HostId, а не позиция, старые id чинятся при чтении зоны; нода на Go 1.27+ сама включает расширенный CONNECT HTTP/2 (фронты h2 и l3); SHA256SUMS с голыми именами файлов; Android — отчёты о сбоях в GitHub, крупный системный шрифт, значок в безопасной зоне. Протокол loft/1.0.

1.0.31: профили маршрутов — cap 30 lists_inline, CmdLists 13, kinds 4–6, копия оператора, loft://routes; Android — зона Block, локальный DNS, приложения вокруг туннеля, свайп/выбор/отмена; QUIC — BBR или фиксированная скорость через форк a3app/quic-go, TCP остаётся на ядре; Windows — трей Win7 SP1; mux больше не пишет в закрытый канал; панель — изоляция VLAN, сортировка портов, светодиоды; P2P — запечатанное рукопожатие, witness внутри TLS. Протокол loft/1.0.

1.0.30: VLESS — XHTTP stream-up, кадр Vision с UUID в одной TLS-записи с заголовком, на application data command end, а не direct; macOS подписывает сессию xhttp, reality, vision или tcp. Слив или удаление релея закрывает его транк. Hello несёт controller_pin. Takeover — подписанное поколение контроллера после повышения standby. Панель при смене ключа показывает, сколько уже подключённых устройств ему доверяет. Протокол loft/1.0.

1.0.29: прямой фронт может подтвердить clear или AES-256-GCM после Status, GET остаётся ChaCha20-Poly1305; реестры номеров, вопросы безопасности и конфиденциальность; буферы сокета на autotune, unsent 16 КиБ, park потока 8 МиБ; линии mux на живой сессии; tight — четыре HTTP/2; в loft:// authority — нода, edge — URL первого GET; панель — сегменты вкладками, у VLAN свой L3, проверка ping. Протокол loft/1.0.

1.0.28: Dist публикует подписанные GitHub APK, имена файлов с версией; GET вешает лишние фронты в фоне и не цепляет их на CDN; macOS — один GET outbound и `sstp://`; панель — Адаптеры / Внешние каналы. Протокол loft/1.0.

---

## 中文

loft 是跑在 TLS 1.3 里面的覆盖网。应用走本机 SOCKS5 或 TUN。流复用，ChaCha20-Poly1305，外面是普通 HTTPS：GET、WebSocket、HTTP/2、QUIC、HTTP/3。节点前面可以接 CDN。协议 **loft/1.0**。分享 URI `loft://`。

本 Dist：macOS 应用（`macos-arm` / `macos-intel` DMG）、Linux/macOS CLI（`loft`、`loft-front`、`loft-probe`、`loft-bench`）、节点（tar.gz 和 .deb）、面板（tar.gz 和 .deb）、Android 旁路 APK（手机和电视）、Windows portable（`windows-x86-64`、`windows-x86`、`windows-arm64`）、MikroTik 容器。GitHub 版本用下一份发布里的 zip 替换自己的目录。iOS / Sailfish / Keenetic 不在这次包里。

1.0.35：Android — 开启分流路由时其他应用不再闪退：大型列表让 VPN 有约 7000 条路由，Android 无法把这么大的路由表传给应用；现在路由不超过 1200 条，放得下的列表不变，过大的在边缘合并为更宽的网段；封锁列表仍逐包精确检查。协议仍是 loft/1.0。

1.0.34：Android 流畅度与稳定性（小米 HyperOS 卡顿）— VPN 会话跑在独立线程，每秒轮询不再在界面线程重读路由列表或重存配置；路由列表与路由配置移到独立文件，后台原子写入；设置改动合并为一次隧道重建，界面只重绘变化部分；TUN 读线程不再在重建时泄漏、每包不分配内存；已存连接原子写入，密钥库故障不再清空；修复 Android 14+ 快捷开关、旋转后重复导入、握手中停止；电视遥控焦点不再跳动。引擎不再反复尝试 Android 11+ 禁止的网卡查询。协议仍是 loft/1.0。

1.0.33：Dist — GUI 文件名改为 `macos-arm`/`macos-intel` 和 `windows-x86-64`/`x86`/`arm64`，加入 MikroTik 容器；Android — 粘贴栏改为多行、添加连接为两张卡片、大号系统字体下的设置/关于/扫描、连接列表不再写 SOCKS 端口；MikroTik — SOCKS 可关，LAN nat 或 route；节点 — overlay 租约跨重启，Linux TUN 走 netlink；macOS — 格鲁吉亚语界面；面板 — HELO 与 Roundcube 一键登录。协议仍是 loft/1.0。

1.0.32：面板 — 删除邮件域名时可一并删除面板为其创建的 DNS 记录（确认框中的勾选，默认开启），mail.<域名> 证书随域名删除；Namecheap 记录 id 改用 HostId 而非位置，旧 id 在读取区域时修复；Go 1.27+ 构建的节点自行开启 HTTP/2 扩展 CONNECT（h2、l3 前端）；SHA256SUMS 只写文件名；Android — 崩溃报告发到 GitHub、大号系统字体、图标在安全区内。协议仍是 loft/1.0。

1.0.31：路由配置 — cap 30 lists_inline、CmdLists 13、kinds 4–6、运营方副本、loft://routes；Android — Block 区、本机 DNS、隧道周边应用、滑动/多选/撤销；QUIC 经 a3app/quic-go 分叉切 BBR 或固定速率，TCP 仍走内核默认；Windows 托盘 Win7 SP1；mux 不再往已关闭通道写；面板 VLAN 隔离、端口排序、指示灯；P2P 密封握手，witness 走 TLS。协议仍是 loft/1.0。

1.0.30：VLESS 支持 XHTTP stream-up；Vision 的 UUID 填充帧与请求头在同一条 TLS 记录里，application data 用 command end 结束填充，不发 direct；macOS 状态行按 xhttp、reality、vision 或 tcp 标注。排空或删除中继会关掉它的 trunk。Hello 携带 controller_pin。Takeover 是 standby 提升后经过签名的控制器世代。面板换钥时显示已有多少在线设备信任新钥。协议仍是 loft/1.0。

1.0.29：直连前置可在 Status 之后确认 clear 或 AES-256-GCM，GET 仍是 ChaCha20-Poly1305；编号表、安全考虑与隐私；套接字缓冲交给内核 autotune，unsent 上限 16 KiB，流 park 8 MiB；mux 线路挂在活会话上；tight 要四条 HTTP/2；loft:// 的 authority 是节点，edge 只是第一次 GET 的 URL；面板里网段以标签打开，VLAN 自有 L3，有 ping 检查。协议仍是 loft/1.0。

1.0.28：Dist 发布已签名 GitHub APK，文件名带版本；GET 在后台挂额外前置、CDN 上不挂；macOS 单 GET outbound 和 `sstp://`；面板「适配器 / 外部通道」。协议仍是 loft/1.0。

# loft 1.0.22

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

## 1.0.22

Node and protocol (additive Status tails; old clients ignore extra bytes). Protocol stays **loft/1.0**.

- Origin CONNECT is no longer an open TCP proxy. Cap `connect` is off unless `fronts` lists `connect`. Every CONNECT carries 0x02 Cookie `session=` + `X-Auth: 1`. Loopback, link-local, multicast, unspecified, localhost, and `metadata.google.internal` return 403.
- Hold resume parks the mux by the authenticated member plus `instance_id`. A second member with the same instance cannot adopt it.
- `rate_bps` is a per-session token bucket on bulk Data/Dgram (optional Status tail). 0 is off. Pri frames skip the bucket.
- Node reloads `network.conf` and `members.keys` on SIGHUP.

macOS:

- Split routing: ignore entries on imported lists; Tools menu checkbox; Open Loft icon.
- loft-priv asks for the admin password once (setuid), not on every split apply.
- README support wallets (BTC, ETH/USDT ERC-20, TRX/USDT TRC20, LTC, ZEC).

Panel (first Dist packages):

- Live Packages screen, picker, job log, release updates; wizard step 5 installs.
- Sessions survive panel restarts; setup wizard (domain, Let's Encrypt, first member).
- Users: roles with a section × view/change matrix, invitations, API tokens.
- Network and IP: server addresses and routes, upstream channels, exit rules.
- Mail packages (Postfix/Dovecot, Rspamd, Roundcube); node screen (node.conf, relays, CDN, who is online).
- MTProto proxy package (`mtg`) from a pinned GitHub tarball, with share links.

---

## Русский

Оверлей внутри TLS 1.3. Приложения ходят в локальные SOCKS5 или TUN. Потоки в одном mux, ChaCha20-Poly1305, снаружи обычный HTTPS: GET, WebSocket, HTTP/2, QUIC или HTTP/3. Перед нодой может стоять CDN. Протокол **loft/1.0**. Ссылка `loft://`.

В Dist: приложение macOS (DMG arm64 и x86_64), CLI Linux/macOS (`loft`, `loft-front`, `loft-probe`, `loft-bench`), нода (tar.gz и .deb amd64/arm64), панель (tar.gz и .deb). iOS, Android, Aurora, Keenetic, MikroTik в этот Dist не входят.

1.0.22: CONNECT на origin больше не открытый прокси (нужен 0x02, cap `connect` выключен по умолчанию); hold/park по члену и `instance_id`; `rate_bps` на сессию; SIGHUP перечитывает сеть и ключи. macOS: игнор в split, пароль loft-priv один раз, чекбокс в Инструментах. Панель впервые в Dist: пакеты, мастер, пользователи, сеть и каналы, почта, MTProto (`mtg`). Протокол loft/1.0.

---

## 中文

loft 是跑在 TLS 1.3 里面的覆盖网。应用走本机 SOCKS5 或 TUN。流复用，ChaCha20-Poly1305，外面是普通 HTTPS：GET、WebSocket、HTTP/2、QUIC、HTTP/3。节点前面可以接 CDN。协议 **loft/1.0**。分享 URI `loft://`。

本 Dist：macOS 应用（arm64 / x86_64 DMG）、Linux/macOS CLI（`loft`、`loft-front`、`loft-probe`、`loft-bench`）、节点（tar.gz 和 .deb）、面板（tar.gz 和 .deb）。iOS / Android / Aurora / Keenetic / MikroTik 不在这次包里。

1.0.22：源站 CONNECT 不再是未认证的 TCP 代理（需要 0x02，cap `connect` 默认关闭）；hold/park 按成员和 `instance_id`；会话 `rate_bps`；SIGHUP 重载网络和密钥。macOS：分流忽略项、loft-priv 一次管理员密码、工具菜单勾选。面板首次进入 Dist：软件包、向导、用户、网络和通道、邮件、MTProto（`mtg`）。协议仍是 loft/1.0。

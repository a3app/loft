# 跑 loft

[English](guide.md) · [Русский](guide_ru.md) · **中文**

软件 1.0.24。协议 1.0（loft/1.0）。规范：[protocol_zh.md](protocol_zh.md)。结构：[architecture_zh.md](architecture_zh.md)。

这是参考实现。

---

## 1. 编译

Go 1.24 或更新，在仓库根目录：

```bash
make test
make node
make client    # dist/clients/go/loft
make probe     # dist/clients/go/loft-probe
make macos     # dist/clients/macOS/loft.app
make macos-dmg # dist/clients/macOS/loft-*-macos-<arch>.dmg  （Applications 符号链接）
make macos-dmgs # arm64 和 x86_64 两份 DMG（CI）
make dist      # 节点 tarball/.deb + CLI 归档
```

```bash
go test ./...
go build -ldflags "-s -w -X github.com/a3app/loft/internal/version.Version=$(cat VERSION)" -o loft-node ./server/cmd/loft-node
go build -ldflags "-s -w -X github.com/a3app/loft/internal/version.Version=$(cat VERSION)" -o dist/clients/go/loft ./clients/go/cmd/loft
go build -ldflags "-s -w -X github.com/a3app/loft/internal/version.Version=$(cat VERSION)" -o dist/clients/go/loft-probe ./clients/go/cmd/loft-probe
```

`--version` 打印程序名、semver 和协议范围。

`make client` 是 `dist/clients/go/loft`。`make probe` 是 `loft-probe`。macOS 应用还要 `loft-front`：

```bash
go build -ldflags "-s -w -X github.com/a3app/loft/internal/version.Version=$(cat VERSION)" -o dist/clients/go/loft-front ./clients/go/cmd/loft-front
```

---

## 2. 节点角色

**控制器** 存密钥、租约、ACL，以及名册和列表的签名密钥。可以给成员听 443。

**中继** 终结公网 TLS，把明文经干线拷到控制器。IP 用完就扔。成员身份和覆盖网 IP 还在控制器上。

两个角色同一个二进制。角色是 YAML `role: controller` 或 `role: relay`。

---

## 3. 覆盖网

路径：`{dir}/network.conf`（旧的 `{dir}/network.yaml` 还能加载）。没有它节点起不来。

```
network home
name home
enable
ipif overlay 10.8.0.0/24
  gateway 10.8.0.1
  pool 10.8.0.10 10.8.0.220
  mtu 1280
  peers hub
  p2p enable
  dns enable
  dns 10.8.0.1
  dns upstream https://1.1.1.1/dns-query
  dns upstream tls://1.1.1.1
  listen 0.0.0.0 443 front ws
create member alice
config member alice multi enable 4
config member alice key_sha256 <hex>
enable member alice
ip access-list overlay
  permit tcp host 10.8.0.2 host 10.8.0.5 eq 22
```

`dns.servers` 塞进 Status，写到客户端 TUN。打到网关 53 端口的查询（UDP CmdDgram 和 TCP Open）由节点答。`dns.upstream` 是节点怎么取答案：`https://` 是 DNS-over-HTTPS，`tls://` 是 DNS-over-TLS（端口 853），光 IP 是 UDP/53。`quic://` 预留。空 `servers` 就是网关。`enabled: false` 不碰系统 DNS。

成员密钥是 `lt_` 加 48 个十六进制字符（24 字节）。`network.conf` 存 SHA-256。明文在 `{dir}/members.keys`（权限 0600），因为 HMAC 和 HKDF 不能从哈希跑。`keep_keys enable`（默认）时运营方能再读这个文件，再吐客户端 YAML。`keep_keys disable` 只在签发时给 YAML；节点还是要明文文件。

**重载。** `systemctl reload loft-node`（SIGHUP）在不停止节点的情况下重新读取 `network.conf` 与 `members.keys`。条目未变的成员保持连接；被删除、停用或修改的成员会断开，并以新设置重连。客户端在 Hello 时获得的内容（CIDR、网关、MTU、peers、p2p、DNS、路由、VLAN、会话计时；启用 p2p 时的规则）发生变化时，所有成员都会重连，但仍无需重启。无法加载的文件只记入日志，运行中的配置保持不变。`node.conf`、证书与令牌仍需重启。

**出口通道。** 默认情况下成员经服务器自身路由出网。与 `network.conf` 同目录的 `egress.conf`（由 loft-panel 写入）可改走其他通道：

```
channel wg-nl dev wg-nl check ping probe 1.1.1.1 every 10 fails 3   # 内核接口（WireGuard、OpenVPN、ZeroTier）
channel wg-fi dev wg-fi
channel px socks5 192.0.2.40:1080 user u pass "p w" udp proxy check tcp probe example.com:443
channel web http 192.0.2.41:3128 tls      # HTTP 或 HTTPS 代理（CONNECT）
channel office sstp vpn.example.org:443 user office pass x auth mschapv2
prio main wg-nl wg-fi px then direct limit wg-fi 50/10   # 优先级列表：第一个可用通道，然后 direct 或 block；wg-fi 限速 50/10 Mbit/s
prio both wg-nl wg-fi then direct multi  # 多路径：新连接依次分配到可用通道
prio work office then block
rule member bob via work
rule tag staff via main
rule list blocked via main                # node.conf 中 lists 的列表
default via main
```

规则按顺序匹配，然后是 `default`；每条规则指向一个优先级列表（或 `direct`、`block`）。列表先用第一个能承载流量的通道，再用下一个，最后是 `then` 那一步。通道处于 `off`、没有接口、最近 20 秒连接失败或连通性检查不通过时会被跳过：`check ping`（内核接口）或 `check tcp` 每 `every` 秒（10）发一次探测，连续 `fails` 次（3）失败即判定断连；一次成功就恢复。规则仍可以直接指向通道——此时顺序由该通道的 `fail` 链（通道、`direct`、`block`）决定，与旧文件相同。`multi` 让列表变成多路径：每个新 TCP 连接从下一个通道开始（成员到同一主机的 UDP 保持一条路）。`limit 通道 入/出` 为按该列表出去的所有人限制该通道速度，单位 Mbit/s（入是到成员的方向，0 表示不限）。代理可经由（`via`）另一通道。`dev` 把套接字绑定到接口（SO_BINDTODEVICE）；系统需要相应路由规则（`ip rule add oif DEV lookup TABLE` 并在 TABLE 中设默认路由），由 loft-panel 配置。TCP 流与成员数据报遵循规则；ICMP 与发往节点的 DNS 查询直连。节点在 SIGHUP 时重新读取该文件，并每 5 秒写入 `egress-status.json`（状态、字节数、连接数）。

`LOFT_TOKEN` 是节点密钥（伪装、控制器 seed）。客户端用 `members.keys` 里的每成员密钥。`auth shared enable`（YAML 里 `auth_shared: true`）在密钥环空着时把 `LOFT_TOKEN` 当客户端密钥——旧的单令牌覆盖网。环不空就绝不掺节点令牌，标志开着也不行。新安装写 `auth shared disable`，签发一把 `operator` 密钥。

客户端 YAML 带着 `member_id` 和这个成员的 `token`。Hello `member_id` 从对上的密钥绑，不从客户端自称。

| `peers` | 对另一个成员的覆盖网 Open |
|---------|--------------------------------|
| `hub` | Reset `policy_drop`。互联网 Open 不变。成员后面的受管 CIDR 仍拼接。 |
| `mesh` | `p2p` 允许就直连 TCP，否则经节点拼接。 |
| `vlan` | 跟 mesh 一样，只限同一 VLAN。 |

可选命令：`create vlan`、`ip route DST VIA|member ID|tag NAME`（下一跳是覆盖网 IP、成员 id，或带该 tag 的第一个在线/固定成员）、`ip access-list`（Cisco 扩展 ACL，先匹配先用，`permit`/`deny`，`tag NAME` / `group NAME`，`default deny`）、`config member ID tags …`、`session` 定时器（`handshake_ms`、`idle_ms`、`keepalive_ms`、`lease_hold_ms`、`gap_hold_ms`、`ttl_ms`）以及 `session rate_bps`。`ttl_ms` 是活会话上限（0 = 关）。`lease_hold_ms` 是断开后占 IP。`rate_bps` 是成员会话 bulk Data/Dgram 上限，双向（0 = 关）。覆盖：`config member ID rate_bps N`。

```
config member lab ipaddress 10.8.0.5
config member lab tags lab
config member alice tags ops
ip route 192.168.10.0/24 member lab
ip route 10.20.0.0/16 tag lab
ip access-list overlay
  default deny
  permit tcp tag ops tag db eq 5432
  permit ip tag ops 192.168.10.0/24
session ttl_ms 86400000
```

`default deny` 仍放行覆盖网网关（DNS）。没有这一行时 mesh 成员之间仍通。Status 把 CIDR 推给每个成员，VIA 等于自己覆盖网 IP 的那位除外。打向 `192.168.10.5:22` 的 TCP Open 在 lab 成员上拼成 `192.168.10.5:22`；打向覆盖网 IP 的 Open 仍是 `127.0.0.1:port`。直连 P2P 走同一套 ACL（Status policy）。没有 policy 的老 Status 跳过这项检查。

客户端可以设 `p2p: false`，那就只用 hub 拼接。直连 TCP：客户端听着，宣告 `loft.peer:<port>` 加 32 字节 X25519 公钥，Status 带着节点在 TLS 上看到的主机。前言是 LFT（成对 ECDH）。NAT 后面试失败，同一条流走节点。经中继时看到的主机就是中继。客户端大约在 `session_ttl_ms` 的 80% 重新握手。

---

## 4. 控制器

例子：`server/configs/server.yaml`。

```bash
export LOFT_TOKEN='long-secret'
./loft-node -c server/configs/server.yaml -dir /var/lib/loft
```

| 标志 | 意思 |
|------|---------|
| `-c` | YAML：角色、诱饵、前置、名册、TLS |
| `-dir` | `network.conf`、`members.keys`、`controller.seed` |
| `-listen` | 改写监听地址 |
| `-cert` `-key` | YAML 里没写就用 PEM |
| `--version` | 版本 |

| 字段 | 意思 |
|-------|---------|
| `listen` | `:443` |
| `cert` `key` | 节点证书。诱饵+伪装时，loft 客户端看到 dest 形状的叶子。 |
| `token` | 节点密钥（`LOFT_TOKEN`）：伪装、控制器 seed。成员密钥在 `members.keys`。 |
| `get_path` `ws_path` `h2_path` `h3_path` | 前置路径 |
| `get_h2` | GET 的 HTTP/2 连接数，1–4（默认 1）。inflight 总数共用。 |
| `fronts` | 空 = `get`。要 `ws`、`h2`、`quic`、`h3` 就列入。`connect` 是 mux-off 的 HTTP CONNECT 拼接；未列入则关。`l3` 为 HTTP/2 CONNECT 而后 IP 数据报；未列入则关 |
| `decoy` | `host:port` 列表。第一条是节点身份。空 = 关 |
| `decoy_proxy` | `enable`：每个 TCP 拼接先发一行带客户端地址的 PROXY v1。只用于自己的网页服务器（nginx `listen … proxy_protocol`）；公共网站会出错。默认关。不影响 UDP |
| `mask` | 给 loft 客户端 dest 形状的 ECDSA 握手；探测照样拼接 |
| `role` | `controller` |
| `advertise` | 这条前置在名册里的公网 URL |
| `roster_path` | 公开 GET，默认 `/static/v3/manifest.json` |
| `enrol` | `list`：id 必须在 `relays:` 里。`open`：任何 `CapRelay` 干线 |
| `auth shared` | `enable`：`members.keys` 空时把 `LOFT_TOKEN` 当客户端密钥。默认 `disable`。 |
| `relays` | 允许名单：id、url、decoy、fronts、weight、path |
| `lists` | 名字 → 地址（域名、`.suffix`、CIDR） |
| `lists_path` | 公开 GET，默认 `/static/v3/lists.json` |
| `canary` | 路径金丝雀：`fat_hello`、`split_hello`、`pad` |
| `cover_dir` | 静态站点根。空 = 内置页 |
| `replicas` | 未签名的额外 `host:port`，给故障切换 |
| `quic_alpn` | 默认 `h3` |

签名 seed：`{dir}/controller.seed`。公钥给客户端当 `controller_pub`。

活着的名册 = `advertise` 加上在心跳、不是 drain 的中继。三次没打到（大约 30 秒）或 `ctl` 流死了，就把中继拿掉，serial 加一。

路径应当看起来像普通站点的资源或 API。这棵树的默认：`/static/v3/build`、`/api/v1/sync`、`/api/v1/tunnel`、`/static/v3/manifest.json`、`/static/v3/lists.json`。

---

## 5. 中继

例子：`server/configs/relay.yaml`。

```bash
./loft-node -c server/configs/relay.yaml -dir /var/lib/loft-relay
```

| 字段 | 意思 |
|-------|---------|
| `role` | `relay` |
| `id` | `enrol: list` 时必须对上 `relays[].id` |
| `controller` | 枢纽 URL：`wss://host/api/v1/sync` 或 `https://host` |
| `advertise` | 客户端走这个边缘的 URL |
| `decoy` `mask` | 这个 IP 的身份 |
| `token` | 和控制器一样，或者两边都用 `relay_token` |
| `fronts` | 这个边缘提供的前置 |
| `cert` `key` | 这个 IP 的公网 TLS |

中继用 Hello cap `relay` 开干线，然后 `ctl:1`（enrol、beat），每个成员 `hub:1` 或 `hub:2`。

证书、DNS、机器本身带外准备。Enrol 是控制器知道边缘还活着的办法。

---

## 6. 客户端

二进制 `loft`。例子 `clients/go/configs/client.yaml`。

```bash
./dist/clients/go/loft -c clients/go/configs/client.yaml
ALL_PROXY=socks5://127.0.0.1:1080 curl https://example.com
```

本机监听：SOCKS5（`client.yaml` 和 GUI 里 `127.0.0.1:1080`）。CLI `loft` / `loft-front` 没写端口就绑 `127.0.0.1:18080`，好跟开着的客户端并排。`tun: auto` 才有 TUN。`-if en0`（或本机/网关 IPv4）把前置套接字钉在那条路上；`loft-front`、`loft-probe`、`loft-bench` 同一个标志。`loft-front` / `loft-bench` 的 `-h2-max-age` 就是 YAML `h2_max_age_ms`（0 = 留到出错）。

Wi-Fi / LTE / VPN 路径一变：SOCKS 和 TUN 还在，覆盖网 IP 还在，助手进程还在。GET 从新出口换 HTTP/2；sid 和 mux 留下。别的前置走 Park；续上是新的 `0x02` 和同一个 `instance_id`。有 cap `hold` 时，停着的 mux 流在 `gap_hold_ms`（默认 8 秒）里还在，里面的 TCP 不断。Leave 还是会拆会话。

客户端 YAML `pause: starlink` 在已知的 15 秒 LEO 切换窗口附近不发 mux Data。控制帧照发。这是本地的，不是协议字段。

### 覆盖网出站探测

`loft-probe` 经 Loft 查 ICMP、TCP、UDP、DNS、HTTP 和 HTTPS，不改系统路由，不开 utun。ping 通但站点不行，或者默认路由关着的时候用。

```bash
make probe
./dist/clients/go/loft-probe                                          # icmp + tcp + dns + http/https
./dist/clients/go/loft-probe 8.8.8.8                                  # 经覆盖网 ICMP echo
./dist/clients/go/loft-probe -proto tcp 1.1.1.1:443
./dist/clients/go/loft-probe -proto http https://ifconfig.me/ip       # 节点看到的公网 IP
./dist/clients/go/loft-probe -via socks -proto tcp example.com:443    # 活着的 loft SOCKS，不再开会话
./dist/clients/go/loft-probe -c "$HOME/Library/Application Support/loft/profiles/example.yaml" 8.8.8.8
./dist/clients/go/loft-probe -if en0 -c "$HOME/Library/Application Support/loft/profiles/example.yaml" 8.8.8.8
```

`-via mux` 自己开 GET/WS 会话（连 ICMP 也查）。`-via socks` 用活着的 SOCKS5（GUI `127.0.0.1:1080`，调试 CLI `127.0.0.1:18080`；TCP/UDP/HTTP；跳过 ICMP）。`-via auto` 先 YAML，再活着的 SOCKS。

| 字段 | 意思 |
|-------|---------|
| `token` | 成员密钥 |
| `instance_id` | 覆盖网实例。空：YAML 旁边的 `{config}.instance`，第一次跑时创建 |
| `select` | `auto` 或一条出站的名字 |
| `fronts` | 额外过滤。空 = 出站类型（默认 GET） |
| `bootstrap` | 名册清单的 HTTPS URL，认证之前 |
| `controller_pub` | 32 字节 Ed25519（64 hex）。接名册必须有 |
| `tofu` | `true` 在 `controller_pub` 空时钉住第一个有效签名者（默认关） |
| `outbounds` | `type`、`url`、`path`、`cdn`、`fingerprint` |
| `use` | 列表名 → `direct` \| `via` \| `drop` |
| `default` | 没有列表命中时的动作（默认 `via`） |
| `p2p` | `false` 关掉直连覆盖网尝试。直连 TCP 是 LFT（成对 ECDH） |
| `lists_url` | 改写地址列表的 GET |
| `listen.socks5` | 绑定地址 |
| `listen.tun` | 空 = 关；`auto` 建 utun |
| `listen.tun_addr` | TUN 前缀；地址是 Status 给的覆盖网 IP |
| `listen.kill_switch` | TUN 死了还按住系统默认路由 |
| `listen.http` | LAN 运营页（MikroTik `:8090`） |
| `listen.http_pass` | 该页的可选密码 |
| `listen.bind` | 和 `-if` 一样；命令行 `-if` 优先 |
| `listen.uplink` | 容器出口用的 RouterOS WAN 名（`ether1`、`lte1`） |

出站字段：`name`、`type`（`get` \| `ws` \| `h2` \| `quic` \| `h3`）、`url`、`path`、`cdn`（`yandex` \| `cloudflare` \| 空）、`fingerprint`（`chrome` \| `firefox` \| `go`）、`header_budget`、`inflight`、`h2_max_age_ms`、`insecure_skip_verify`。

没写 `header_budget` 就是 16 KiB HTTP 头（Yandex CDN 配置钉死 12 KiB）。没写 `inflight` 在这棵树是 8（协议范围 2–8，规范默认 4）。没写 `h2_max_age_ms` 就是 0：GET 的 HTTP/2 一直留到出错。大于 0（下限 15 秒，上限 1 小时）会换这条 TCP；mux 和 GET 会话 id 不变。GET 上行是 Cookie `session=`；第一次 GET 是标签 `0x02`，会话 id 已经在路径里。覆盖网 ICMP 是打到 `dst:1` 的 CmdDgram；节点答网关，并发互联网 echo。

客户端不选诱饵 SNI。节点宣告。CDN 前置保持 CDN 主机名。

Yandex Cloud CDN（源站组、资源、缓存关、GET 12 KiB）：[yandex-cdn_zh.md](yandex-cdn_zh.md)。

`bootstrap` 必须是有真证书的主机或 CDN。打到诱饵 IP 且没有会话 HMAC 的 GET 拼接到源站。

名册：`GET /static/v3/manifest.json` → `{"v":1,"p":"<base64url>"}`。文件里没有成员密钥。握手之后 Status 更新名册。失败时客户端试前置、未签名的 `replicas`，再试名册。

### 地址列表

控制器发布有名字的集合。客户端把名字映射到动作：

```yaml
# server.yaml
lists:
  banks: [example-bank.test, .bank.example]
  video: [youtube.com]
  ads: [ads.example]

# client.yaml
default: via
use:
  banks: direct
  video: via
  ads: drop
```

如果主机命中几张映射过的列表：drop，然后 direct，然后 via。SOCKS 匹配主机名。TUN 匹配 IP（CIDR）。拉取：`GET /static/v3/lists.json`。Status 带着 serial 和哈希。

列表名是运营方的词。协议把它们当不透明字符串。

---

## 7. 诱饵

未授权 TCP（任意 SNI、坏掉的 hello、明文）拼接到 `decoy:`。同一端口的 UDP 拼接到同一源站，但 Initial 带 loft 标记的 loft QUIC 除外：quic 和 h3 前置在 decoy 后面照常工作（从这一版起的客户端）。loft ClientHello（有效 `session_id`）由节点处理。`mask: true` 时这条路出示 dest 形状的 ECDSA 叶子；探测还是看到源站。`cdn_peers` 列出允许在 own SNI 上不带 sid 拉源站的 CIDR。源站是本机 nginx 时，`decoy_proxy enable` 让它看到真实客户端地址（日志、限制、面板登录防护）。

`session_id` 是 32 字节。sid v3 是和节点 X25519 做 ECDH 再 HKDF（`mask_pub` / 分享链接 `pbk`）。sid v2（回退）是成员密钥上的 HKDF。偷到的成员密钥只标 v2 hello。

例子 YAML 里的源站主机名是例子。选公网站点这条路径不愿意丢掉、节点又能拼接其 TLS 的源站。它们不编进协议。

---

## 8. 带外

虚拟机、DNS、公网证书、二进制更新，都不在协议里。线上走 enrol、beat、slice、drain、名册和列表。

典型部署：

1. 给公网名字签发证书（或在 CDN 上终结 TLS）。
2. 写 `network.conf`，用 `-dir` 启动控制器。
3. 在不是诱饵拼接 IP 的主机上公布 `controller_pub` 和 `bootstrap` URL。
4. 启动中继，`id` 对上 `relays:`。
5. 给成员一把密钥和一份 `client.yaml`（或 `.loft` 文件）。

---

## 9. 故障

| 现象 | 查 |
|---------|--------|
| `token is required` | `LOFT_TOKEN` 或 `token:` |
| `network disabled` | `network.conf` 里的 `enable` |
| TLS 挂住 | 诱饵 SNI、伪装、证书、`session_id` |
| 清单是别人的 HTML | bootstrap 打到没有 HMAC 的诱饵 IP |
| 中继 `enrol_no id` | `enrol: list` 时 id 不在 `relays:` 里 |
| `auth_already_online` | 另一个实例占着密钥；开 `multi` 或停掉另一个 |
| `auth_limit` | `multi_limit` |
| `policy_drop` | `peers: hub` 下的覆盖网 Open，或 ACL drop |
| SOCKS 通，TUN 没声 | `tun: auto`、权限、排除的出站主机 |
| TUN 死后系统默认路由没了 | `kill_switch`；恢复路由或重连 |
| GET 上 ping 大约 1 秒 | 空闲 GET 节奏是 `keepalive/2`（下限 1 秒）；重连让客户端在 Dgram 之后能追上 |
| 上传卡住 / TLS bad record | 没写 GET `inflight` 以前默认 1；这棵树现在默认 8 |

---

## 10. Debian

节点在 Debian 主机上：[server/debian/README_zh.md](../server/debian/README_zh.md)。

```bash
make dist-node
# 把 dist/node/linux-amd64/loft-node-<version>-linux-amd64.tar.gz 拷到主机
tar -xzf loft-node-*-linux-amd64.tar.gz
cd loft-node-*-linux-amd64
sudo ./install.sh --unattended
```

中继：

```bash
sudo ./install.sh --role=relay --id=ru1 \
  --controller=wss://controller.example/api/v1/sync \
  --advertise=https://203.0.113.10
```

Debian 包：`make dist-node`，然后 `dpkg -i dist/node/linux-amd64/loft-node_*_amd64.deb`。

安装脚本创建用户 `loft`、`/etc/loft/loft.env`（令牌）、自签证书、systemd unit，以及 `/root/loft-client.yaml`。令牌留在环境文件里。`loft-node -print-pub` 打印 `controller_pub`。

---

## 11. 测试

```bash
make test
go test ./internal/wire ./internal/crypto ./internal/mux ./internal/netconf ./internal/transport
```

线协议测试覆盖帧、Hello/Status、名册篡改、列表、机群。传输测试覆盖诱饵 session_id、拉名册、覆盖网策略。TCP loopback 上的伪装在部分主机上可以跳过。`make test` 还跑 `server/debian/install_os_test.sh`。

---

## 12. GitHub Actions

在 `main` 上 push 和 pull request 跑 `make test`。

标签 `vX.Y.Z` 必须和根目录 `VERSION` 一致。这个标签会编：

- 节点 tarball 和 `.deb`（linux amd64、arm64）
- CLI 归档（`loft`、`loft-front`、`loft-probe`、`loft-bench`，linux/darwin，amd64/arm64）
- macOS DMG，ad-hoc 签名：`loft-*-macos-arm64.dmg` 和 `loft-*-macos-x86_64.dmg`（`BUMP=0`，CI 不改写 `clients/macOS/VERSION`）。跑的是 Apple Silicon；Intel 是 Swift/Go 交叉编译（`ARCH=x86_64`）。

然后发 GitHub Release。Dist 还会把标签拉进 [proxy.golang.org](https://proxy.golang.org)，并请求 [pkg.go.dev](https://pkg.go.dev/github.com/a3app/loft)，让模块页跟上这次发布。同名文件还会不带版本上传（`loft-macos-arm64.dmg`、`loft-linux-amd64.tar.gz`，…），好让 [README 下载链接](../README_zh.md#下载) 走 `/releases/latest/download/…` 指到当前标签。同一标签再跑一遍工作流会替换文件。没有标签时，Actions → Dist → Run workflow 只上传产物。

本地打包：`make dist`。本地打 DMG 且不抬 macOS 市场版本：`make macos-dmg BUMP=0`。Developer ID 和公证留在有钥匙串的 Mac 上；不在 Actions 里。

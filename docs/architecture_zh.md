# 参考节点的架构

[English](architecture.md) · [Русский](architecture_ru.md) · **中文**

软件 1.0.32。协议 1.0（loft/1.0）。规范：[protocol_zh.md](protocol_zh.md)。运营：[guide_zh.md](guide_zh.md)。第一次读代码：[code-tour_zh.md](code-tour_zh.md)。

本文描述本仓库的 Go 树。第三方实现可以换目录结构，照样走协议。

---

## 1. 简要

参考节点是一个二进制 `loft-node`，两个角色：控制器和中继。参考客户端是 `loft`（CLI：SOCKS5 和可选 TUN）。`loft-front` 是同一条会话，没有改系统路由的 CLI 标志；macOS 菜单栏应用把它当助手拉起。`loft-probe` 经会话查 ICMP/TCP/UDP/DNS/HTTP。共用代码在 `internal/`。

---

## 2. 树

```
docs/                 协议、架构、指南
  brand/              标志和图标
internal/
  wire/               帧, Hello, Status, 名册, 列表, 机群, caps, 码
  crypto/             HKDF, ChaCha20-Poly1305, Status 之后可选 AES-256-GCM 或 clear, auth 0x02 + X25519, Ed25519 seed
  identity/           访问密钥 lt_ + 48 hex; SHA-256 在 network.conf; 明文在 members.keys
  mux/                流, 反压, CmdDgram, CmdLeave
  netconf/            network.conf, 租约, ACL, VLAN, DNS, 会话定时器
  transport/          前置, 诱饵, 伪装, 中继干线, 覆盖网, p2p, DNS 桩, 拉名册
  config/             client.yaml, server.yaml
  cdn/                边缘配置（Yandex, Cloudflare）；不是 caps。运营方：docs/yandex-cdn_zh.md
  version/            程序名, semver, 协议范围
server/
  cmd/loft-node
  origin/             掩护页, GET, WebSocket, HTTP/2 CONNECT, 公开名册/列表
  configs/            server.yaml, relay.yaml
  debian/             安装脚本, systemd unit, .deb
  panel/              loft-panel 网页面板（规划与原型：panel/design）
clients/
  go/cmd/loft         CLI：SOCKS5, TUN, kill-switch
  go/cmd/loft-front   会话助手（macOS bundle；Sailfish RPM libexec）
  go/cmd/loft-probe   覆盖网 ICMP/TCP/UDP/DNS/HTTP，不改系统路由
  go/cmd/loft-bench   GET/WS 吞吐，对照 macOS connect 路径
  go/tools/wsprobe    一次性 GET 握手（手动；不进 dist）
  macOS/Sources       Swift 5 + AppKit 菜单栏；拉起 loft-front
  macOS/pack          DMG（Applications 符号链接）、签名、entitlements
  macOS/design        UX 规范和模型
  macOS/tests         RFC 8439 AEAD 向量（make -C clients/macOS test-crypto）
  Keenetic/           Entware OpkgTun 客户端（loft-keenetic，LAN :8090）
  MikroTik/           RouterOS 7 容器（镜像 + .rsc；先 SOCKS；不进 Dist）
  iOS/                Packet Tunnel（薄宿主 + appex；gomobile xcframework；Apple TV 在 PLAN 第 6 阶段）
  Android/            VpnService（薄 Java 宿主；gomobile AAR；手机和电视 flavor）
  Sailfish/           ConnMan 覆盖网（QML 宿主 + loft-connman 插件 + loft-front；Aurora 是 OMP RPM）
  mobile/             iOS/Android 共用 Go 引擎（loft, SSTP, VLESS）
lab/
  devstand/           笔记本上的真实面板与 adm（假 systemctl）；make panel-dev
```

`wire` 是协议。`transport` 是这棵树怎么扛它。`cdn` 是部署配置：某个命名边缘的头顺序和 SNI。线上它不能当能力出现。

---

## 3. 会话

成员会话：拨号、auth blob、Status，然后 mux。

```
client.Holder
    Supervise(cfg)                 轮换出站和前置
        Open(target)               get | ws | h2 | quic | h3 | l3
            TLS 1.3 (uTLS)
            auth 0x02 (nonce || eph_c || HMAC)
            Hello MemberCaps（没有 CapRelay；loft-node 中继上有 ExtraCaps）
            eph_n || AEAD(Status)
            mux Open / Data / Close / Dgram / Leave
```

`Supervise` 优先 GET。GET 的 Status 里非空 `ticket` 就是会话标识。Status `fronts` 列出本节点接受的 mux 端点（name、path、可选 URL）；客户端按这些路径 attach，并应把地图写回已保存的配置。客户端可用 `0x04` 挂上 `ws` / `h2` / `quic` / `h3`（用会话密钥做 HMAC，不再发第二次 `0x02`）。GET 留着。Mux 大宗走挂上的前置（`CmdFront`）；那条前置死了，大宗回到 GET，同一密钥、mux、序号。`l3` 和 `connect` 不 attach。

没有可 attach 的 GET 会话时，`select: auto` 下 `Supervise` 按列表顺序试配置好的出站。第一次 Status 之后只留前置的交集。失败就换下一条还在的前置，再试未签名的 `replicas`，再试签过的名册。节点省略 `fronts` 只宣告 GET；`ws`、`h2`、`quic`、`h3`、`l3` 要显式列出。`l3` 为 HTTP/2 CONNECT 而后 IP 数据报（无 mux）；TUN 直接拷包，不走用户态 TCP 栈。

`WatchPath` 大约一秒采四次默认 IPv4 出口。GET 上 Wi-Fi / LTE / VPN 地址一变就换 HTTP/2 套接字；sid、mux、seq 留下（不做 `0x02`）。挂上的前置掉了，大宗回到 GET，不做 Park。单独的 `ws` / `h2` / `quic` / `h3`（没有 GET ticket）换路径则 Park，再用新的 `0x02` 和同一个 `instance_id` 续上。SOCKS5 和 TUN 还绑着（`Holder`）；覆盖网 IP 不变。有 cap `hold` 时，Park 让 mux 流在 `gap_hold_ms`（默认 8 秒）里还在，里面的 TCP 不 RST。Park 之后的 GET 握手按 `instance_id` 捡起这条 mux（新的 HTTP sid，同一条 mux）。`loft` 和 `loft-front` 都跑这个循环。

拉名册要 `controller_pub`。`tofu: true` 钉住第一个有效签名者，写到 `{instanceDir}/controller.pub`。公钥空着又没这个标志，名册就不用；静态出站照样拨。

里面 AEAD 的 nonce 对每个会话密钥唯一（32 位前缀 + 64 位计数器）。用同一把密钥 park，计数器接着走。新的 `0x02` 从 X25519 派生新密钥；HKDF 的 ikm 是 DH 密钥。Status 之后，直连前置可以不用内部 AEAD，或用 AES-256-GCM；`get` 仍是 ChaCha20-Poly1305。握手的 Seal(Status) 始终是 ChaCha20-Poly1305。

客户端不选诱饵 SNI。Status、名册或探测证书给。CDN 出站保持 CDN 主机名。

本机入站（SOCKS5、TUN）调 `Holder.Open(host:port)` 或发数据报。覆盖网目的地由节点上的 `NetSwitch` 解析。Status `dns` 是解析器 IP 列表；节点用 `dns.upstream`（DoH、DoT 或 UDP）答 `gateway:53`。Status 尾巴 `ip6` / `gateway6` 可选，没有就是只有 IPv4。

---

## 4. 节点角色

### 4.1 控制器

存着：

- 成员密钥：`network.conf` 里是 SHA-256；明文在 `{dir}/members.keys`。`auth shared enable` 只在密钥环空着时才把 `LOFT_TOKEN` 当客户端密钥。
- 租约和覆盖网 IP
- ACL 和 VLAN
- Ed25519 seed `{dir}/controller.seed`
- 活着的名册和地址列表

可以给成员听 443。中继干线必须听。

签名 seed 第一次启动时创建。公钥给客户端当 `controller_pub`。参考客户端不会 TOFU 未知签名者，除非 `tofu: true`。

### 4.2 中继

终结公网 TLS。用 Hello cap `relay` 向控制器开一条干线。这条会话上：

- `ctl:1` — enrol、beat、slice、drain
- 每个成员 — `hub:1`（HTTP/1.1）或 `hub:2`（HTTP/2）

中继把 TLS 之后的 HTTP 拷到 `hub:1` / `hub:2`。里面的 mux AEAD 和 FinishAuth 留在控制器。中继不解覆盖网帧。不发租约。不存成员密钥。覆盖网 IP 在控制器上分配。

中继 IP 用完就扔。从名册拿掉会 bump serial。已经拿到 Status 的客户端会话接着跑，直到失败，再拉新名册。

### 4.3 源站

`server/origin` 是公网路径看到的 HTTPS 应用：

- 掩护页（`cover_dir`，或内置页）
- GET 前置、WebSocket、HTTP/2 CONNECT
- 公开名册和列表 JSON

诱饵拼接的探测到不了这个处理：HTTP 之前就转到配置的源站。

### 4.4 Debian 主机

systemd unit、安装脚本和 `.deb`：[server/debian/README_zh.md](../server/debian/README_zh.md)。令牌在 `/etc/loft/loft.env`。覆盖网文档在 `/var/lib/loft`。进程用户 `loft`，绑 443 用 `CAP_NET_BIND_SERVICE`。

---

## 5. 诱饵和伪装

`transport.WrapDecoy` 偷看 TCP ClientHello。

```
Accept
  read ClientHello（或剩下的字节）
  if valid session_id and own SNI
      节点证书（advertise 上的成员，sid 之后的 CDN）
  else if own SNI from cdn_peers
      节点证书（CDN 拉源站）
  else if session_id = sid v3，或伪装关闭时的 sid v2
      if mask ready → dest 形状的 ECDSA 叶子 + 记录填充
      else            → loft HTTP 处理
  else
      TCP 拼接到诱饵源站（SNI 匹配，否则主源）
UDP
  decoy on → 数据报拼接到 dest
  decoy off → loft QUIC/H3
```

拼接到哪是节点配置。客户端自己发现。YAML 里的主机名是例子，不是协议常量。

伪装签发一张 ECDSA P-256 叶子，抄源站身份，用 `ecdsa_secp256r1_sha256` 签 CertificateVerify。序列号绑在和节点 X25519 的 ECDH 上。ClientHello 提供 X25519MLKEM768（`0x11EC`）时，ServerHello 选择该群组；叶子仍在 WebPKI 之外。自身 SNI 仍接受 sid v2，并出示节点证书。探测、诱饵 SNI 上的 sid v2，以及 10 分钟内重复的 TCP `client_random`，都拼接。伪装节点上的 QUIC Initial 只在 v3 标记下准入。应用记录可以跟源站密文长度。

---

## 6. 覆盖网结构

`transport.Overlay` 把每条准入会话绑到一个覆盖网 IP。对网段 CIDR 里面的地址做 Open，按 `peers` 分类：

| 模式 | 动作 |
|------|--------|
| hub | Reset `policy_drop` |
| mesh | 拼接到对端的 mux 会话，Open `127.0.0.1:port` |
| vlan | VLAN 对上就 mesh |

TCP Open 和覆盖网 ICMP 走 `Network.Allow`（tag、CIDR、VLAN、`default deny`）。`ip route DST VIA|member ID|tag NAME` 是成员后面的 CIDR：Open `host:port` 带着原来的 target 拼到那个成员。Status `routes` 列出这些 CIDR，via 是解析后的覆盖网 IP；VIA 那位不在名单里。互联网 Open（CIDR 和这些路由外面）不变。直连 P2P 用 Status `policy` 走同一套 Allow。`session ttl_ms` 关掉活会话；`lease_hold_ms` 在断开后占着覆盖网 IP。

接收端客户端接这个 Open，拨 target。覆盖网 IP 拼接是 `127.0.0.1:port`。Routed 拼接是成员主机栈上的 `host:port`。

可选 P2P：客户端听着，宣告 `loft.peer:<port>` 加 32 字节 X25519 公钥，Status 带着 TLS 看到的主机、端口和公钥。直连 TCP 先做加密握手（新生成的 X25519 密钥、基于与对方公钥的成对 ECDH 的证明、随机填充，没有固定字节），数据用 ChaCha20-Poly1305 封装（`p2pseal.go`），Status 带着 policy 时再走和 hub 拼接同一套 ACL。失败就回 hub 拼接。客户端 `p2p: false` 就不听。seed 在 `{instanceDir}/p2p.seed`。

---

## 7. 地址列表

控制器发布有名字的集合。客户端把名字映射到动作（`use:`）。几张列表都命中时的顺序：drop，然后 direct，然后 via。没命中的默认 `via`。

SOCKS 匹配请求的主机名。TUN 拿目的 IP 对 CIDR。域名种类对裸 IP 套接字不管用。

Status `lists_serial` 或 `lists_hash` 比缓存新时才拉列表。签名和名册同一把 Ed25519。

---

## 8. 本机入站

| 监听 | 默认 | 说明 |
|----------|---------|-------|
| SOCKS5 | `127.0.0.1:1080`（GUI / `client.yaml`）；`127.0.0.1:18080`（`loft` / `loft-front` 没写端口时） | RFC 1928。`dgram` 开着就有 CONNECT 和 UDP ASSOCIATE。 |
| TUN | 关；`tun: auto` 建 utun | gVisor 用户态栈。UDP 走 CmdDgram。ICMP echo：自己本地回；覆盖网和互联网目的地走 Dgram `:1`（节点发互联网 echo）。Darwin 在 ICMP 上 NAT 假地址 `169.254.8.1` ↔ 覆盖网 IP。 |
| kill-switch | `listen.kill_switch` | 最后一条 TUN 死了，系统默认路由还是撤着，直到用户自己恢复 |

TUN 排除 loft 会话的出站主机（CDN、节点），免得隧道把自己的路吞了。

macOS 菜单栏客户端是 Swift 5 + AppKit，在 [`clients/macOS/Sources/`](../clients/macOS/Sources/)。UX：[`clients/macOS/design/DESIGN_zh.md`](../clients/macOS/design/DESIGN_zh.md)。活着的会话是应用包里的 `loft-front`（`Contents/MacOS/loft-front`），不是进程内的 `GETFront.swift`。这个助手的 Hello 是 Go 客户端（GET 和 WS）。设计文档里的路由保持、故障切换和分流列表是机器策略，坐在 Go CLI 标志上面。

---

## 9. 故障切换

会话死了之后试的顺序：

1. GET 还在、挂上的前置死了：大宗回到 GET（同一 ticket，不再 `0x02`）。
2. 交集里剩下的前置，同一条出站。
3. `select: auto` 顺序里下一条出站。
4. Status 里未签名的 `replicas`。
5. 签过的名册条目，按权重，跳过 drain。

重连定时器来自 Status：`reconnect_min_ms`、`reconnect_max_ms`、`reconnect_fast_n`、`reconnect_fast_for_ms`。先快试，再指数退避。

副本接新的 `0x02`。Hello `instance_id` 保住覆盖网租约。

Mux `Pull` 先抽 Open/Ping/Pong/Dgram/Leave 和小 Data（≤ 4 KiB），再抽大块 Data/Pad，免得整段下载卡住 ICMP、TCP ACK 和 Cloudflare loaded-ping。Close/Reset 留在 Data 队列，优雅关闭不能跳过剩下的字节。Data 队列 48 帧。WS 预取 2×48 KiB；控制帧自己有 8 KiB 消息。

`session rate_bps`（网络默认，`config member ID rate_bps` 覆盖）是该成员会话 bulk `CmdData`/`CmdDgram` 的令牌桶，双向。0 为关。控制和小帧不整形。Status `rate_bps` 宣告上限；节点执行。中继干线不限。

---

## 10. 并发

每个成员实例一条 mux 会话。流是带有界接收缓冲的 goroutine。mux 载荷 ≤ 16384；`Stream.Write` 和 `EncodeBatch` 切开 CmdData，免得 32 KiB 的 `io.Copy` 把帧丢掉。

GET 前置有有界 inflight 窗口（协议 2–8，默认 4；这棵树默认 8）。客户端泵是唯一的上行 Puller，按 Pull 顺序发 GET `seq`。节点按 seq 应用上行；只有按序的 GET 才 Pull mux 下行，免得丢掉的 HTTP 响应把已经从 mux 拿走的帧弄丢（YouTube/Speedtest TLS）。空闲 GET 节奏是 `keepalive_ms / 2`（下限 1 秒），抖动 ±⅓。Open/很小的 Data 等 15 ms，数据报等 80 ms，好让 ICMP 搭同一条 body。这棵树通用 GET `header_budget` 是 16 KiB HTTP 头（base64url+AEAD 之后解码 mux 大约 12 KiB，Cookie Z85 之后大约 13 KiB）；Yandex 配置钉死 16 KiB encoded。Status `h2_conns`（节点 `get_h2`，1–4）是额外的 HTTP/2 连接，当第二个 header-budget 信封；inflight 还是总数。可选客户端 `h2_max_age_ms`（0 = 留到出错）会换这条 TCP；mux 和 GET sid 不变。本机出口变更同样换 HTTP/2 池（不再做一次 `0x02`）。上行已经有 ≥ 2 KiB CmdData 的 GET 把 body 限制在 96 KiB，好让 Cookie 槽回收；只下载还是 256 KiB。

中继干线：每个中继进程一条 mux 会话，很多 `hub:*` 流。

`Supervise` 每个客户端进程一个循环。多个 YAML 配置（macOS 多连接）是多个进程或多个 Holder。那不是 cap `multi`。cap `multi` 是一把成员密钥上多个 `instance_id`。

---

## 11. 外面的东西

虚拟机、公网 DNS、公网证书、二进制更新，都不在这个协议里。systemd unit、POSIX 安装脚本和 `.deb` 在 [`server/debian/`](../server/debian/)。线上走 enrol、beat、slice、drain、名册和列表。运营方签发每成员密钥，写 `network.conf` 和 `members.keys`。

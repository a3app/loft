# loft: 跑在 HTTPS 上的覆盖网

[English](protocol.md) · [Русский](protocol_ru.md) · **中文**

**loft** 是覆盖网，在线路上占的位置和普通 HTTPS 一样。线协议是 **loft/1.0**。

**协议 1.0。** 第三方按本文实现，就能和 loft 节点对话。本仓库的 Go 树是其中一种实现。许可 [Apache-2.0](../LICENSE)。

软件版本（`VERSION`，现在 1.0.22）和协议版本无关。握手两边都带。关键词 **MUST**、**SHOULD**、**MAY** 按 RFC 2119。Hello 和 Status 尾巴上不认识的字段 MUST 忽略。

---

## 摘要

覆盖网要是握手长得特别，或者目的地址独一无二，真实路径上就不稳：CDN、反向代理、TLS 中间盒等的是普通 HTTPS。loft 在线路上占的位置和普通 HTTPS 一样：443 上的 TLS 1.3，浏览器模样的 ClientHello，应用数据 CDN 可以终结。里面的流做多路复用，再用 ChaCha20-Poly1305 封住。节点 MAY 出示真实 HTTPS 源站的身份；没带访问密钥的探测拼接到这个源站，扫描器和健康检查看到的是网站。覆盖网身份（密钥、租约、活着的前置名单）住在控制器上。中继是可换的 TLS 边缘。客户端拉一份签过名的前置名册和有名字的地址列表，再把这些列表对上本机路由。同一网络的成员 MAY 在 hub 上隔离、经节点做 mesh，或允许直接打 TCP。典型部署：从外面进实验室局域网、管一机群、站点互联。

---

## 1. 引言

加密隧道在生产路径上会以两种方式挂掉。握手长得特别，TLS 中间盒或 CDN 等的是浏览器，直接丢。隧道打向独一无二的目的 IP，地址不在 allowlist 上，也丢。对实验室或远程接入的覆盖网，这两种都是运营故障。

loft 把公网路径做成普通 HTTPS。里面的协议就一套。承载它的路径（GET、WebSocket、HTTP/2 CONNECT、QUIC、HTTP/3 CONNECT）按部署选，MAY 改而不升协议版本。客户端发出的 TLS 名字由节点定。没有访问密钥的探测，和配置好的源站完成握手。

第二笔账是运营。地址到不了了，只要客户端已经知道另一条前置、覆盖网地址还是同一个，换起来便宜。控制器签一份前置名册。中继登记、发心跳，沉默了就出名册。访问密钥从不出现在这份文档里。

本文规定线协议。参考节点怎么编译、怎么运营，见 [guide_zh.md](guide_zh.md)。参考树怎么摆，见 [architecture_zh.md](architecture_zh.md)。

---

## 2. 构造

```
application  TCP / UDP     (SOCKS 和 TUN 是本机的)
    mux      Open Data Close Reset Ping Pong Pad Dgram Leave
      AEAD   ChaCha20-Poly1305
        front  get | ws | h2 | quic | h3
          TLS 1.3
            TCP/443 or UDP/443
```

TLS ClientHello SHOULD 做成当下浏览器那样（密码套件、扩展、JA3/JA4）。CDN 厂商名是部署配置。它们 MUST NOT 出现在协议能力里。

会话有三层：

1. **TLS。** 公网。CDN、负载均衡或源站 MAY 终结它。
2. **Auth 和 AEAD。** 内部。出示访问密钥证明成员身份。ChaCha20-Poly1305 把 mux 藏起来，TLS 终结那边看不到。
3. **Mux。** 客户端和节点之间的流和数据报（再经节点，到互联网或其他成员）。

中继干线上预留的 mux 目标：`hub:1`（HTTP/1.1）、`hub:2`（HTTP/2）、`ctl:1`（机群控制）。覆盖网宣告：`loft.peer:<port>`。

Hello、Status 和多数机群记录的字符串字段是 `u8 length || bytes`，最多 64 字节。URL 和列表值是 `u16be length || bytes`，最多 255 字节，除非某节另有说明。整数是大端。

---

## 3. 传输

节点在 Status `caps` 里宣告开着的前置。客户端在 Hello `caps` 里宣告它会用的前置。第一次 Status 之后，客户端 MUST 只用交集，按自己配置的顺序，当前这条挂了 MUST 切到剩下的下一条前置。

两边都开了全部前置时，默认尝试顺序：`get`、`ws`、`h2`、`quic`、`h3`。

### 3.1 诱饵拼接

loft 的 ClientHello 有 32 字节 `session_id`。节点两种构造都收，先试 v3。

**sid v3。** 令 `P` 为节点的静态 X25519 私钥（节点密钥的 HKDF-SHA256，info `"loft-mask-x25519"`），`X` 为 ClientHello 的 X25519 `key_share`，`R` 为 32 字节 `client_random`，`S` 为小写 SNI：

```
shared = X25519(P, X)
session_id = HKDF-SHA256(ikm=shared, salt=R, info="loft-sid-v3" || 0x00 || S, L=32)
```

对应公钥是 Status / 名册 `mask_pub`（32 字节），MAY 出现在分享链接 query `pbk`。要标出这类 ClientHello，得有 `P`。偷来的访问密钥标不出来。

**sid v2（回退）。** 令 `token` 为访问密钥：

```
HKDF-SHA256(ikm=token, salt=R, info="loft-sid-v2" || 0x00 || S || X, L=32)
```

客户端还没拿到 `mask_pub` 时发 v2。有 `token` 就能认出 v2 ClientHello。绑在 `R`、`S`、`X` 上，HMAC(`token`, `random`) 那种配方对不上。

别的 TCP ClientHello 拼接到节点配好的源站。第一个源站是节点的身份。节点源站里 SNI 对上的就用那个；否则用主源站。源站主机名是配置。它们 MUST NOT 作为协议常量出现。

没有有效 `session_id` 时，这个字段是 32 字节串，形状和 Chrome 的兼容 `session_id` 一样。直连路径上客户端 MUST 把节点的主诱饵主机名当 SNI 发出。这个名字从 Status、名册，或探测会看到的证书里学到。Status 之后对不上 MUST 用宣告的名字重连。CDN 前置继续用 CDN 主机名。

### 3.2 诱饵伪装

伪装开着时，loft ClientHello 完成 TLS 1.3 时戴着 dest 形状的证书：ECDSA P-256 叶子，抄源站的 Subject、SAN 和有效期。叶子序列号是

```
HKDF-SHA256(ikm=shared, salt=R, info="loft-cert-serial", L=16)
```

`shared` 是 sid v3 的 ECDH。CertificateVerify 是这张叶子上的 `ecdsa_secp256r1_sha256`。知道 `mask_pub` 的客户端 MUST 核序列号绑定和 ECDSA 验签。应用记录 SHOULD 填充，让密文长度跟着从源站采到的样式走，包括采过的头几条之后。Status `mask_pub` 和名册 `mask_pub` 带着 32 字节 X25519 公钥。空表示伪装关着。探测照样拼接。只做拼接的实现，和只拼接的节点仍然兼容。

### 3.3 WebSocket (`ws`)

TLS，然后 HTTP upgrade。路径是节点本地的（默认 `/api/v1/sync`）。客户端第一条消息是明文 auth blob。节点第一条是 AEAD(Status)。后面是 AEAD(mux 批次)。空批次 MUST NOT 发送。关掉套接字就结束会话。客户端 SHOULD 在关之前在 mux 批次里发 CmdLeave。

### 3.4 GET (`get`)

给只转发 GET 的边缘。HTTP/2。路径长得像资源：`{base}/{session}/{seq}.js`。

第一个 GET：头 `X-Auth: 1`，Cookie `session=<base64url(auth blob)>`。响应 200，`X-Session`，`Cache-Control: private, no-store`，体是 AEAD(Status)，AAD = 会话 id。

后续 GET：`X-Request-Id: base64url(AEAD(mux batch))`，AAD = 会话 id，体是 AEAD(mux 批次)。节点 SHOULD 在体前面加 CmdReport（`applied` = 最后一段连续收下的 GET seq）。客户端收到了 MUST 停止重试上行 seq ≤ `applied`。节点 MAY 也在 Cookie `session=` 里收这条上行（老客户端）；那时没有 `X-Auth: 1`。认证失败 MUST 回 404 带 HTML。上行 MUST 塞进一个头（Status `header_budget` 是编码后 Cookie 字节；省略时默认 16 KiB HTTP 头）。Status `uplink_enc` 1 是 `~` 加上 `u16be length || AEAD blob` 的 Z85（RFC 32）；0 和省略是 base64url。握手 Cookie 仍是 base64url。节点 MUST 两种都收。Status `h2_conns`（1–4）是到这条前置的 HTTP/2 连接数；0 保持一条。Inflight 是这些连接上未完成 GET 的总数。HTTP 413：预算减半，下限 1024，再拆开。HTTP 405：这条前置不是 GET。Inflight 2–8（协议默认 4；Status `inflight` 是上限）。客户端看到 HTTP/2 `GOAWAY ENHANCE_YOUR_CALM` MUST 在同一会话上退避，MAY 在下一条已经带着 mux 的 GET 上发 CmdTune event 1。节点 MAY 降低这条前置的 `header_budget` / `inflight`，并在这个 GET 体里回 CmdTune event 0。空闲时上行空着：至少等 `keepalive_ms / 2`（且不少于 1 秒）。已经带着 mux Data、Open、Dgram 或 Leave 的 GET 不算空闲。节点 SHOULD 为下行把 GET 最多攥 250 ms，MUST NOT 攥超过 1 秒。上行里已经有 CmdData 时，节点 MAY 一下行 Data 入队就回。这条上行有 ≥ 2 KiB CmdData 时，节点 SHOULD 把这个 GET 体封顶 96 KiB，好让下一条 Cookie 快点转出去；纯下载的 GET 仍是 256 KiB（多条流时 192 KiB）。

HTTP/2 断了但没 Leave，会话不结束。客户端进程退出前 MUST 再发一个 GET，上行批次里带着 CmdLeave。节点于是关掉 mux 会话，忘掉 GET 会话 id，Detach，Release。`lease_hold` 仍为 `ok_resumed` 留着覆盖网 IP。

### 3.5 HTTP/2 CONNECT (`h2`)

ALPN `h2`。CONNECT 到节点本地路径（默认 `/api/v1/tunnel`）。200，然后消息 `u32be length || bytes`：auth、AEAD(Status)、AEAD(mux 批次)。

### 3.6 QUIC mux (`quic`)

客户端发起的第一条双向流，长度前缀消息和 `h2` 一样。ALPN MUST 是 `h3`。如果这个 UDP 口上也开了 HTTP/3 CONNECT，节点按第一条流的第一个字节区分：0 是这条 mux（4 字节长度，不超过 1 MiB），1 是 HTTP/3 的 HEADERS 帧。节点在知道之前先发 HTTP/3 SETTINGS；mux 客户端不读那条流。

### 3.7 HTTP/3 CONNECT (`h3`)

ALPN `h3`。CONNECT 路径形状和 `h2` 一样。

### 3.8 路径金丝雀

Status MAY 置 `canary_flags`：bit 0 胖 ClientHello，bit 1 ClientHello 拆成两条 TLS 记录（节点拼回来）。`canary_pad` 是额外填充字节，0–2048。

成员出示浏览器模样的 ClientHello，CDN、反向代理、TLS 中间盒看到的是普通 HTTPS。中间盒要是改写了握手，会话就起不来。这是路径完整性：节点不收被改坏的 TLS。它不向会重组记录的检查器隐瞒 SNI。

### 3.9 CONNECT 拼接 (`connect`)

GET mux 在请求/响应上轮询一套流协议。节点 MAY 在 Status 里置能力 `connect`，仅当 `fronts` 列出 `connect`（默认关）。常规 GET/WS 握手（租约、覆盖网 IP）之后，客户端 MAY 用 HTTP/2 `CONNECT` `:authority` `host:port` 打开 TCP，而不是 mux `CmdOpen`。该 CONNECT MUST 带与 GET 握手相同的 0x02 Cookie（`Cookie: session=` base64url，`X-Auth: 1`）。证明失败为 HTTP 404。节点拨那个目标并把字节拼上。它 MUST 拒绝 loopback、link-local（含 `169.254.169.254`）、multicast、unspecified 和 `localhost`。覆盖网成员 IP、UDP、ICMP 仍走 mux。没有 `connect` 的节点，每条 TCP 都走 mux。未开此前置的 HTTP CONNECT MUST 404。

---

## 4. 认证与保密

访问密钥是 `lt_` 加 48 个十六进制字符（24 字节）。节点存 SHA-256(key)。部署 MAY 所有成员共用一个令牌；那是配置。

### 4.1 无客户端时钟的证明

```
0x02 || nonce[16] || eph_c[32] || HMAC-SHA256(key, "lft-a2" || nonce || eph_c)[0:16] || Hello
```

`eph_c` 是客户端的临时 X25519 公钥。重放一个*新* nonce 是新握手。同一个 `nonce||eph_c` 落在节点时钟 10 分钟缓存里，是幂等重试（GET Cookie）；同一个 nonce 换了 `eph_c` 就拒。客户端时钟不用。

节点 MAY 仍收 1.0.16 的证明 `0x02 || nonce[16] || HMAC-SHA256(key, nonce)[0:16] || Hello`，会话密钥 HKDF(key, nonce)，第一条消息 `Seal(Status)` 不带 `eph_n`，以及 `0x03` 续上票据。参考节点在整个 1.0.x 软件里这么做。本修订的客户端 MUST 发 4.1 的证明。1.1 里丢掉 1.0.16 这条路。

### 4.2 续上

换路径或进程重启发一条新的 `0x02`，新临时密钥，同一个 Hello `instance_id`。Hold 按已认证成员加 instance 把停着的 mux 再挂上。副本故障切换是在新节点上发 `0x02`；覆盖网 IP 跟着 `lease_hold` 和 instance。Status MAY 仍带空的 `ticket` 字段（长度 0）。

### 4.3 会话密钥

```
ss = X25519(sk_c, eph_n)
HKDF-SHA256(ikm=ss, salt=nonce, info="loft-v1", L=32)
```

节点第一条消息是 `eph_n[32] || Seal(Status)`。后面的 mux 消息只 Seal。`eph_n` 是节点这次握手的临时 X25519 公钥。HMAC 盖住 `eph_c`，所以偷来的 Cookie 组不成会话：AEAD 密钥需要 `sk_c`，它没离开过客户端。

封装：`nonce[12] || ChaCha20-Poly1305(key, nonce, plaintext, aad)`（RFC 8439）。Nonce MUST 对每个会话密钥唯一。参考实现用 32 位随机前缀加 64 位计数器，好让停着的 GET 会话接着计数。GET 用 AAD = 会话 id；其他前置用空 AAD，除非某节另有说明。

AEAD 盖住 mux。TLS 盖住到终结器的路径。终结 TLS 的 CDN 看到的是 mux 的密文。

---

## 5. Hello 和 Status

Hello 接在 auth blob 后面。节点第一条消息是 `eph_n[32] || Seal(Status)`。Status 是 AEAD 明文。Hello 和 Status 都不是 mux 命令。

### 5.1 Hello

```
u8    version          (= 1)
u8    member_id_len || member_id
u8    instance_id_len || instance_id
u16be proto_min
u16be proto_max
u64be caps
u8    program_len || program
u8    software_len || software
```

`instance_id` 存在客户端上。空表示节点在 Status 里发一个；客户端 MUST 存下来。字符串 ≤ 64 字节。协议版本是 `major<<8|minor`（`0x0100` = 1.0）。能力 `relay` 标记中继干线。能力 `p2p` 标记会宣告监听端口的客户端。

没有 proto/caps 的老 blob 仍能解。尾巴上不认识的字节 MUST 忽略。

### 5.2 Status

字段按顺序：

| 字段 | 编码 |
|------|-----------|
| code | u16be |
| lease_sec | u32be |
| mtu | u16be |
| vlan | u16be |
| keepalive_ms, idle_ms, reconnect_min_ms, reconnect_max_ms | 各 u32be |
| reconnect_fast_n | u16be |
| ip, gateway, instance_id | u8 len \|\| bytes |
| reconnect_fast_for_ms | u32be |
| selected proto, min, max | 各 u16be |
| caps | u64be |
| program, software | u8 len \|\| bytes |
| ticket | u16be len \|\| bytes |
| decoy | u8 n，然后 n × (u8 len \|\| bytes) |
| mask_pub | u8 len \|\| bytes（0 或 32） |
| replicas | u8 n，然后 n × (u8 len \|\| bytes) |
| canary_flags | u8 |
| canary_pad | u16be |
| roster | u16be len \|\| bytes |
| lists_serial | u64be |
| lists_hash | u8 len \|\| bytes（0 或 32） |
| peers | u8（0 hub，1 mesh，2 vlan） |
| p2p | u8（1 = 节点允许） |
| peer hints | u8 n ≤ 16，然后 n × (ip, host, port u16be, vlan u16be) |
| dns | u8 n ≤ 8，然后 n × (u8 len \|\| bytes) — 给客户端 TUN 的解析器 IPv4/IPv6。空：客户端 MUST 不动系统 DNS。 |
| ip6, gateway6 | 可选尾巴：各 u8 len \|\| bytes。两个都空时整段省略（只有 IPv4）。不读它们的解码器 MUST 忽略尾巴字节。 |
| header_budget, inflight | ip6/gateway6 之后可选：u16be 编码后 GET Cookie 预算，u8 inflight。有一个非零就出现（IPv4 时 ip6/gateway6 是空串）。握手值是上限；活着的 CmdTune 说了算。0 = 客户端留自己的。 |
| h2_conns, uplink_enc | inflight 之后可选：u8 HTTP/2 连接数（1–4；0 = 客户端留一条），u8 上行编码（0 base64url，1 Cookie Z85）。有一个非零就出现。老客户端忽略多余字节。 |
| gap_hold_ms | uplink_enc 之后可选：u32be。前置掉了但没 Leave 时，节点把 mux 流攥多久。0 = 客户端默认（8 秒）。老客户端忽略多余字节。 |
| routes | gap_hold_ms 之后可选：u8 n ≤ 16，然后 n × (dst, via)，各是 u8 len \|\| bytes。受管 CIDR：成员后面的实验室 LAN（`via` 是该成员的覆盖网 IP，由 `ip route` 的 VIA / `member ID` / `tag NAME` 解析）。节点把 Open `host:port` 拼到那个成员上。客户端 MUST NOT 安装 `via` 等于自己覆盖网 IP 的路由。老客户端忽略多余字节。 |
| policy | routes 之后可选：紧凑 ACL，让客户端在直连 P2P 上走同一套 Allow。`u8 n_own_tags` 再 tags；`u8 acl_default`（0 peers/accept，1 drop）；`u8 n_rules` 再规则；`u8 n_peers` 再覆盖网 IP + tags。空就省略。读到这段的解码器 MUST 置 Present。老客户端忽略多余字节。 |
| session_ttl_ms | policy 之后可选：u32be 活会话上限。0 = 关。跟 `lease_sec`（断开后占 IP）不是一回事。没有 policy 也要写这个字段时，编码器先写空 policy。老客户端忽略多余字节。 |
| rate_bps | session_ttl_ms 之后可选：u32be 本会话 bulk `CmdData`/`CmdDgram` 上限，双向，字节/秒。0 = 关。控制和小帧（Open、Ping、ACK ≤ 4 KiB、DNS/ICMP Dgram ≤ 512 B）不整形。节点执行。没有 `session_ttl_ms` 也要写时，编码器先写 ttl 0。老客户端忽略多余字节。 |

选定协议是两个范围交集的最大值。交集为空给出码 `protocol`（0x0203），不开 mux。

### 5.3 能力位

| 位 | 名 | 含义 |
|-----|------|---------|
| 0 | multi | 一把密钥上多个实例 |
| 1 | resume | `ok_resumed` 和租约保持 |
| 2 | sticky_ip | 密钥上粘滞的覆盖网 IP |
| 3 | vlan | 从密钥来的 VLAN |
| 4 | rules | 交换机 ACL |
| 5 | get | GET 前置 |
| 6 | ws | WebSocket 前置 |
| 7 | status | 带码的 Hello/Status |
| 8 | timers | Status 里的 keepalive / idle / reconnect |
| 9 | hello_caps | 这块能力 |
| 10 | reset | CmdReset |
| 11 | quic | QUIC mux 前置 |
| 12 | dgram | CmdDgram |
| 13 | clockless | 认证不靠客户端时钟 |
| 14 | ticket | Status MAY 带票据 blob（本修订为空） |
| 15 | decoy | Status 列出源站 |
| 16 | h2 | HTTP/2 CONNECT |
| 17 | h3 | HTTP/3 CONNECT |
| 18 | mask | 借用的源站证书 |
| 19 | relay | 本会话是中继干线 |
| 20 | roster | Status 可以带签过名的名册 |
| 21 | lists | Status 可以宣告列表 serial/hash |
| 22 | p2p | mesh 时对等直连 |
| 23 | dns | Status 可以带 DNS 服务器；gateway:53 是解析器 |
| 24 | connect | HTTP/2 CONNECT 拼接：TCP Open 不走内部 mux |
| 25 | resin | CmdReport 和 resin/draft GET 上行 |
| 26 | hold | mux 流撑过短暂前置掉线；用 `0x02` 和同一成员加 `instance_id` 续上 |
| 27 | quic_mark | decoy 节点上的 quic：Initial MUST 带 loft 标记（§11.3） |
| 28 | h3_mark | decoy 节点上的 h3：Initial MUST 带 loft 标记（§11.3） |

节点关掉某条前置 MUST 清掉对应位。客户端不会用某条前置 MUST 清掉它。SOCKS、TUN、CDN 厂商、OS、git、源站主机名不是能力。

版本协商：`max(intersection(client.min..client.max, node.min..node.max))`。Status 里的能力是节点宣告的集合。客户端自己决定用其中哪些。

---

## 6. 多路复用

Status 之后，每段 AEAD 明文是帧的拼接：

```
u16be payload_length   0..16384
u8    cmd
u32be stream_id
bytes payload
```

客户端流标识是奇数，从 1 起。节点的是偶数，从 2 起。活着的标识 MUST NOT 复用。Close 或 Reset 之后 MAY 再用。

| cmd | 名 | 载荷 |
|-----|------|---------|
| 1 | Open | 目标 |
| 2 | Data | 字节 |
| 3 | Close | 空 |
| 4 | Ping | 8 字节，原样回 |
| 5 | Pong | 那 8 字节 |
| 6 | Pad | 忽略 |
| 7 | Reset | u16be code |
| 8 | Dgram | Open 目标前缀，然后 UDP 载荷 |
| 9 | Leave | 空；stream_id MUST 是 0 |
| 10 | Tune | 4 字节：`u8 event \|\| u8 inflight \|\| u16be header_budget`，可选第 5 字节 `h2_conns`。客户端 event ≠ 0 是一次前置中断（1 `ENHANCE_YOUR_CALM`，2 HTTP 413，3 HTTP 404，4 连接重置，5 HTTP 429，6 其他 GOAWAY），在下一条已经带着 mux 的 GET 上发一次。节点 event 0 设定活着的 GET `header_budget` / `inflight` / `h2_conns`（字段里 0 = 不变）。节点只在这些值变了才回。stream_id MUST 是 0。 |
| 11 | Report | `u64be applied`（节点收下的最高连续 GET seq）。stream_id MUST 是 0。老客户端忽略不认识的 mux 命令。 |

mux 帧是 **resin** 或 **draft**。Resin 攥着，直到 Report 盖住它的 GET seq，然后在同一个 seq 上重传：Open、Ping、Pong、Leave、Tune、Report、Close、Reset，以及打到 53 端口的 Dgram。Draft 只发一次（Data、Pad、其他 Dgram）。里面的 TCP 或 UDP 如果应用还要这些字节，会再产一帧。一次 mux Pull 最多变成三个 GET seq：控制 resin、draft Data，然后 Close/Reset。Close MUST NOT 和同一条流的 Data 共用 Cookie。HTTP 出错之后，客户端用纯 resin 重试同一个 seq（Cookie 只有 draft 时用 Pad），把 seq 洞补上，draft 不重放。第一次尝试时若干 inflight GET 收到 HTTP 404 是边缘噪声；同一个 sid 上三次**重试** 404 表示源站丢了会话。活着的 stream_id 上重复 Open MUST 忽略。

Open 目标：`u16be host_len || host || u16be port`。Dgram 载荷是这个前缀加上数据报。

Leave 结束 mux 会话。stream_id 是 0。载荷为空。节点 MUST 关掉会话，Detach 覆盖网对端，Release 租约（`Online=false`）。`lease_hold` 仍为同一个 `instance_id` 留着 IP。同一批次里 Leave 后面的帧 MAY 忽略。还不会处理 Leave 的实现 MUST 忽略这条命令（不认识的 mux 命令 MUST 忽略，MUST NOT 因此重置会话）。CmdClose 结束一条流；不结束会话。

实现 MUST 把帧打成批次（每段 AEAD 明文大约 1–16 KiB）。缓冲压力下 MUST NOT 丢 Data：要做反压。每流接收缓冲至少 64 KiB，最多 256 KiB。完不成的 Open 是 Reset `stream_refused`（0x0501）。Ping 间隔是 `keepalive_ms`。空闲超时是 `idle_ms`。在 `get` 上 Pad，让 AEAD 前的明文大约 400–600 字节。

带长度前缀的包装（`h2`、`quic`、`h3`、`ctl`）：`u32be length || payload`，最多 1 MiB。AEAD 里面的 mux 载荷仍 ≤ 16384。

---

## 7. 身份和定时器

订户是一把访问密钥。实例是客户端生成、存在本地的标识。空实例：节点在 Status 里发一个；客户端存下来。

同一个实例再连上收到 `ok_resumed` 和同一个覆盖网 IP。`multi` 关着时第二个实例收到 `auth_already_online`。`multi` 开着，后续实例从池里拿地址，直到 `multi_limit`（默认 8），然后 `auth_limit`。密钥上的粘滞 IP 给第一个实例。

断开之后，`lease_hold_ms`（默认 45 秒）为这个实例留着 IP。Leave 立刻 Release `Online`；保持只是为续上预订 IP。GET 会话没 Leave 就消失了，仍保持 `Online`，直到节点下次再见到这个会话。

Hello 和 Status 都宣告 `hold` 时，意外的前置掉线（换路径、无线空洞）在 `gap_hold_ms`（默认 8 秒）内 MUST NOT Close mux 流。客户端用新的 `0x02` 和同一个 `instance_id` 重拨；节点只把停着的 mux 挂给证明匹配的那位成员。里面的 TCP 不断。`gap_hold_ms` 内没续上，节点 Close mux。Leave 仍立刻 Close。

客户端 MAY 在已知空洞附近在本地暂停 draft Data（不上线路）；resin 照发。

默认值：握手 10 秒，空闲 120 秒，保活 15 秒，租约保持 45 秒，短断开窗 8 秒，重连最小 200 ms，最大 8 秒，8 秒内快试 5 次，然后指数退避。

---

## 8. Status 码

数值稳定。名字 MAY 增加。号码不改。

| 码 | 名 | 含义 |
|------|------|---------|
| 0x0000 | ok | 新租约 |
| 0x0001 | ok_resumed | 同一实例，同一 IP |
| 0x0101 | auth_bad_key | 不认识的密钥 |
| 0x0102 | auth_revoked | 密钥换过或删了 |
| 0x0103 | auth_expired | 加入 TTL 过了 |
| 0x0104 | auth_not_authorized | 成员关掉了 |
| 0x0105 | auth_limit | multi_limit |
| 0x0106 | auth_already_online | multi 关着，另一个实例 |
| 0x0107 | auth_instance | instance id 不对 |
| 0x0108 | auth_clock | 时钟偏差（clockless 时不用） |
| 0x0201 | net_disabled | 网络关了 |
| 0x0202 | net_unknown | 网络不对 |
| 0x0203 | protocol | 没有共同版本 |
| 0x0204 | rate_limit | 握手太多 |
| 0x0205 | timeout | 握手或空闲 |
| 0x0206 | internal | 节点故障 |
| 0x0301 | ip_pool | 池耗尽 |
| 0x0302 | ip_conflict | 粘滞冲突 |
| 0x0303 | vlan_denied | VLAN 不允许 |
| 0x0401 | policy_drop | ACL 或 hub 隔离 |
| 0x0501 | stream_refused | Open 失败 |
| 0x0502 | stream_reset | 对端 Reset |

非 OK 的 Status 禁止 mux。GET 认证上的 HTTP 404 当成 `auth_bad_key`。

---

## 9. 覆盖网

节点加载一份网络文档（CIDR、网关、成员、ACL）。覆盖网 IP 和 VLAN 出现在 Status 里。打向覆盖网 CIDR 内地址的 Open 按 `peers` 转发：

| peers | Overlay Open |
|-------|----------------|
| hub | Reset `policy_drop`。打向互联网的 Open 不变。打向成员后面受管 CIDR 的 Open 仍拼接。 |
| mesh | 拼接到对端会话：在那个成员上 Open `127.0.0.1:port`。 |
| vlan | 和 mesh 一样，仅当两端同一 VLAN。 |

Hub 拼接：接收端客户端接这条流，拨本机端口。

网络上 `p2p` 对 mesh 和 vlan 默认开。开着时，Status 带着最多 16 条对端提示（覆盖网 IP、TLS 上看到的主机、监听端口、VLAN、32 字节 X25519 pub）。Hello 带能力 `p2p` 的客户端听 TCP，用 Open `loft.peer:<port>` 加上这条流上的 32 字节 X25519 pub 宣告，MAY 先用 42 字节前言拨 `host:port`，再退到 hub 拼接：

```
"LFT" || 0x00 || src_ipv4[4] || HMAC-SHA256(ss, "lft" || nonce || dest_port || src_ipv4)[0:16] || nonce[16] || dest_port u16be
```

`ss` 是 X25519(local_sk, 提示里的 peer_pub)。两名成员 `lt_` 不同，仍然共享这对密钥，HMAC 用它。应答方在自己的提示里查 `src_ipv4`。没有 pub 或 MAC 对不上就关套接字；发起方走 hub 拼接。`p2p: false` 的客户端 MUST 只用 hub 拼接。经中继看到的主机是中继自己；直连于是失败，走 hub 拼接。Status 带着 policy 时，发起方和应答方 MUST 在拼本地端口之前走和 hub 拼接同一套 Allow。没有 policy 的 Status（老节点）跳过这项检查。

每条提示记录是 `u8 ip | ip | u8 host | host | u16be port | u16be vlan`，然后可选 `u8 32 | pub[32]`。后面一个字节不是 32，解码器 MUST 当成下一个字段（老 blob，或对端还没宣告 pub）。

ACL 规则是先匹配先算，`accept` 或 `drop`。规则为空仍应用 `peers`。包字段：源、目的、源 VLAN、目的 VLAN、源 tag、目的 tag、到网关、routed（目的命中经成员的 `ip route`）。地址：`any`、`host <IP>`、CIDR、`tag NAME`（`group` 是同一记号）。`ip access-list overlay` 下的 `default deny` 保留网关，其余丢掉，直到 permit 命中。TCP Open 和覆盖网 ICMP 走同一条 Allow。`ip route DST` 的下一跳是覆盖网 IP、`member ID` 或 `tag NAME`（先活着的成员，否则固定 IP）。

覆盖网 ICMP echo 是打到 `dst:1`（IP 协议 ICMP）的 CmdDgram，载荷是 IPv4 包。节点应答网关地址。其他覆盖网目的按 `peers`（hub 丢掉成员之间的；mesh/vlan 拼接到那个成员）。打向覆盖网 CIDR 外地址的 echo，节点发成真的 ICMP echo；应答以 CmdDgram `session-ip:1` 回来。客户端对本机覆盖网 IP 的 echo 在本地应答，MUST NOT 替其他覆盖网或互联网地址伪造应答。节点 MUST 把会话的覆盖网 IP 当成包源（Darwin utun 的假地址 `169.254.8.1` 会改写）。客户端 MAY 在 echo-reply 上把这个假地址填回去，好让内核对上 ping 套接字。

---

## 10. 控制器和中继

控制器握着密钥、租约、ACL 和签名。中继终结公网 TLS，把 TLS 之后的 HTTP 拷到干线会话上：Open `hub:1` 或 `hub:2`。里面的 mux AEAD 和 FinishAuth 留在控制器；中继不解覆盖网帧，也不存成员密钥。只有带能力 `relay` 的 Hello MAY Open `hub:*` 或 `ctl:1`。成员 MUST NOT 置这一位（它不是前置）。参考客户端 Hello 里不带；角色是中继的 `loft-node` 经 ExtraCaps 置上。`enrol: list` 的节点还要求认证过的成员 id 在 `relays:` 里（Hello 成员 id 为空是共享令牌中继；`enrol_req` 仍查 id）。能力 `hold` 和续上 `0x02` 跟这一位无关。成员认证和覆盖网 IP 仍在控制器上。

```
client ──TLS──► relay ──trunk (mux hub:1|hub:2, ctl:1)──► controller
                     ▲
                     访问密钥从不存在中继上
```

### 10.1 名册

Ed25519 盖在体上：

```
u64be serial
u8 n
repeat n:
  id                 (u8+bytes)
  url                (u16be+bytes)
  decoy              (u8+bytes)
  fronts             u8 k, k × (u8+bytes), k ≤ 16
  drain              u8
  weight             u8 (0 means 100)
  path               (u8+bytes)
  mask_pub           u8 len, 0..32 bytes
```

然后 `u8 pub_len || pub || u16be sig_len || sig`。签名是体上的 Ed25519。公钥 32 字节。签名 64 字节。

活名册是 `advertise`（id `self`）加上在跳心跳、没 drain 的中继。三次没跳到（默认间隔 10 秒）或 `ctl` 流死了，中继拿掉，serial 加一。

认证后拉取：Status `roster`。认证前拉取：`GET {roster_path}`（默认 `/static/v3/manifest.json`），JSON `{"v":1,"p":"<base64url>"}`，`Cache-Control: public, max-age=60`。这个 GET 在 CDN 或有真证书的主机上能过。诱饵拼接的 IP 没有会话 HMAC 就转到源站。文档 MUST NOT 含成员令牌。参考客户端要有 `controller_pub` 才收名册。首次使用信任（`tofu: true`）是客户端显式打开的旗标，不是默认。

### 10.2 机群 (`ctl:1`)

每条消息 `u32be length || u8 type || body`。

| type | 名 | 体 |
|------|------|------|
| 1 | enrol_req | id, url, decoy, fronts |
| 2 | enrol_ok | 签过名的 slice |
| 3 | enrol_no | u16be code, reason |
| 4 | beat | load u8, conns u32be, program, uptime_s u32be |
| 5 | slice | 签过名的 slice |
| 6 | drain | id, u8 |

Slice（Ed25519 盖在体上）：serial、id、url、decoy、fronts、drain、weight、beat_ms u32be、expires unix u64be，然后 pub 和 sig 和名册一样。`enrol: list` 要求 id 在控制器 `relays:` 里。`enrol: open` 收任何认证过的干线。YAML 在设了的时候覆盖 URL、decoy、fronts、weight。Enrol 拒绝：1 不认识的 id，2 格式不对，3 重复，4 关掉了。

机器和证书怎么上线，不在本协议里。

### 10.3 地址列表

控制器 MAY 发布有名字的地址列表。名字本身在协议里没有含义。客户端把名字对到 `direct`、`via` 或 `drop`。主机落进多个对上的列表时，先 drop，再 direct，再 via。没对上的目的用客户端默认（SHOULD 是 `via`）。

体（Ed25519，和名册同一把密钥）：

```
u64be serial
u8 nsets
repeat:
  name               (u8+bytes)
  u16be n
  repeat n: u8 kind, u16be+bytes value
```

Kind 1 精确域名，2 后缀，3 CIDR。然后公钥和签名和名册一样。

Status 带着 `lists_serial` 和签过名 blob 的 SHA-256。serial 或哈希更新时客户端拉 `GET {lists_path}`（默认 `/static/v3/lists.json`）。运营方 YAML：前导 `.` 或 `*.` 是后缀；CIDR 串是 kind 3。名字匹配不分大小写。SOCKS 对主机名。TUN 对 IP（CIDR）。

---

## 11. 路径兼容

这套构造对准的是等着普通 HTTPS 的 CDN、反向代理和 TLS 中间盒。它不是 mixnet。覆盖网 IP、访问密钥、控制器身份是部署的秘密，不是公网的。

### 11.1 握手分类

ClientHello 做成当下浏览器那样，就能过银行和门户已经在用的同一套 JA3/JA4 allowlist。参考客户端里 loft 用 uTLS 指纹。独特的 ALPN、独特的 GREASE 样式，或 Go 默认密码套件，都是便宜的破绽。所以 QUIC 的 ALPN 是 `h3`。

`session_id` 是 32 字节、看着像随机。sid v3 是节点静态 X25519 上 ECDH 再 HKDF；中间盒要标出这类 ClientHello，得有那把私钥。偷来的访问密钥只标得了 sid v2（知道 `mask_pub` 之前的回退）。两把秘密都没有的中间盒，看到的是 Chrome 已经在发的同样长度的串。

伪装出示 dest 形状的 ECDSA P-256 叶子和 `ecdsa_secp256r1_sha256` CertificateVerify，用序列号绑在同一份 ECDH 上。核公有 CA 链的中间盒不会收这张叶子；只看名字、日期和签名方案的中间盒，看到的是 dest 该有的值。loft 路径上的应用记录长度 SHOULD 跟着从源站采到的样式走。只做拼接的节点不用伪装。

### 11.2 目的分类

只跑 loft 的 IP 是单点故障。CDN 主机名，或已经在跑公开站点的源站，才是打算用的前置。中继存在，是为了地址可以出名册，成员身份仍留在控制器上。

诱饵拼接的探测看到源站。把诱饵主机名或源站 IP 撤掉，是路径的运营决定，不是协议常量。

### 11.3 主动探测

扫描器打开 TLS 但没有有效 `session_id`，拼接到配置好的源站，包括自身 SNI（CT / 反向 DNS 主机名）、空 SNI，以及第一趟不是 TLS ClientHello 的。它拿到源站活着的证书和字节。这个 IP 上的 UDP 转到同一个源站，所以 QUIC 探测看到 dest，不是 loft。例外是带 loft 标记的 QUIC v1 Initial。它的 source connection ID 是 20 字节 `HKDF-SHA256(secret, salt = client_random, info = "loft-quic-v3")`，其中 client_random 是临时 X25519 公钥（最高位随机），secret 是 ECDH(该临时密钥, `mask_pub`)。没有 `mask_pub` 的客户端用 info `loft-quic-v2`，以自己的访问密钥为 secret。节点用公开的 Initial 密钥（RFC 9001 §5.2）打开 Initial，校验标记，每个 client_random 10 分钟内只认一次，并把这个客户端地址交给自己的 quic/h3 前置。节点自己的 connection ID 带 HMAC，所以新地址来的短包头数据包仍归 loft。这样的节点通告 `quic_mark` / `h3_mark` 而不是 `quic` / `h3`；不打标记的客户端 MUST NOT 使用它们。组成 sid v3 需要 `mask_pub`；覆盖网 Hello 仍要访问密钥。例外是 CDN origin pull：配了的 `cdn_peers` CIDR 可以在自身 SNI 上用节点证书完成 TLS，不需要 sid。

没有有效 Cookie 的 GET 回 404 带 HTML。掩护路径（名册、列表、静态资源）是普通 GET。

### 11.4 重放和时钟

认证 `0x02` 是 nonce、临时 X25519 pub，以及访问密钥下的 HMAC。同一个 `nonce||eph_c` 落在缓存 TTL 里是幂等重试。新 nonce 是新握手。客户端时钟不用，所以时钟跑偏的笔记本照样能进。副本收新的 `0x02`；它们不共享这份缓存。

### 11.5 名册和列表

签过名的文档是公开的。谁能 GET 到 CDN 路径，谁就能读活着的前置。这和公开网站是同一块面。访问密钥不在里面。钉死 `controller_pub` 的客户端拒掉被换掉的密钥。首次使用信任是显式打开的（`tofu`）；参考客户端默认不钉不认识的签名者。

### 11.6 覆盖网

Hub 隔离对成员之间的 Open 发 Reset `policy_drop`。Mesh 经节点拼接。P2P 是可选的 TCP 尝试，用 TLS 上看到的主机；NAT 和中继会让这次尝试失败，然后走 hub 拼接。直连 P2P 用 Status 里的 policy，和节点同一套 ACL。控制器握着成员密钥哈希、租约和覆盖网地址。其他成员 Open 某个对端时才知道它的覆盖网 IP（mesh/vlan）。`p2p` 开着时，Status 对端提示还带着 TLS 上看到的主机、监听端口和 X25519 pub。直连 TCP 用 LFT 认证（ECDH 对密钥）。`session_ttl_ms` 是活会话上限；`lease_sec` 是断开后占 IP。同一 `instance_id` 的新 `0x02` 重新计 TTL。

### 11.7 本文不承诺什么

中间盒已经有访问密钥时怎么跑。每个不认识的 IP 都被过滤、没有 allowlist 时怎么跑。TLS 终结器手里还有覆盖网内部密钥时怎么跑。这些不在协议里。

---

## 12. 限制

| 限制 | 值 |
|-------|-------|
| mux 载荷 | ≤ 16384 |
| Hello/Status 字符串 | ≤ 64 |
| 名册 URL | ≤ 255 |
| 同时未完成的 GET | ≤ 8 |
| 每会话流数 | SHOULD ≤ 256 |
| AEAD 批次 | ≤ 48 KiB（`ws`），≤ 头预算（`get`） |
| GET 掩护明文 | AEAD 前大约 400 字节 |
| 带长度前缀的包装 | ≤ 1 MiB |
| 对端提示 | ≤ 16 |
| 名册条目 | ≤ 255 |
| 每条目前置数 | ≤ 16 |
| nonce 缓存 | TTL 10 分钟；超过 50 000 条就清 |
| canary_pad | 0–2048 |
| gap_hold_ms | 0–600000（默认 8000） |
| rate_bps | 0 = 关；否则字节/秒，u32 |

---

## 13. 版本

协议 1.0 在第一次发布前保持 1.0。升 major：帧格式或认证不兼容。升 minor：新能力；老客户端仍能说。握手选两个范围里最高的版本。

软件版本是 Hello 和 Status 上的字符串。git 哈希省略。线路上有程序名和 semver 就够。

客户端 MUST 至少实现 `get` 或 `ws`、mux 和认证。要走 CDN 或 TLS 中间盒，或跑在 mesh 网络里的客户端，名册拉取、诱饵、伪装、列表和覆盖网转发都要有。

---

## 14. 测试向量

Data，流 1，载荷 `hi`：

```
00 02 02 00 00 00 01 68 69
```

Open `example.com:443`（CmdOpen 的载荷）：

```
00 0b 65 78 61 6d 70 6c 65 2e 63 6f 6d 01 bb
```

Reset `stream_refused`：载荷 `05 01`。

访问密钥形状：`lt_` + 48 个小写十六进制。整串的 SHA-256 是节点存的。

---

## 15. 参考文献

1. RFC 2119. Key words for use in RFCs to Indicate Requirement Levels.
2. RFC 5869. HMAC-based Extract-and-Expand Key Derivation Function (HKDF).
3. RFC 7539 / RFC 8439. ChaCha20 and Poly1305 for IETF Protocols.
4. RFC 8032. Edwards-Curve Digital Signature Algorithm (Ed25519).
5. RFC 8446. The Transport Layer Security (TLS) Protocol Version 1.3.
6. RFC 9000. QUIC: A UDP-Based Multiplexed and Secure Transport.
7. RFC 9113. HTTP/2.
8. RFC 9114. HTTP/3.
9. RFC 6455. The WebSocket Protocol.
10. RFC 1928. SOCKS Protocol Version 5.

---

## 16. 结语

里面的协议不大：一套 AEAD，一条 mux，稳定的码，签过名的名册，有名字的地址列表，加上覆盖网策略。花样推到路径上（前置、诱饵源站、中继 IP），运营方换边缘不用给成员重新编号。成员身份和覆盖网地址还在。

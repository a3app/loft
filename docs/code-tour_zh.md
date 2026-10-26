# 代码导读

[English](code-tour.md) · [Русский](code-tour_ru.md) · **中文**

这一页写给第一次打开 loft 源码的人：先读哪些文件，一个连接如何从应用经过代码到达互联网，以及注释里那些词是什么意思。完整的目录树见 [architecture_zh.md](architecture_zh.md)，协议规则见 [protocol_zh.md](protocol_zh.md)。

---

## 1. 从哪里开始

按顺序读这五处，就能看懂整体设计：

1. `internal/mux/mux.go` —— 多路复用器。用户的每个连接都变成同一个 mux 里的一条流，mux 再变成发到网络上的帧。这是协议的核心。
2. `internal/crypto/box.go` —— `0x02` 握手（`MakeAuth`）和帧加密（`Seal` / `Open`）。
3. `internal/transport/dial.go` —— 客户端如何选前置、连接和重连（`StartSelected`、`Supervise`）。
4. `internal/transport/get.go` —— 最复杂也最重要的前置：所有流量都走普通的 GET 请求，所以能穿过 CDN。
5. `internal/transport/hub.go` 里的 `proxyStream` —— 节点如何处理每条流：交给覆盖网，或者向互联网建立连接。

`internal/transport` 的其余部分是其他前置（WebSocket、HTTP/2、QUIC、HTTP/3、L3）的同类实现，以及防探测（decoy、mask）。

---

## 2. 一个连接的完整路径

例子：笔记本上运行 `loft` 客户端，浏览器打开 `https://example.com`。

### 客户端

1. **启动。** `clients/go/cmd/loft/main.go` 读取 `client.yaml`（`internal/config`），调用 `transport.SuperviseOn`，它在程序运行期间一直保持连接。
2. **选前置。** `StartSelected`（`dial.go`）依次尝试配置里的前置（`get`、`ws`、`h2`、`quic`、`h3`、`l3`），用第一个连上的。
3. **TLS。** `tls.go` 用类似浏览器的 ClientHello（uTLS 库）建立 TLS 1.3。在网络看来这就是普通 HTTPS。
4. **握手。** 客户端发送 `0x02`（`crypto.MakeAuth`）：随机 nonce、临时 X25519 公钥，以及证明自己知道成员密钥的 HMAC。随后是 Hello（`wire`，`dial.go` 里的 `newClientHello`）：版本、能力、`instance_id`。
5. **节点回应。** 节点发回 Status：覆盖网地址、计时器、前置列表、`ticket`。此后双方共享会话密钥，所有帧都用它加密。
6. **本地入口。** `clients/go/inbound/socks5.go`（TUN 模式下是 `tun.go`）接受浏览器的连接，调用 `Dialer.Open("example.com:443")`。
7. **流。** `mux.Session.Open` 创建一条奇数编号的流，把 Open 帧放进队列。浏览器的数据随后以 Data 帧发送。
8. **发送。** 前置从 mux 取出帧（`Pull`），加密（`crypto.Seal`）后发出：GET 放在下一个请求的 `Cookie` 头里，WebSocket 作为二进制消息。

### 节点

1. **启动。** `server/cmd/loft-node/main.go` 读取 `server.yaml`、`network.conf` 和 `members.keys`，组装 HTTP 处理器（`server/origin`），监听 443 端口。
2. **分拣入站连接。** `decoy.go` 查看第一个 TLS 包（ClientHello，在 `hellopeek.go` 里解析）。不是 loft，就把整个连接交给真实网站（decoy），扫描器看到的是普通网站。是 loft，就由节点处理。
3. **检查密钥。** `GETServer.ServeHTTP`（`get.go`）或 `WSSServer`（`wss.go`）取出 `0x02`，用成员密钥校验 HMAC（`auth.go`、`internal/identity`）。密钥不对时，节点像普通网站一样回应（GET 返回 404），不暴露任何信息。
4. **Status。** 节点分配覆盖网地址（`internal/netconf`），组装 Status（`node.go`），用会话密钥加密。
5. **流。** `serveMux`（`wss.go`）从 mux 接收流，对每条流调用 `proxyStream`（`hub.go`）。
6. **流去哪里。** 覆盖网内的地址由 `Overlay.Handle`（`overlay.go`）处理，转发给另一个成员。互联网地址交给 `dialOut`（`egress.go`），按出口规则（`internal/egress`）建立 TCP。节点自身的地址（loopback、link-local、云元数据）会被拒绝。
7. **返回。** 网站的回应沿同一条流反向传回：Data 帧加密后放在 GET 响应或 WebSocket 消息里回到客户端。

### 断线时

- `hold.go`：短暂断线时节点不立即关闭 mux，而是在 `gap_hold_ms` 内把它挂起。客户端用同一个 `instance_id` 重连，拿回同样的流——内部 TCP 不会断。
- `holder.go`：在客户端，底层连接被替换期间 SOCKS5 和 TUN 照常工作。
- `path.go`：客户端发现网络变化（Wi-Fi → LTE）后自己重连，不必等超时。

---

## 3. 词汇表

| 术语 | 含义 | 代码位置 |
|--|--|--|
| **成员（member）** | 持有 `lt_…` 密钥的设备或人。节点把密钥存在 `members.keys`，哈希存在 `network.conf`。 | `internal/identity`、`transport/auth.go` |
| **节点（node）** | loft 服务器。角色：controller（主节点）和 relay（额外入口，转发给 controller）。 | `server/cmd/loft-node` |
| **前置（front）** | 在 HTTPS 上承载 mux 的方式：`get`、`ws`、`h2`、`quic`、`h3`、`l3`、`connect`。内部协议相同，只是“包装”不同。 | `transport/get.go`、`wss.go`、`h2.go`、`quic.go`、`h3.go`、`l3.go`、`connect.go` |
| **mux** | 多路复用器：一个连接里有多条流。客户端的流用奇数编号，节点用偶数。 | `internal/mux` |
| **帧（frame）** | mux 的单位：Open、Data、Close、Reset、Dgram、Ping、Leave…… | `internal/wire/frame.go` |
| **Pull / Puller** | Pull 是从 mux 取出待发送的帧。Puller 是当前被允许这样做的一方。GET 只有一个，保证帧严格按顺序发出。 | `mux.go`（`Pull`）、`get.go`（`pump`） |
| **`0x02`** | 客户端的第一条消息：证明持有成员密钥，并带上用于会话密钥的临时公钥。 | `crypto/box.go`（`MakeAuth`） |
| **Hello / Status** | Hello 说明客户端的能力。Status 是节点的回应：地址、计时器、前置、错误码。 | `internal/wire` |
| **sid / ticket** | GET 会话标识。节点在 Status 里以 `ticket` 发回；其他前置凭它加入会话。 | `get.go`、`attach.go` |
| **attach（`0x04`）** | 不重新握手，把另一个前置（比如 WebSocket）加入正在进行的 GET 会话。 | `attach.go`、`attach_client.go` |
| **hold / park** | 断线后节点保持 mux 存活，让客户端回来时不丢连接。 | `hold.go` |
| **instance_id** | 一次客户端安装的持久标识。重连后节点凭它认出“同一个”客户端。 | `instance.go` |
| **decoy** | 节点后面的真实网站。不是 loft 的流量都交给它，所以扫描器看到的是普通网站。 | `decoy.go` |
| **mask** | 节点出示一张与 decoy 网站证书相似的证书，让人更难从 TLS 上区分 loft 和网站。 | `mask.go`、`maskcert.go` |
| **覆盖网（overlay）** | 成员组成的虚拟网络：每人有自己的 IP，节点充当交换机（hub、mesh、VLAN）。 | `overlay.go`、`internal/netconf` |
| **roster** | controller 签名的在线前置和 relay 列表。客户端下载后就知道该切到哪里。 | `roster_fetch.go`、`fleet.go` |
| **地址列表（lists）** | 签名的域名和子网集合。客户端据此决定哪些走 loft、哪些直连。 | `lists.go` |
| **egress** | 节点访问互联网的规则：直连、另一块网卡、SOCKS5、HTTP 或 SSTP。 | `internal/egress`、`transport/egress.go` |
| **canary** | 检查中间设备是否改写了 TLS 握手。改写了，会话就建立不起来。 | `tls.go`、`node.go` |
| **inner** | TLS 内部对帧的额外加密：ChaCha20-Poly1305、AES-256-GCM，直连前置上也可以不加。 | `crypto/inner.go`、`transport/inner.go` |
| **ctl:1、hub:1** | relay 与 controller 之间 mux 内的服务地址：控制和流量转发。 | `fleet.go`、`relay_ctl.go`、`hub.go` |

---

## 4. 继续阅读

- **测试就在代码旁边**（`*_test.go`）。它们是最好的用法示例：多数测试在同一个进程里启动客户端和节点，并让流量穿过它们。
- **测试向量**（`internal/crypto/vectors_test.go`、`internal/transport/vectors_test.go`）给出握手的字节，方便核对你自己的实现。
- **`*_darwin.go`、`*_linux.go`、`*_other.go`** 是同一个函数在不同操作系统上的版本。
- **`server/panel`** 是独立程序，即网页面板。它不实现协议，而是通过 root 助手 `loft-panel-adm` 配置节点。
- **`clients/mobile`** 是 iOS 和 Android 共用的 Go 引擎；Swift 和 Java 应用只是它外面的一层薄壳。

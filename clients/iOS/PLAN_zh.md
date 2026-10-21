# 计划 · loft 在 iOS

[English](PLAN.md) · [Русский](PLAN_ru.md) · **中文**

软件 1.0.0。协议 1.0。本计划不抬任一版本。

## 目标

iPhone 上的 loft / SSTP / VLESS 客户端，装成 Personal VPN。用户粘贴访问密钥、扫二维码（`loft://`、`sstp://`、`vless://`）或打开 `.loft` 文件，点打开，设备流量进会话。走 loft 时，Wi-Fi / LTE 切换保住 Overlay IP（新的 `0x02`，同一个 `instance_id`）。

## 不做

- 把 `loft-front` 当助手拉起来（macOS 那套）。appex *就是* 会话。
- SOCKS5 当别的应用用的入站。
- 同时多条 Packet Tunnel（iOS 只允一条 Personal VPN）。
- 本应用里的 IKEv2 / WireGuard。SSTP 是进程内 Go（`internal/sstp`），走同一条 Packet Tunnel。
- 第一版就上 App Store（TestFlight 够）。
- 小组件、控制中心、Apple Watch、iPad 优先布局。
- 第一个 `v*` GitHub Release 就带 Dist / IPA（要等后面的标签）。
- VIDAA / Tizen / webOS（没有 Packet Tunnel）。那些电视走 Keenetic 或 HDMI 棒。

## 阶段

### 0. 目录 — 就是这里

文档、计划、HTML 模型。导入 URI [docs/share_zh.md](../../docs/share_zh.md)。还没有 Xcode 工程。

### 1. 容器 + 空隧道

Xcode：应用 `app.loft.ios` + appex `app.loft.ios.packet-tunnel`。已经写了（薄的打开 / 断开、粘贴 YAML）。按模型做完整 GUI 是后面的事。

### 2. 会话进 appex

`gomobile bind` [`clients/mobile`](../mobile/)（`make -C ../mobile ios` → `Mobile.xcframework`）。PacketTunnelProvider 泵 `packetFlow`，套 Snapshot。已经写了。

- `packetFlow` ↔ TUN 栈（Overlay IP、MTU、网关跟 Status 一样）。
- 出站 IP 的主机路由从隧道里排除（不要发夹）。Go 里已有 `RememberGateway`。
- `NWPathMonitor`（或 provider 的路径更新）：关掉前置，用新的 `0x02` 和同一个 `instance_id` 续上。SOCKS 用不上；隧道还在。
- DNS 来自 Status `dns`，经 `NEDNSSettings`。
- Status 之后做能力交集，`select: auto`。

冒烟：ping 覆盖网网关；手机 Safari 的 HTTPS 从 loft 出去。

### 3. 配置和整机策略

App Group `group.app.loft`：YAML 配置、当前指针、偏好。`lt_…` 进 Keychain。导入/导出 `.loft`（和 macOS 同一个文件）。

- 一条当前配置（对上系统一条 VPN 的上限）。
- 不要恢复直连：`includeAllNetworks`，会话死了也不把蜂窝/Wi-Fi IP 露出去。文案用 *路由* / *直连网络*，不写「kill switch」。
- 分流：协议列表 → `includedRoutes` / `excludedRoutes`（`via` / `direct` / `drop`）。
- 简化界面：打开/断开和名字。IP、前置、字节藏在开关后面。
- 扫描：相机二维码 + 图片/文件。识别：`loft://`、`sstp://`、`vless://`、YAML / `.loft`（一条或多条连接）。URL scheme `loft`。先确认再保存。把选中出站分享成二维码 `loft://`。把选中连接导出成 `.loft`。列表：滑动编辑/删除、长按、选中时导出。副标题 类型 · 传输 · 主机。

### 4. 打包

签过名的 IPA，TestFlight。团队上要有 Network Extension 权限。GitHub Actions 以后再说；不进第一个标签。开发机 ad-hoc 签名够自己先用。

### 5. VLESS 和 SSTP

同一个进程，同一条 Packet Tunnel。扫描已经认 `vless://` 和 `sstp://`（相机、照片、文件、粘贴）。第 5 阶段把这些配置接到对应出站。loft 仍是默认。还是一条 VPN。

SSTP 是 L3：`packetFlow` 的 IP 包进 `sstp.Session`（PPP/IPCP）。这条路径没有 gVisor。地址/DNS/MTU 来自 IPCP，不是 loft Status。换路就重拨 TLS+PPP（没有 loft 握手）；IPCP 发了新 IP 就再套一遍隧道设置。

### 6. Apple TV（tvOS 17+）

iPhone GUI 之后。同一条 Packet Tunnel（`NEPacketTunnelProvider`，TN3134：tvOS 17.0，不支持 per-app VPN）。会话是 appex 里的 Go。目录以后：[`clients/tvOS/`](../tvOS/)（还没开始）。官方 `gomobile bind` 没有 tvOS 切片；把 `appletvos`（fork 或自写 CGO）加进和 iOS 同一个 xcframework。

- 底线：**tvOS 17+**（能跑 17 的 Apple TV HD 2015 和所有 4K）。
- 十英尺 SwiftUI（焦点 / Siri Remote）。iPhone 容器不是电视 UI。
- 没有相机：反向二维码、iPhone 键盘 / Continuity、粘贴。识别不变（`loft://`、`.loft`）。
- 发夹：前置 IP 走 `excludedRoutes`，跟 iPhone 一样。换路：Ethernet ↔ Wi-Fi → `NotifyPathChange`，loft 上新的 `0x02`。
- 先在 Apple TV HD（2 GiB）上量 RSS，再承诺这台。先 4K。

## Go 目录里的顺序

1. [`clients/mobile`](../mobile/) — `Engine.Start` YAML、Stop、Inject/Pull、Snapshot、ParseShare。没有 CLI 参数。已经写了。
2. loft：新的 `0x02` / `RememberGateway`。SSTP：重拨。换路是系统来的 `NotifyPathChange`。不要加能力。
3. 解析 `loft://`、`sstp://`、`vless://`（分享链接，不是线协议）。已经写在 `share.go`。
4. `loft-front` 留给 macOS。不要教它 iOS 或 tvOS。
5. tvOS bind 是 [`clients/mobile`](../mobile/) 后面的切片，等 iPhone appex 稳了再说。

## 风险

| 风险 | 怎么挡 |
|------|------------|
| Network Extension 50 MiB 里的 Go RSS | 第 2 阶段在真机上量。不够就从 bind 里丢掉不用的传输。 |
| gomobile + Network Extension 线程 | Holder 放专用线程；`packetFlow` 走 provider 队列。 |
| 发夹 | 隧道走 `0/0` 之前先排除前置 IP。 |
| 换路 | 新的 `0x02`，同一个 `instance_id`。里面的 TCP 还是会 RST。 |
| 权限 | Developer Program；Personal VPN。门户步骤写在 `docs/ios_zh.md`。 |
| 后台唤醒 | provider 还在；容器可以挂起。应用里的状态是快照。 |
| 带访问密钥的二维码 | 确认屏；URI 不进日志。用户只粘贴或选文件时，相机权限可以不要。 |
| gomobile 没有现成的 tvOS 目标 | 第 6 阶段：appletvos 切片（NetBird 那种 fork 或自写 CGO）。别拿这个挡 iPhone 路径。 |
| Apple TV HD 上的 Go RSS | 先在 4K 上量。HD 可选。 |

## 做到什么算完

**iPhone。** iOS 16+（GUI）上的成员装 TestFlight 包，导入 `.loft` 或 `loft://` 二维码，点打开，看见灯，Safari 走 Overlay IP。Wi-Fi 切 LTE 这个 Overlay IP 还在。appex 里的 Go 引擎按 iOS 15+ 编。

**Apple TV。** 第 6 阶段：同一条会话在 tvOS 17+ 上，用 Siri Remote。没有电视相机也能导入，打开，电视应用走 Overlay IP。Ethernet → Wi-Fi 这个 IP 还在。

# loft 在 iOS 上

[English](ios.md) · [Русский](ios_ru.md) · **中文**

loft 在 iPhone 上怎么跑，以后是 Apple TV（tvOS 17+）。目录：[clients/iOS/](../clients/iOS/)。计划：[clients/iOS/PLAN.md](../clients/iOS/PLAN.md)（Apple TV 是第 6 阶段）。模型：[clients/iOS/design/index.html](../clients/iOS/design/index.html)。协议：[protocol_zh.md](protocol_zh.md)。导入 URI：[share_zh.md](share_zh.md)。

容器在 [`clients/iOS/`](../clients/iOS/)。`make ios` 写出未签名的 Simulator `dist/clients/iOS/loft.app`。真机 IPA 需要 Packet Tunnel entitlement。

---

## 1. 两个进程

iOS 管隧道**什么时候**能开（Personal VPN，同时只有一条，On Demand）。Go 引擎管隧道在信道上**长什么样**。

```
应用（Safari、邮件、…）
  → Network Extension 拥有的 UTUN
      → NEPacketTunnelProvider  (appex app.loft.ios.packet-tunnel)
          → clients/mobile
              → loft  Holder (get|ws|h2|quic|h3)
              → SSTP  TLS + PPP，IP 来自 IPCP
              → VLESS TCP+TLS
                  → 服务器 IP 排除出隧道
```

容器（`app.loft.ios`）不说协议。它把 YAML 写进 App Group，然后调 `startVPNTunnel`。loft、SSTP 和 VLESS 是同一个 provider 的出站。IKEv2 仍是设置里的原装客户端。

## 2. Packet Tunnel

活会话之后（loft Status，或 SSTP IPCP）套 `NEPacketTunnelNetworkSettings`：

| Status | 设置 |
|--------|------|
| `ip` / 前缀 | `NEIPv4Settings.addresses` |
| 覆盖网 `gateway` | 默认路由的 remote address |
| `mtu` | `tunnelOverheadBytes` / MTU |
| `dns` | `NEDNSSettings` |
| `ip6` / `gateway6` | 有的话用 `NEIPv6Settings` |

前置 IP **必须**放进 `excludedRoutes`，会话才不会发夹。协议列表映射到 `includedRoutes`（`via`）和 `excludedRoutes`（`direct`）。`drop` 是 provider 里丢弃，不是操作系统路由。

不要恢复直连网络对应 `includeAllNetworks`：会话死了，手机就没有蜂窝/Wi-Fi 路径，直到再打开，或到设置里重置。

## 3. 路径切换

Wi-Fi → LTE（反过来也一样）是一条新的默认路径。操作系统适配器调用 `NotifyPathChange`。loft：关掉当前前置，用新的 `0x02` 和同一个 `instance_id` 续上（覆盖网 IP 不变）。SSTP：重新拨 TLS+PPP；若 IPCP 发了新 IP，再套一遍设置。里面的 TCP 还是会 RST。

和 macOS / CLI 的路径切换是同一份约定。不是新能力。Packet Tunnel 里不用 `WatchPath`（出口会变成覆盖网）。

## 4. 标识符

| | |
|--|--|
| 应用 | `app.loft.ios` |
| Packet Tunnel | `app.loft.ios.packet-tunnel` |
| App Group | `group.app.loft` |
| VPN 类型 | Personal VPN（Packet Tunnel） |

Apple Developer 门户：Network Extensions → Packet Tunnel，App Groups。TestFlight 用同一套 id。

引擎（appex + `clients/mobile`）：**iOS 15+**。SwiftUI 容器：**iOS 16+**。上传 App Store：Xcode 26 和 iOS 26 SDK；15 是这套工具链还能编的最低 deployment。

URL scheme `loft`（导入 `loft://…`）。摄像头扫二维码。照片 / 文件接图片或 `.loft`（一条或多条连接）。检测器：[share_zh.md](share_zh.md)。

## 5. 这不是什么

SOCKS5 在 `127.0.0.1` 上不是入站。别的 iOS 应用没法指向本机 SOCKS，除非走系统 VPN。

IPA 里没有 macOS 的 `loft-front` 和 `loft-priv`。

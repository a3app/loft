# loft · iOS

[English](README.md) · [Русский](README_ru.md) · **中文**

iOS 客户端：一条 **Packet Tunnel**，系统当成 Personal VPN。容器应用管配置和灯；Network Extension 跑 Go 会话（loft、SSTP 或 VLESS）。

协议：[docs/protocol_zh.md](../../docs/protocol_zh.md)。对接：[docs/ios_zh.md](../../docs/ios_zh.md)。导入 URI：[docs/share_zh.md](../../docs/share_zh.md)。计划：[PLAN.md](PLAN.md)。UX：[design/DESIGN_zh.md](design/DESIGN_zh.md)。模型：[design/index.html](design/index.html)。

Home 和 Settings 的 SwiftUI（`HomeView`、`SettingsView`，Xcode Canvas `#Preview`）和 Packet Tunnel 在这个目录。先 bind Go 引擎：`make -C ../mobile ios`。YAML 宿主仍在 `ContentView`。

## 范围

| 手机上 | 角色 |
|--------------|------|
| loft（get / ws / h2 / quic / h3 / l3） | loft 覆盖网。一个 `NEPacketTunnelProvider`。 |
| SSTP | 同一条隧道。TLS + PPP，IP 来自 IPCP。Go `internal/sstp`。导入 YAML `type: sstp` 或 `sstp://`。 |
| VLESS | loft 被滤时走同一条隧道。导入是二维码 `vless://`（相机或文件）。 |
| SOCKS5 当产品入站 | iOS 没有。别的应用不会去连本机 SOCKS。入站是系统 VPN。 |
| 本应用里的 IKEv2、WireGuard | 没有。IKEv2 还在 设置 → VPN。 |

iOS 同时只能开 **一条** Personal VPN。多条 loft 配置是切换，不是并行 TUN。

扩展必须把前置 IP 从隧道里摘出去，会话才不会发夹。

## 布局

```
clients/iOS/
  README.md          本文件
  PLAN.md            构建阶段（Apple TV = 第 6 阶段）
  design/            UX 说明和 HTML 模型
  App/               SwiftUI 宿主：HomeView + SettingsView（Canvas Preview）+ YAML ContentView
  PacketTunnel/      Network Extension → clients/mobile
  Loft.xcodeproj
  ../mobile/         共用 Go 引擎（iOS + Android）
```

会话是 [`clients/mobile`](../mobile/)（`internal/` + 入站栈）链进 appex。没有 `loft-front` 进程。macOS 还是单独的助手；iOS 会话就在扩展进程里。

## 需要

| | 版本 |
|--|--|
| Go 引擎（`clients/mobile`） | **iOS 15+**（上 App Store 的底线是 Xcode 26） |
| SwiftUI 容器 | **iOS 16+**（`NavigationStack`，模型） |
| 上架 | Xcode 26+，iOS 26 SDK。Deployment 15 是工具链还能编的最低版本。 |

Packet Tunnel 权限需要 Apple Developer Program。应用和 appex 之间的配置走 App Group `group.app.loft`。架构：真机 arm64，模拟器 arm64。

## 编译

在仓库根目录（Xcode，模拟器 SDK）：

```bash
make ios                 # dist/clients/iOS/loft.app
make -C clients/iOS run  # 装到 loft-ios 并启动
```

中间文件在 `dist/clients/iOS/`，不进 `clients/iOS/`。模拟器 Unsigned Debug。真机 IPA / TestFlight 以后再说。

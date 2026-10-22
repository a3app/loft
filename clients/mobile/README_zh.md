# loft · 移动引擎

[English](README.md) · [Русский](README_ru.md) · **中文**

共用的 Go 会话，给 **iOS Packet Tunnel** 和 **Android VpnService**。后面同一个 bind 再出 Android TV / Google TV 的 flavor（还是那个 AAR），以及 tvOS 切片（Apple TV，计划第 6 阶段）。没有 GUI。没有 `loft-front`。协议 1.0 不变。

iOS 产品目录：[../iOS/](../iOS/)。Android 产品目录：[../Android/](../Android/)。导入 URI：[docs/share_zh.md](../../docs/share_zh.md)。

## 出站

一个进程，系统里一条 VPN。YAML `outbounds[].type` 选路径：

| type | 线上 | 内层 IP | 包 |
|------|------|----------|---------|
| `get` `ws` `h2` `quic` `h3` | loft mux | Status 里的覆盖网 | gVisor → `Holder.Open` |
| `l3` | loft IP | Status 里的覆盖网 | H2 CONNECT IP 泵 |
| `sstp` | TLS + PPP（`internal/sstp`） | IPCP | L3 泵进 `sstp.Session` |
| `vless` | VLESS TCP+TLS / REALITY / XHTTP（`internal/vless`） | 合成 / 栈 | gVisor → VLESS 拨号 |
| `trojan` | Trojan TCP+TLS / REALITY / XHTTP（`internal/trojan`） | 合成 / 栈 | gVisor → Trojan 拨号 |

IKEv2 还是系统自带客户端。WireGuard 不在这个包里。

## API

```
NewEngine() *Engine
Start(yaml string) error
Stop()
InjectPacket([]byte) error  // IP 从系统来
PullPacket() []byte         // IP 回系统；空则 nil
SetSink(PacketSink)         // 和 PullPacket 互斥
NotifyPathChange()          // 系统通知 Wi-Fi ↔ LTE
Snapshot() *Snapshot        // 也有 Status()
ParseShare(s) (yaml, error) // loft://, sstp://, vless://, trojan://, YAML, .loft, .vpn
```

整条隧道里不要跑 `WatchPath`（出口会变成覆盖网）。系统适配器调用 `NotifyPathChange`。loft 换路用新的 `0x02` 和同一个 `instance_id`。SSTP/VLESS/Trojan 重拨；IPCP 可能发新 IP，适配器再套一遍隧道设置。

前置 / SSTP / VLESS 服务器 IP 在 `Snapshot.FrontIPs`（逗号分隔），适配器把它们排除出去（不要发夹）。

## Bind

```bash
make ios        # build/Mobile.xcframework   (Xcode, iOS 15)
make android    # build/engine.aar           (NDK, API 24, 16 KiB 页)
```

从仓库根目录：`make mobile-ios` / `make mobile-android`。

Swift `PacketTunnelProvider` 和 Java `VpnService` 泵包，并从 `Snapshot()` 装路由/DNS。Android 调 `SetProtector`（`VpnService.protect`）。iOS 在 `NEPacketTunnelNetworkSettings` 里排除前置 IP。

## 系统最低版本

| | 引擎（本包） | GUI | 上架 |
|--|--|--|--|
| iOS | **15+** | **16+** | Xcode 26，iOS 26 SDK |
| Android | **API 24**（7.0） | **API 26**（8.0） | `targetSdk` **36**（Android 16） |

Play 不定 `minSdk`。2026 年 8 月 31 日起，新的手机/平板应用必须 **target** API 36。Always-on VPN 是 API 24。通知渠道（VpnService 前台）是 API 26。arm64 的原生 `.so` 必须兼容 16 KiB 页（Play 对 `targetSdk` 35+ 的规定）。

源码：`engine.go`（会话），`packets.go`（IP 管道），`share.go`（二维码 / 粘贴），`yaml.go`，`split.go`（发夹主机）。只 bind 这个包；GUI 不从这里出。

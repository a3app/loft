# loft 在 Aurora OS 上

[English](aurora.md) · [Русский](aurora_ru.md) · **中文**

loft 在 Aurora OS 手机上怎么跑。目录：[clients/Aurora/](../clients/Aurora/)。计划：[clients/Aurora/PLAN_zh.md](../clients/Aurora/PLAN_zh.md)。模型：[clients/Aurora/design/index.html](../clients/Aurora/design/index.html)。协议：[protocol_zh.md](protocol_zh.md)。导入 URI：[share_zh.md](share_zh.md)。

Dist 里还没有 GUI。这份文件是对接规格。

---

## 1. 三块

Aurora 管 tun **什么时候**存在（ConnMan VPN 行，同时只有一条）。`loft-front` 管会话在信道上**长什么样**。QML 应用写 YAML，让 ConnMan 启动。

```
应用
  → connman-vpnd 拥有的 tun
      → ConnMan loft 插件  (app.loft.aurora)
          → loft-front
              → loft  Holder (get|ws|h2|quic|h3)
              → SSTP  TLS + PPP，IP 来自 IPCP
              → VLESS TCP+TLS
                  → 服务器 IP 排除出 tun
```

容器（`app.loft.aurora`）不说协议。它写 `client.yaml`，让 ConnMan 启动类型 `loft`（D-Bus `net.connman.vpn`）。没有插件时，它在 loopback 上起带 SOCKS 的 `loft-front`。loft、SSTP 和 VLESS 是同一行的出站。OpenVPN 仍是设置里的原装类型。

助手：`loft-connman` exec `loft-front -tun NAME -tun-noexec`。ConnMan 从 notify 脚本套地址、DNS 和路由。

没有 `clients/mobile` bind。Aurora 是 GNU/Linux；助手就是 macOS 拉起的那个 `loft-front`。

## 2. TUN

活会话之后（loft Status，或 SSTP IPCP）插件告诉 ConnMan：

| Status | ConnMan |
|--------|---------|
| `ip` / 前缀 | tun 地址 |
| 覆盖网 `gateway` | 默认路由的 remote |
| `mtu` | tun MTU |
| `dns` | 名字服务器 |
| `ip6` / `gateway6` | 有就写 |

前置 IP **必须**坐在 tun 外面，会话才不会发夹。协议列表 / 区域映射到 ConnMan 路由（`via` / `direct`）。`drop` 是助手里丢弃，不是操作系统路由。

不要恢复直连网络：会话死了仍占着 default，手机没有蜂窝/Wi-Fi 路径，直到再打开，或到设置里重置。文案用 *路由* / *直连互联网*。

## 3. 路径切换

Wi-Fi → LTE（反过来也一样）是一条新的默认路径。loft-front 已经会关掉当前前置，用新的 `0x02` 和同一个 `instance_id` 续上（覆盖网 IP 不变）。SSTP 重新拨 TLS+PPP。里面的 TCP 还是会 RST。

和 macOS / CLI 是同一份约定。不是新能力。

## 4. 标识符

| | |
|--|--|
| 应用 | `app.loft.aurora` |
| 助手 | RPM 里的 `loft-front` |
| VPN 客户端 | `loft-connman`（ConnMan Task） |
| ConnMan 类型 | `loft` |
| VPN 行名 | **Loft** |
| URL scheme | `loft` |

Aurora SDK：Qt/QML 应用 + 特权插件 RPM。先在开发机上 sideload。

引擎（`loft-front`）：**linux/arm64**，Aurora 5 的 glibc。GUI：Aurora OS **5.x**。

## 5. 这不是什么

SOCKS5 在 `127.0.0.1` 上不是别的 Aurora 应用用的入站。QML 宿主只在没有 ConnMan 插件时才绑它。

RPM 里没有 macOS 的 `loft-priv`。TUN 由 ConnMan 创建。

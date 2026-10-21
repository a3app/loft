# loft · Aurora OS

[English](README.md) · [Русский](README_ru.md) · **中文**

Aurora OS 客户端：一条 **ConnMan** 覆盖网会话。QML 应用管配置和灯；`loft-front` 是会话进程；`connman-vpnd` 管 tun。

协议：[docs/protocol_zh.md](../../docs/protocol_zh.md)。对接：[docs/aurora_zh.md](../../docs/aurora_zh.md)。导入 URI：[docs/share_zh.md](../../docs/share_zh.md)。计划：[PLAN_zh.md](PLAN_zh.md)。UX：[design/DESIGN_zh.md](design/DESIGN_zh.md)。模型：[design/index.html](design/index.html)。

薄 QML 宿主在这个目录。交叉编译助手：`make front`。

## 范围

| 手机上 | 角色 |
|--------------|------|
| loft（get / ws / h2 / quic / h3） | loft 覆盖网。一条 ConnMan。 |
| SSTP | 接上以后同一条。TLS + PPP，IP 来自 IPCP。Go `internal/sstp`。 |
| VLESS | loft 被滤时走同一条。导入是二维码 `vless://`。 |
| Trojan | loft 被滤时走同一条。导入是二维码 `trojan://`。Go `internal/trojan`。 |
| SOCKS5 当产品入站 | Aurora OS 没有。别的应用不会去连本机 SOCKS。入站是 ConnMan TUN。 |
| 本应用里的 OpenVPN、IPsec、WireGuard | 没有。那些还在 设置 → VPN。 |

Aurora OS 同时只能开 **一条** VPN。多条 loft 配置是切换，不是并行 TUN。

插件必须把前置 IP 从 tun 里摘出去，会话才不会发夹。

## 布局

```
clients/Aurora/
  README.md          本文件
  PLAN.md            构建阶段
  design/            UX 说明和 HTML 模型
  qml/               Silica GUI（薄粘贴宿主）
  src/               C++ QProcess → loft-front
  connman/           VPN 插件、loft-connman、notify 脚本
  settings/          设置 → 网络 里的类型 Loft
  rpm/               RPM spec
  CMakeLists.txt     Aurora IDE / mb2
  ../go/cmd/loft-front
```

会话是 [`loft-front`](../go/cmd/loft-front/)，跟 macOS 应用拉起的同一个助手。没有 gomobile bind。iOS/Android 继续用 `clients/mobile`。

## 需要

| | |
|--|--|
| 系统 | Aurora OS **5.x** |
| GUI | Qt 5 / QML Silica，Aurora SDK |
| 助手 | `loft-front` `linux/arm64`（`make front`） |
| 包 | RPM，id `app.loft.aurora` |

架构：先 aarch64，设备要的话再 armv7hl。模拟器是 i486/x86_64，只跑 QML。

## 编译

```bash
make front          # Go 交叉：bin/loft-front (linux/arm64, 关 CGO)
# Aurora IDE → open clients/Aurora
# 或 SDK 在 PATH 里：
#   mb2 build
```

Id：`app.loft.aurora`。系统类型名：**Loft**。协议：**loft** / **LOFT**。

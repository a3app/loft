# loft · KeeneticOS

[English](README.md) · [Русский](README_ru.md) · **中文**

Entware 客户端：一个 **OpkgTun** 接口，KeeneticOS 拿它进连接优先级和策略路由。设置在 LAN 口上；登录是 Keenetic 网页管理会话。

协议：[docs/protocol_zh.md](../../docs/protocol_zh.md)。对接：[docs/keenetic_zh.md](../../docs/keenetic_zh.md)。计划：[PLAN_zh.md](PLAN_zh.md)。UX：[design/DESIGN_zh.md](design/DESIGN_zh.md)。模型：[design/index.html](design/index.html)。

这里只做目录。还没有二进制。

## 范围

| 路由器上 | 角色 |
|---------------|------|
| loft（get / ws / h2 / quic / h3） | loft 覆盖网。一个 OpkgTun。 |
| VLESS | 以后。loft 被滤时的第二条 loft 出站。 |
| SSTP、WireGuard、OpenVPN、IPsec、OpenConnect | 系统自带 KeeneticOS。不是这个包。 |

客户端 **不得** 装默认路由。表归 NDM。前置 IP 留在运营商路径上，会话才不会发夹。

## 布局（计划）

```
clients/Keenetic/
  README.md          本文件
  PLAN.md            构建阶段
  design/            UX 说明和 HTML 模型
  cmd/               （以后）loft-keenetic：会话 + 面板
  pack/              （以后）init.d、ipk、ndmc 助手
```

会话是 `clients/go` 里的 Go 客户端，TUN 开着、`default_route` 关。面板是小 HTTP 服务，检查 Keenetic `/auth` cookie。

## 需要

KeeneticOS **5.0+**（策略里有 OpkgTun）。组件：OPKG、TUN/TAP。Entware 在 NAND 或 USB EXT4。架构：aarch64、armv7、mipsel。

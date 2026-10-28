# loft · KeeneticOS

[English](README.md) · [Русский](README_ru.md) · **中文**

Entware 客户端：每个连接一个 **OpkgTun** 接口，KeeneticOS 拿它们进连接优先级和策略路由。设置在 LAN 口上；登录是 Keenetic 网页管理会话。

协议：[docs/protocol_zh.md](../../docs/protocol_zh.md)。对接：[docs/ndms_zh.md](../../docs/ndms_zh.md)。计划：[PLAN.md](PLAN.md)。UX：[design/DESIGN_zh.md](design/DESIGN_zh.md)。模型：[design/index.html](design/index.html)。

二进制是 `loft-ndms`。它提供 LAN 页面，并为每个已启用的连接再启动自己（`-session`）。`clients/go/lanui` 仍是 MikroTik 的页面。

## 范围

| 路由器上 | 角色 |
|---------------|------|
| loft（get / ws / h2 / quic / h3 / l3） | loft 覆盖网。每个连接一个 OpkgTun。 |
| VLESS、Trojan | 单独的连接，各有自己的 OpkgTun。 |
| SSTP、WireGuard、OpenVPN、IPsec、OpenConnect | 系统自带 KeeneticOS。不是这个包。 |

客户端不装默认路由。路由表归 NDM。在 `interface OpkgTunN up` 之前，用 `ip route <ipv4>/32 <ISP>` 把前置地址钉在运营商接口上。

## 构建

KeeneticOS **5.0+**（策略里有 OpkgTun）。组件：OPKG 和 TUN/TAP。Entware 在 NAND 或 USB EXT4。

```bash
make -C clients/NDMS test
make -C clients/NDMS ipk       # aarch64：Giga、Peak、Hopper
make -C clients/NDMS ipk-all   # aarch64、armv7 soft-float、mipsel soft-float
```

在路由器上：`opkg install loft-ndms-<version>-<arch>.ipk`。页面是 `http://<lan>:8090/`。样例配置是 `/opt/etc/loft/client.yaml`（`check_updates: true`）。YAML 里没有 `enabled` 就是关闭。

`prerm` 只在卸载时删除 OpkgTun0…OpkgTun15。升级会停进程并留下接口，优先级还在。

面板从 `a3app/loft` 最新发布安装更新的 `.ipk`，前提是发布里有这个文件和它的 `.sha256`。用 Makefile 构建。Dist 的内容不变。

## 布局

```
clients/NDMS/
  cmd/loft-ndms   父进程和 -session 子进程
  web/index.html      LAN 页面（打进包里的那份）
  pack/               S99loft、prerm、postinst、样例、mkipk
  design/             UX 说明和模型
```

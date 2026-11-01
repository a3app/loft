# ConnMan 插件 · loft

[English](README.md) · **中文**

本文件就是约定。

`connman-vpnd` 管着 tun。这个插件注册类型 **loft**，拉起 `loft-connman`，再通过 notify 脚本把 Overlay IP / DNS / MTU 喂回去。`loft-front` 挂到命名 tun 上（`-tun NAME -tun-noexec`），不配地址也不配路由。

## 部件

| 文件 | 作用 | 安装 |
|------|------|---------|
| `loft-plugin.c` | ConnMan VPN 驱动。类型名 `loft`。 | `%{_libdir}/connman/plugins-vpn/loft.so` |
| `loft-connman.c` | VPN 客户端：拉起 loft-front，把 STATUS 映射成脚本环境。 | `%{_libexecdir}/harbour-loft/loft-connman`（Aurora：`app.loft.aurora`） |
| `loft-script.c` | D-Bus notify `ss a{ss}`（OpenVPN 约定）。 | `%{_libdir}/connman/scripts/loft-script` |
| `loft.conf` | `[VPNBinary]` User/Group。 | `/etc/connman/vpn-plugin/loft.conf` |
| `../settings/*.qml` | 设置 → 网络，类型 **Loft**。 | `%{_datadir}/sailfish-vpn/loft/` |

Provider 属性：`Host`, `Name`, `Domain`, `Loft.Token`, `Loft.URL`, `Loft.Type`, `Loft.Path`, `Loft.CDN`, `Loft.User`, `Loft.SNI`, `Loft.Config`。

发夹（hairpin）：`Host` / `--host` 变成 `trusted_ip`，ConnMan 在默认路由之前把前置钉在 tun 外面。

GUI 从不打开 `/dev/net/tun`。`.so` 对 Sailfish 或 Aurora SDK（`connman-devel`）编译。Dist 不在 macOS 上编它。

方案：[../PLAN.md](../PLAN.md)。对接：[../../../docs/sailfish_zh.md](../../../docs/sailfish_zh.md)。

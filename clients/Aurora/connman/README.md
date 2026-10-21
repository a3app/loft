# ConnMan plugin · loft

**English** · [中文](README_zh.md) · this file is the contract.

`connman-vpnd` owns the tun. This plugin registers type **loft**, starts `loft-connman`, and feeds overlay IP / DNS / MTU back through the notify script. `loft-front` attaches to the named tun (`-tun NAME -tun-noexec`) and does not configure addresses or routes.

## Pieces

| File | Role | Install |
|------|------|---------|
| `loft-plugin.c` | ConnMan VPN driver. Type name `loft`. | `%{_libdir}/connman/plugins-vpn/loft.so` |
| `loft-connman.c` | VPN client: spawn loft-front, map STATUS → script env. | `%{_libexecdir}/app.loft.aurora/loft-connman` |
| `loft-script.c` | D-Bus notify `ss a{ss}` (OpenVPN contract). | `%{_libdir}/connman/scripts/loft-script` |
| `loft.conf` | `[VPNBinary]` User/Group. | `/etc/connman/vpn-plugin/loft.conf` |
| `../settings/*.qml` | Settings → Networking type **Loft**. | `%{_datadir}/sailfish-vpn/loft/` |

Provider properties: `Host`, `Name`, `Domain`, `Loft.Token`, `Loft.URL`, `Loft.Type`, `Loft.Path`, `Loft.CDN`, `Loft.User`, `Loft.SNI`, `Loft.Config`.

Hairpin: `Host` / `--host` becomes `trusted_ip` so ConnMan pins the front off the tun before the default route.

The GUI never opens `/dev/net/tun`. The `.so` compiles only against the Aurora SDK (`connman-devel`). Dist does not build it on macOS.

Plan: [../PLAN.md](../PLAN.md). Integration: [../../../docs/aurora.md](../../../docs/aurora.md).

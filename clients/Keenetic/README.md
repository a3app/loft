# loft · KeeneticOS

**English** · [Русский](README_ru.md) · [中文](README_zh.md)

Entware client: one **OpkgTun** interface per connection, which KeeneticOS uses in connection priorities and policy routing. Settings live on a LAN port; login is the Keenetic web-admin session.

Protocol: [docs/protocol.md](../../docs/protocol.md). Integration: [docs/keenetic.md](../../docs/keenetic.md). Plan: [PLAN.md](PLAN.md). UX: [design/DESIGN.md](design/DESIGN.md). Mock: [design/index.html](design/index.html).

The binary is `loft-keenetic`. It serves the LAN page and starts one child of the same binary (`-session`) per enabled connection. `clients/go/lanui` stays the MikroTik page.

## Scope

| On the router | Role |
|---------------|------|
| loft (get / ws / h2 / quic / h3 / l3) | loft overlay. One OpkgTun per connection. |
| VLESS, Trojan | Separate connections, each with its own OpkgTun. |
| SSTP, WireGuard, OpenVPN, IPsec, OpenConnect | Stock KeeneticOS. Not this package. |

The client does not install a default route. NDM owns the table. Before `interface OpkgTunN up`, front addresses are pinned with `ip route <ipv4>/32 <ISP>`.

## Build

KeeneticOS **5.0+** (OpkgTun in policies). Components: OPKG, TUN/TAP. Entware on NAND or USB EXT4.

```bash
make -C clients/Keenetic test
make -C clients/Keenetic ipk       # aarch64: Giga, Peak, Hopper
make -C clients/Keenetic ipk-all   # aarch64, armv7 soft-float, mipsel soft-float
```

On the router: `opkg install loft-keenetic-<version>-<arch>.ipk`. The page is `http://<lan>:8090/`. The sample config is `/opt/etc/loft/client.yaml` (`check_updates: true`). A missing `enabled` field means off.

`prerm` deletes OpkgTun0…OpkgTun15 only when the package is removed. An upgrade stops the process and leaves the interfaces, so priorities that name them stay.

The panel installs a newer `.ipk` from the latest `a3app/loft` release when that asset and its `.sha256` are published. The Makefile is the build. Dist is unchanged.

## Layout

```
clients/Keenetic/
  cmd/loft-keenetic   parent and -session child
  web/index.html      LAN page (the page that ships)
  pack/               S99loft, prerm, postinst, sample, mkipk
  design/             UX spec and the mock
```

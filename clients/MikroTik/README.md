# loft · RouterOS

**English** · [Русский](README_ru.md) · [中文](README_zh.md)

Container client for MikroTik **RouterOS 7**. The same `loft` binary as Linux CLI, inside `/container`. Not a vendor `.npk`.

Protocol: [docs/protocol.md](../../docs/protocol.md). Integration: [docs/mikrotik.md](../../docs/mikrotik.md). UX: [design/DESIGN.md](design/DESIGN.md). Mock: [design/index.html](design/index.html).

## What the operator gets

| | |
|--|--|
| SOCKS5 `172.31.20.2:1080` | Always, once the container is `running` and the log shows `socks5`. Tools and browsers use this today. |
| LAN page `http://172.31.20.2:8090/` | Status, member key, node URL, **RouterOS uplink** (`ether1`, `lte1`, …). Writes YAML and `uplink.rsc`. |
| LAN gateway | After the log shows `tun up`. Then `policy.rsc` marks a LAN list into table `loft`. |
| Fronts | Bound on `veth-loft` (`loft -if 172.31.20.1`) so GET/WS/H2/QUIC/H3 do not hairpin into the overlay. |

RouterOS owns **where** packets go (mangle, routing tables, address-lists). loft owns **how** the session looks on the wire.

## Boards

| | |
|--|--|
| Runs | arm, arm64, x86 / CHR — RB5009, hAP ax²/ax³, CCR2004+, CHR |
| Does not run | mipsbe / mmips / smips — hEX, hAP lite, and most cheap boards |

Need RouterOS **7.12+**, extra package `container`, `device-mode container=yes` (Reset once), and a disk (`/disk/print` — usually `disk1`).

## Layout

```
clients/MikroTik/
  README.md          this file
  design/            UX spec and HTML walkthrough
  pack/              Dockerfile, client.yaml, check/install/policy/uplink/uninstall.rsc
  Makefile           linux loft + docker save
```

## Install

On a PC (Docker):

```bash
make -C clients/MikroTik image GOARCH=arm64   # or arm | amd64
```

Upload `pack/loft-linux-<arch>.tar` → `<disk>/loft/loft.tar` and `pack/client.yaml` → `<disk>/loft/data/client.yaml`. Set `token` and `url`.

On the router, New Terminal:

1. `pack/check.rsc`
2. `pack/install.rsc` (edit `:local disk`)
3. `/container/start loft`
4. GUI: `http://172.31.20.2:8090/` — key, URL, uplink. Then `/import disk1/loft/data/uplink.rsc` if the WAN changed.
5. SOCKS: `172.31.20.2:1080`. Gateway: `pack/policy.rsc` only after `tun up`.

Remove with `pack/uninstall.rsc`. YAML on disk stays.

## Out of scope here

Vendor `.npk`. Winbox “Add loft” type. SSTP / WireGuard / IPsec that already ship in RouterOS. loft-node as the HTTPS origin on the same board.

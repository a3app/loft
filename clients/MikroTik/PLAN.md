# Plan · loft on RouterOS

**English** · [Русский](PLAN_ru.md) · [中文](PLAN_zh.md)

Software follows the tree `VERSION`. Protocol 1.0. This plan does not raise either.

## Goal

A routed loft member on MikroTik that an operator can stand up from Winbox/WebFig without a Linux box in front of the LAN. SOCKS5 on the first boot; LAN gateway when the container has TUN.

## Out of scope

- A vendor `.npk` (only MikroTik ships those).
- A new row in Winbox “Interfaces / VPN”.
- loft-node / decoy origin on the same board.
- SSTP, WireGuard, IPsec, L2TP that RouterOS already has.
- MIPS boards.

## Phases

### 0. Skeleton — this tree

Docs, plan, HTML walkthrough, Dockerfile, `client.yaml`, `check.rsc` / `install.rsc` / `policy.rsc` / `uninstall.rsc`. `make image` builds the OCI tarball. Dist does not ship it yet.

### 1. Smoke on CHR and one ARM64 board

- Image `linux/amd64` on CHR, `linux/arm64` on RB5009 or hAP ax³.
- Member key + GET front. Log: `socks5`, then `outbound … up overlay=`.
- SOCKS from a LAN host: `curl --socks5 172.31.20.2:1080 https://example.net`.
- Confirm fronts stay on `eth0` (`-if eth0`) while TUN is up.

### 2. Gateway

- If `/dev/net/tun` exists: `tun up`, then `policy.rsc`.
- Ping overlay gateway from a host on `loft-lan`.
- If TUN is missing: keep SOCKS; log a single line and do not run `policy.rsc`.

### 3. LAN page

HTTP on `172.31.20.2:8090` (`listen.http`). Status, key, URL, front, RouterOS uplink. Writes YAML and `uplink.rsc`. Winbox cookie is not shared. Optional `listen.http_pass`.

### 4. Package in Dist

Optional later tag: attach `loft-linux-arm64.tar` / `amd64` / `arm` next to CLI. First RouterOS tag does not have to wait for Dist.

## Risks

| Risk | Mitigation |
|------|------------|
| No `/dev/net/tun` in the netns | SOCKS is the supported path; gateway is gated on `tun up`. |
| Hairpin of GET/WS into TUN | `loft -if eth0`; policy mangle skips `172.31.20.0/24`. |
| Disk full / SPI flash | `root-dir` and `tmpdir` on `disk1`, never NAND. |
| Wrong image arch | `check.rsc` prints `architecture-name`; tarball name matches. |
| device-mode off | `check.rsc` stops the operator before `install.rsc`. |
| RSS on 256 MiB boards | Ship arm64 first; measure before 32-bit ARM. |

## Done when

An operator on RB5009 or CHR pastes `check.rsc` then `install.rsc`, starts `loft`, and a laptop on the LAN reaches the overlay through SOCKS. If TUN is present, `policy.rsc` puts a device list on the overlay without a default route on RouterOS itself.

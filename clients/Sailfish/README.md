# loft · Sailfish OS

**English** · [Русский](README_ru.md) · [中文](README_zh.md)

Sailfish OS client: one **ConnMan** overlay session. The QML app holds profiles and the lamp; `loft-front` is the session process; `connman-vpnd` owns the tun.

Aurora OS is the OMP packaging of this tree: [`aurora/`](aurora/) (`app.loft.aurora`). Sailfish RPM: [`sailfish/`](sailfish/) (`harbour-loft`).

Protocol: [docs/protocol.md](../../docs/protocol.md). Integration: [docs/sailfish.md](../../docs/sailfish.md). Aurora flavor: [docs/aurora.md](../../docs/aurora.md). Import URI: [docs/share.md](../../docs/share.md). Plan: [PLAN.md](PLAN.md). UX: [design/DESIGN.md](design/DESIGN.md). Mock: [design/index.html](design/index.html).

Thin QML host is in this tree. Cross the helper: `make front`.

## Scope

| On the phone | Role |
|--------------|------|
| loft (get / ws / h2 / quic / h3) | loft overlay. One ConnMan row. |
| SSTP | Same row when wired. TLS + PPP, IP from IPCP. Go `internal/sstp`. |
| VLESS | Same row when loft is filtered. QR `vless://` is the import. |
| Trojan | Same row when loft is filtered. QR `trojan://` is the import. Go `internal/trojan`. |
| SOCKS5 as the product inbound | Not on Sailfish. Other apps do not use a local SOCKS. ConnMan TUN is the inbound. |
| OpenVPN, IPsec, WireGuard in this app | No. Those stay Settings → VPN. |

ConnMan allows **one** active VPN row. Several loft profiles are a switch, not parallel TUN sessions.

The plugin must pin front IPs off the tun so the session does not hairpin.

## Layout

```
clients/Sailfish/
  README.md          this file
  PLAN.md            build phases
  design/            UX spec and HTML mock
  qml/               Silica GUI (thin paste host)
  src/               C++ QProcess → loft-front
  connman/           VPN plugin, loft-connman, notify script
  settings/          Settings → Networking type Loft
  sailfish/          harbour-loft desktop and spec
  aurora/            app.loft.aurora desktop and spec (OMP)
  CMakeLists.txt     -DLOFT_OS=sailfish|aurora
  ../go/cmd/loft-front
```

The session is [`loft-front`](../go/cmd/loft-front/), same helper the macOS app launches. There is no gomobile bind. iOS/Android keep `clients/mobile`.

## Requires

| | Sailfish | Aurora |
|--|----------|--------|
| OS | Sailfish OS 4.x / 5.x | Aurora OS 5.x |
| GUI | Qt 5 / QML Silica, Sailfish SDK | Qt 5 / QML Silica, Aurora SDK |
| App id | `harbour-loft` | `app.loft.aurora` |
| Helper | `loft-front` `linux/arm64` (`make front`) | same binary, rebuilt if glibc differs |
| CMake | `-DLOFT_OS=sailfish` | `-DLOFT_OS=aurora` (auto if `auroraapp` is found) |

Architectures: aarch64 first, armv7hl if a device needs it. Emulator is i486/x86_64 for QML only.

## Build

```bash
make front          # Go cross: bin/loft-front (linux/arm64, CGO off)
# Sailfish SDK → open clients/Sailfish, -DLOFT_OS=sailfish
# Aurora IDE  → open clients/Sailfish, -DLOFT_OS=aurora
# or, with SDK on PATH:
#   sfdk build          # Sailfish
#   mb2 build           # Aurora (auroraapp present)
```

System type name: **Loft**. Protocol: **loft** / **LOFT**.

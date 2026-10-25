# Clients

**English** · [Русский](README_ru.md) · [中文](README_zh.md)

| | |
|--|--|
| [`go/cmd/loft`](go/cmd/loft/) | CLI: SOCKS5, TUN. Protocol 1.0 reference |
| [`go/cmd/loft-front`](go/cmd/loft-front/) | Session process. macOS and Sailfish launch this |
| [`go/cmd/loft-probe`](go/cmd/loft-probe/) | ICMP/TCP/UDP/DNS/HTTP through a session |
| [`go/cmd/loft-bench`](go/cmd/loft-bench/) | Automated GET/WS throughput vs the macOS connect path |
| [`macOS/`](macOS/) | Menu-bar app. UX: [`macOS/design/DESIGN.md`](macOS/design/DESIGN.md) |
| [`Keenetic/`](Keenetic/) | Entware / OpkgTun. Plan: [`Keenetic/PLAN.md`](Keenetic/PLAN.md). Mock: [`Keenetic/design/index.html`](Keenetic/design/index.html) |
| [`MikroTik/`](MikroTik/) | RouterOS 7 container. Mock: [`MikroTik/design/index.html`](MikroTik/design/index.html) |
| [`iOS/`](iOS/) | Packet Tunnel. Thin host + appex. Mock: [`iOS/design/index.html`](iOS/design/index.html). Apple TV: [`iOS/PLAN.md`](iOS/PLAN.md) phase 6 |
| [`Android/`](Android/) | VpnService. Thin Java host. Phone + TV flavors. Mock: [`Android/design/index.html`](Android/design/index.html) |
| [`Sailfish/`](Sailfish/) | ConnMan overlay. Thin QML host + loft-front. Aurora OS is the OMP RPM. Mock: [`Sailfish/design/index.html`](Sailfish/design/index.html) |
| [`mobile/`](mobile/) | Shared Go engine (`gomobile bind`). No GUI. |

Shared code: `internal/`. Artifacts: `dist/clients/`.

```bash
make client        # dist/clients/go/loft
make front         # dist/clients/go/loft-front
make probe         # dist/clients/go/loft-probe
make bench         # dist/clients/go/loft-bench
make macos         # dist/clients/macOS/loft.app
make macos-dmg     # dist/clients/macOS/loft-*-macos-<arch>.dmg
make macos-dmgs    # both arm64 and x86_64 DMGs (CI)
make android       # dist/clients/Android/loft.app + loft-tv.app + loft-gtv.app + APKs
make ios           # dist/clients/iOS/loft.app (macOS) + iphonesimulator/loft.app
make dist-clients  # dist/clients/go/loft-<ver>-<os>-<arch>.tar.gz
```

Protocol: [docs/protocol.md](../docs/protocol.md). Run: [docs/guide.md](../docs/guide.md).

# Clients

**English** · [Русский](README_ru.md)

| | |
|--|--|
| [`go/cmd/loft`](go/cmd/loft/) | CLI: SOCKS5, TUN. Protocol 1.0 reference |
| [`go/cmd/loft-front`](go/cmd/loft-front/) | Session process. The macOS app launches this |
| [`go/cmd/loft-probe`](go/cmd/loft-probe/) | ICMP/TCP/UDP/DNS/HTTP through a session |
| [`go/cmd/loft-bench`](go/cmd/loft-bench/) | Automated GET/WS throughput vs the macOS connect path |
| [`macOS/`](macOS/) | Menu-bar app. UX: [`macOS/design/DESIGN.md`](macOS/design/DESIGN.md) |
| [`Keenetic/`](Keenetic/) | Entware / OpkgTun. Plan: [`Keenetic/PLAN.md`](Keenetic/PLAN.md). Mock: [`Keenetic/design/index.html`](Keenetic/design/index.html) |
| [`iOS/`](iOS/) | Packet Tunnel. Thin host + appex. Mock: [`iOS/design/index.html`](iOS/design/index.html). Apple TV: [`iOS/PLAN.md`](iOS/PLAN.md) phase 6 |
| [`Android/`](Android/) | VpnService. Thin Java host. Mock: [`Android/design/index.html`](Android/design/index.html). Android TV / Google TV: [`Android/PLAN.md`](Android/PLAN.md) phase 4 |
| [`mobile/`](mobile/) | Shared Go engine (`gomobile bind`). No GUI. |

Shared code: `internal/`. Artifacts: `dist/clients/`.

```bash
make client    # dist/clients/go/loft
make front     # dist/clients/go/loft-front
make probe     # dist/clients/go/loft-probe
make bench     # dist/clients/go/loft-bench
make macos     # dist/clients/macOS/loft.app
make macos-dmg # dist/clients/macOS/loft-*-macos-<arch>.dmg
make macos-dmgs # both arm64 and x86_64 DMGs (CI)
make dist-clients # dist/clients/go/loft-<ver>-<os>-<arch>.tar.gz
```

Protocol: [docs/protocol.md](../docs/protocol.md). Run: [docs/guide.md](../docs/guide.md).

# loft · mobile engine

**English** · [Русский](README_ru.md) · [中文](README_zh.md)

Shared Go session for **iOS Packet Tunnel** and **Android VpnService** (phone and TV flavors, same AAR). A later bind may add a tvOS slice (Apple TV, iOS PLAN phase 6). No GUI. No `loft-front`. Protocol 1.0 is unchanged.

iOS catalog: [../iOS/](../iOS/). Android catalog: [../Android/](../Android/). Import URI: [docs/share.md](../../docs/share.md).

## Outbounds

One process, one system VPN row. YAML `outbounds[].type` picks the path:

| type | Wire | Inner IP | Packets |
|------|------|----------|---------|
| `get` `ws` `h2` `quic` `h3` | loft mux | Status overlay | gVisor → `Holder.Open` |
| `l3` | loft IP | Status overlay | H2 CONNECT IP pump |
| `sstp` | TLS + PPP (`internal/sstp`) | IPCP | L3 pump into `sstp.Session` |
| `vless` | VLESS TCP+TLS / REALITY / XHTTP (`internal/vless`) | synthetic / stack | gVisor → VLESS dial |
| `trojan` | Trojan TCP+TLS / REALITY / XHTTP (`internal/trojan`) | synthetic / stack | gVisor → Trojan dial |

IKEv2 stays the stock OS client. WireGuard is not this package.

## API

```
NewEngine() *Engine
Start(yaml string) error
Stop()
InjectPacket([]byte) error  // IP from the OS
PullPacket() []byte         // IP to the OS; nil if empty
SetSink(PacketSink)         // exclusive with PullPacket
NotifyPathChange()          // Wi-Fi ↔ LTE from the OS
Snapshot() *Snapshot        // also Status(); IP/IP6, Prefix/Prefix6, Routes, FrontIPs
ParseShare(s) (yaml, error) // loft://, sstp://, vless://, trojan://, YAML, .loft, .vpn
ParseShareAll(s) (yaml, error) // same, every connection joined by ---
```

`WatchPath` is not used inside a full tunnel (egress would be the overlay). The OS adapter calls `NotifyPathChange`. loft path change uses a fresh `0x02` and the same `instance_id`. SSTP/VLESS/Trojan redial; IPCP may issue a new IP and the OS adapter reapplies tunnel settings.

Front / SSTP / VLESS server IPs are in `Snapshot.FrontIPs` (comma-separated) so the adapter can exclude them (no hairpin).

## Bind

```bash
make ios        # build/Mobile.xcframework   (Xcode, iOS 15)
make android    # build/engine.aar           (NDK, API 24, 16 KiB pages)
```

From the repo root: `make mobile-ios` / `make mobile-android`.

Swift `PacketTunnelProvider` and Java `VpnService` pump packets and install routes/DNS from `Snapshot()`. Android calls `SetProtector` (`VpnService.protect`). iOS excludes front IPs in `NEPacketTunnelNetworkSettings`.

## OS minimums

| | Engine (this package) | GUI | Store upload |
|--|--|--|--|
| iOS | **15+** | **16+** | Xcode 26, iOS 26 SDK |
| Android | **API 24** (7.0) | **API 26** (8.0) | `targetSdk` **36** (Android 16) |

Play does not set `minSdk`. New phone/tablet apps since 31 Aug 2026 must **target** API 36. Always-on VPN is API 24. Notification channels (VpnService foreground) are API 26. Native `.so` for arm64 must be 16 KiB page-size compatible (Play rule for `targetSdk` 35+).

Sources: `engine.go` (session), `packets.go` (IP pipe), `share.go` (QR / paste), `yaml.go`, `split.go` (hairpin hosts). Bind this package; do not ship a GUI from here.

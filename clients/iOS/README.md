# loft · iOS

**English** · [Русский](README_ru.md) · [中文](README_zh.md)

iOS client: one **Packet Tunnel** that the system treats as a Personal VPN. The container app holds profiles and the lamp; the Network Extension runs the Go session (loft, SSTP, or VLESS).

Protocol: [docs/protocol.md](../../docs/protocol.md). Integration: [docs/ios.md](../../docs/ios.md). Import URI: [docs/share.md](../../docs/share.md). Plan: [PLAN.md](PLAN.md). UX: [design/DESIGN.md](design/DESIGN.md). Mock: [design/index.html](design/index.html).

Home and Settings SwiftUI (`HomeView`, `SettingsView`, Xcode Canvas `#Preview`) + Packet Tunnel are in this tree. Bind the Go engine first: `make -C ../mobile ios`. YAML host remains in `ContentView`.

## Scope

| On the phone | Role |
|--------------|------|
| loft (get / ws / h2 / quic / h3 / l3) | loft overlay. One `NEPacketTunnelProvider`. |
| SSTP | Same tunnel. TLS + PPP, IP from IPCP. Go `internal/sstp`. Import YAML `type: sstp` or `sstp://`. |
| VLESS | Same tunnel when loft is filtered. QR `vless://` (camera or file) is the import. |
| SOCKS5 as the product inbound | Not on iOS. Other apps do not use a local SOCKS. The system VPN is the inbound. |
| IKEv2, WireGuard in this app | No. IKEv2 stays Settings → VPN. |

iOS allows **one** Personal VPN up. Several loft profiles are a switch, not parallel TUN sessions.

The extension must pin front IPs off the tunnel so the session does not hairpin.

## Layout

```
clients/iOS/
  README.md          this file
  PLAN.md            build phases (Apple TV = phase 6)
  design/            UX spec and HTML mock
  App/               SwiftUI host: HomeView + SettingsView (Canvas Previews) + YAML ContentView
  PacketTunnel/      Network Extension → clients/mobile
  Loft.xcodeproj
  ../mobile/         shared Go engine (iOS + Android)
```

The session is [`clients/mobile`](../mobile/) (`internal/` + inbound stack) linked into the appex. There is no `loft-front` process. macOS keeps the helper; iOS is in-process in the extension.

## Requires

| | Version |
|--|--|
| Go engine (`clients/mobile`) | **iOS 15+** (Xcode 26 floor for App Store upload) |
| SwiftUI container | **iOS 16+** (`NavigationStack`, mock) |
| Store upload | Xcode 26+, iOS 26 SDK. Deployment 15 is the toolchain minimum. |

Apple Developer Program for the Packet Tunnel entitlement. App group `group.app.loft` for profiles between the app and the appex. Architectures: arm64 device, arm64 simulator.

# loft on iOS

**English** · [Русский](ios_ru.md) · [中文](ios_zh.md)

How loft runs on iPhone, later Apple TV (tvOS 17+). Catalog: [clients/iOS/](../clients/iOS/). Plan: [clients/iOS/PLAN.md](../clients/iOS/PLAN.md) (Apple TV is phase 6). Mock: [clients/iOS/design/index.html](../clients/iOS/design/index.html). Protocol: [protocol.md](protocol.md). Import URI: [share.md](share.md).

The app is not built yet. This file is the integration specification.

---

## 1. Two processes

iOS owns **when** the tunnel is allowed (Personal VPN, one at a time, On Demand). The Go engine owns **how** the tunnel looks on the wire.

```
app (Safari, Mail, …)
  → UTUN owned by Network Extension
      → NEPacketTunnelProvider  (appex app.loft.ios.packet-tunnel)
          → clients/mobile
              → loft  Holder (get|ws|h2|quic|h3)
              → SSTP  TLS + PPP, IP from IPCP
              → VLESS TCP+TLS
                  → exclude server IP from the tunnel
```

The container (`app.loft.ios`) does not speak the protocol. It writes YAML into the App Group and calls `startVPNTunnel`. loft, SSTP, and VLESS are outbounds of the same provider. IKEv2 stays the stock Settings client.

## 2. Packet Tunnel

`NEPacketTunnelNetworkSettings` after a live session (loft Status, or SSTP IPCP):

| Status | Settings |
|--------|----------|
| `ip` / prefix | `NEIPv4Settings.addresses` |
| overlay `gateway` | remote address for the default route |
| `mtu` | `tunnelOverheadBytes` / MTU |
| `dns` | `NEDNSSettings` |
| `ip6` / `gateway6` | `NEIPv6Settings` when present |

Front IPs MUST be `excludedRoutes` so the session does not hairpin. Protocol lists map to `includedRoutes` (`via`) and `excludedRoutes` (`direct`). `drop` is a discard in the provider, not an OS route.

Hold is `includeAllNetworks`: if the session dies, the phone has no cell/Wi-Fi path until Connect again or the user resets in Settings.

## 3. Path change

Wi-Fi → LTE (and the reverse) is a new default path. The OS adapter calls `NotifyPathChange`. loft: close the live front and resume with a fresh `0x02` and the same `instance_id` (overlay IP stays). SSTP: redial TLS+PPP; apply settings again if IPCP issues a new IP. Inner TCP still RSTs.

This is the same contract as path change on macOS / CLI. It is not a new cap. `WatchPath` is not used inside the Packet Tunnel (egress would be the overlay).

## 4. Identifiers

| | |
|--|--|
| App | `app.loft.ios` |
| Packet Tunnel | `app.loft.ios.packet-tunnel` |
| App Group | `group.app.loft` |
| VPN type | Personal VPN (Packet Tunnel) |

Apple Developer portal: Network Extensions → Packet Tunnel, App Groups. TestFlight uses the same ids.

Engine (appex + `clients/mobile`): **iOS 15+**. SwiftUI container: **iOS 16+**. App Store upload: Xcode 26 and the iOS 26 SDK; 15 is the lowest deployment that toolchain still builds.

URL scheme `loft` (import `loft://…`). Camera for QR. Photos / Files for an image or `.loft` (one or several connections). Detector: [share.md](share.md).

## 5. What this is not

SOCKS5 on `127.0.0.1` is not the inbound. Other iOS apps cannot be pointed at a local SOCKS without the system VPN.

macOS `loft-front` + `loft-priv` do not ship in the IPA.

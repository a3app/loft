# Plan · loft on iOS

Software 1.0.0. Protocol 1.0. This plan does not raise either version.

## Goal

A loft / SSTP / VLESS client on iPhone that installs as a Personal VPN. The user pastes a member key, scans a QR (`loft://`, `sstp://`, `vless://`), or opens a `.loft` file, taps Connect, and device traffic goes through the session. On loft, Wi-Fi / LTE changes keep the overlay IP (fresh `0x02`, same `instance_id`).

## Out of scope

- Spawning `loft-front` as a helper (macOS model). The appex *is* the session.
- SOCKS5 as the inbound other apps use.
- Several Packet Tunnels at once (iOS allows one Personal VPN).
- IKEv2 / WireGuard inside this app. SSTP is in-process Go (`internal/sstp`) on the same Packet Tunnel.
- App Store listing in the first shipping build (TestFlight is enough).
- Widgets, Control Center, Apple Watch, iPad-first layout.
- Dist / IPA on the `v*` GitHub Release until a later tag.
- VIDAA / Tizen / webOS (no Packet Tunnel). Those TVs use Keenetic or an HDMI stick.

## Phases

### 0. Catalog — this tree

Docs, plan, HTML mock. Import URI [docs/share.md](../../docs/share.md). No Xcode project.

### 1. Container + empty tunnel

Xcode: app `app.loft.ios` + appex `app.loft.ios.packet-tunnel`. Written (YAML host in `ContentView`; Home Canvas `HomeView` + `#Preview` off/on/simple; Settings Canvas `SettingsView` General/Route/Connection, language via `LoftPrefs`; Connections Canvas `ConnectionsView` list Home/Office/Travel, mini Connect, swipe Edit/Delete, `+` sheet, `ConnEditView`). `make ios` → macOS `dist/clients/iOS/loft.app` (double-click `loft-ios`) + unsigned `iphonesimulator/loft.app`. Camera / `.loft` disk / Packet Tunnel bind later.

### 2. Session in the appex

`gomobile bind` of [`clients/mobile`](../mobile/) (`make -C ../mobile ios` → `Mobile.xcframework`). PacketTunnelProvider pumps `packetFlow` and applies Snapshot. Written.

- `packetFlow` ↔ TUN stack (the same overlay IP, MTU, gateway as Status).
- Host routes for outbound IPs excluded from the tunnel (no hairpin). `RememberGateway` already exists in Go.
- `NWPathMonitor` (or the provider’s path update): close the front, resume with a fresh `0x02` and the same `instance_id`. SOCKS does not apply; the tunnel stays.
- DNS from Status `dns` via `NEDNSSettings`.
- Cap intersection after Status, `select: auto`.

Smoke: ping overlay gateway; HTTPS via the phone browser exits through loft.

### 3. Profiles and machine policy

App Group `group.app.loft`: YAML profiles, active pointer, prefs. Keychain for `lt_…`. Import/export `.loft` (same file as macOS).

- One active profile (matches the one-VPN OS limit).
- Hold: `includeAllNetworks` so a dead session does not leak the cell/Wi-Fi IP. Copy uses *route* / *direct Internet*, not “kill switch”.
- Split: protocol lists → `includedRoutes` / `excludedRoutes` (`via` / `direct` / `drop`).
- Simple view: Connect/Disconnect and name. IP, front, bytes behind the toggle.
- Scan: camera QR + image/file. Detector: `loft://`, `sstp://`, `vless://`, YAML / `.loft` (one or several connections). URL scheme `loft`. Confirm then save. Share QR `loft://` of the selected outbound. Export the selected connection as `.loft`. List: swipe Edit/Delete, long-press, export when selected. Subtitle kind · transport · host.

### 4. Package

Signed IPA, TestFlight. Network Extension entitlement on the team. Optional GitHub Actions later; not in the first tag. Ad-hoc for the developer device is enough to dogfood.

### 5. VLESS and SSTP

Same process, same Packet Tunnel. Scan already accepts `vless://` and `sstp://` (camera, Photos, Files, paste). Phase 5 wires those profiles to the matching outbound. loft remains default. Still one VPN row.

SSTP is L3: IP packets from `packetFlow` go into `sstp.Session` (PPP/IPCP). No gVisor on that path. Address/DNS/MTU come from IPCP, not loft Status. Path change redials TLS+PPP (no loft handshake); apply tunnel settings again if IPCP issues a new IP.

### 6. Apple TV (tvOS 17+)

After the iPhone GUI. Same Packet Tunnel (`NEPacketTunnelProvider`, TN3134: tvOS 17.0, per-app VPN not supported). Session is Go in the appex. Tree later: [`clients/tvOS/`](../tvOS/) (not started). Official `gomobile bind` has no tvOS slice; add `appletvos` (fork or custom CGO) into the same xcframework as iOS.

- Floor: **tvOS 17+** (Apple TV HD 2015 and every 4K that runs 17).
- 10-foot SwiftUI (focus / Siri Remote). The iPhone container is not the TV UI.
- No camera: reverse QR, iPhone keyboard / Continuity, paste. Detector unchanged (`loft://`, `.loft`).
- Hairpin: `excludedRoutes` on front IPs, as on iPhone. Path change: Ethernet ↔ Wi-Fi → `NotifyPathChange`, fresh `0x02` on loft.
- Measure RSS on Apple TV HD (2 GiB) before promising that model. 4K first.

## Order of work in the Go tree

1. [`clients/mobile`](../mobile/) — `Engine.Start` YAML, Stop, Inject/Pull, Snapshot, ParseShare. No CLI flags. Written.
2. loft: fresh `0x02` / `RememberGateway`. SSTP: redial. Path change is `NotifyPathChange` from the OS. Do not add caps.
3. Parse `loft://`, `sstp://`, `vless://` (share URI, not the wire). Written in `share.go`.
4. Keep `loft-front` for macOS. Do not teach it iOS or tvOS.
5. tvOS bind is a later slice of [`clients/mobile`](../mobile/), after the iPhone appex is stable.

## Risks

| Risk | Mitigation |
|------|------------|
| Go RSS in a 50 MiB Network Extension | Measure on device in phase 2. Drop unused transports from the bind if needed. |
| gomobile + Network Extension threading | Run Holder on a dedicated thread; `packetFlow` on the provider queue. |
| Hairpin | Exclude front IPs before `0/0` via the tunnel. |
| Path change | Fresh `0x02`, same `instance_id`. Inner TCP still RSTs. |
| Entitlement | Developer Program; Personal VPN. Document the portal steps in `docs/ios.md`. |
| Background wake | Provider stays; the container can suspend. Status in the app is a snapshot. |
| QR with a member key | Confirm screen; do not log the URI. Camera permission is optional if the user only pastes or picks a file. |
| gomobile has no stock tvOS target | Phase 6: appletvos slice (NetBird-style fork or custom CGO). Do not block the iPhone path on that. |
| Go RSS on Apple TV HD | Measure on 4K first. HD is optional. |

## Done when

**iPhone.** A member on iOS 16+ (GUI) installs the TestFlight build, imports a `.loft` or a `loft://` QR, taps Connect, sees the lamp, and Safari uses the overlay IP. Switching Wi-Fi to LTE keeps that overlay IP. The Go engine in the appex is built for iOS 15+.

**Apple TV.** Phase 6: the same session on tvOS 17+ with the Siri Remote. Import without a TV camera, Connect, TV apps use the overlay IP. Ethernet → Wi-Fi keeps that IP.

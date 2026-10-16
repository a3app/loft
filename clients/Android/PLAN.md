# Plan · loft on Android

**English** · [Русский](PLAN_ru.md)

Software 1.0.0. Protocol 1.0. This plan does not bump either.

## Goal

A loft / SSTP / VLESS client on a phone, then on **Android TV / Google TV**, as the system VPN. The user imports a member key (`loft://`, `sstp://`, `vless://`, `.loft`), taps Connect, and device traffic goes through the session. On loft, Wi-Fi / LTE / Ethernet changes keep the overlay IP (ticket `0x03`).

Google TV is the launcher on top of Android TV. One `tv` flavor, one AAR, one `VpnService`.

## Out of scope

- Kotlin / Compose. The adapter is Java 17 Views (phone) and Leanback (TV). gomobile already emits Java.
- Spawning `loft-front`. `LoftVpnService` *is* the session.
- SOCKS5 as the inbound other apps use. Streaming apps ignore a local proxy; the product inbound is `VpnService`.
- Several VpnServices at once (Android allows one).
- IKEv2 / WireGuard inside this app. SSTP is in-process Go on the same service.
- **VIDAA, Tizen, webOS, Roku.** No TUN. Those TVs use Keenetic (or an HDMI stick that runs this client).
- Fire TV as a separate store listing in the first TV drop (same APK can sideload).
- Play Store listing in the first binary drop (sideload is enough).
- Dist / APK on the `v*` GitHub Release until a later tag.

## Phases

### 0. Catalog — this tree

Docs, HTML mock ([design/](design/)). Thin Java host already pastes YAML.

### 1. Thin VpnService

`gomobile bind` of [`clients/mobile`](../mobile/) (`make -C ../mobile android` → `engine.aar`). `LoftVpnService` pumps tun fd, `protect(fd)`, Snapshot routes/DNS. Written. Bind still needs NDK on the machine.

### 2. Phone GUI

Replace the YAML paste screen with the mock: Home (lamp, Connect/Disconnect), Connections, Scan, Edit, Settings, onboard. Java Views. Simple view default.

### 3. Profiles and machine policy

Profiles on disk, active pointer, Android Keystore for `lt_…`. Import/export `.loft`.

- One active profile (one VPN row).
- Hold: deep-link to **Always-on VPN** and **Block connections without VPN**. Copy uses *route* / *direct Internet*.
- Split: protocol lists → routes (`via` / `direct` / `drop`). `excludeRoute` on front IPs (API 33+); `protect(fd)` on every OS.
- Scan: camera QR + SAF + Photo Picker. Detector: `loft://`, `sstp://`, `vless://`, YAML / `.loft` (one or several connections). Intent `loft`. Confirm then save. List: swipe Edit/Delete, long-press, export when selected. Subtitle kind · transport · host.
- Foreground notification while up. Quick Settings tile.

### 4. Android TV / Google TV

Product flavor `tv` in this tree (Leanback, D-pad, banner, `LEANBACK_LAUNCHER`). Same `engine.aar` and `LoftVpnService`.

- 10-foot Home / list / settings. Phone layouts stay in the phone flavor.
- No TV camera: reverse QR (phone scans the TV), `.loft` from storage, paste from the remote keyboard.
- Play on TV lists only leanback apps; the phone APK does not appear there until this flavor is flagged.
- Measure RSS on a 2 GiB box before promising cheap sticks. Shield / Google TV Streamer first.

### 5. Package

Signed APK / AAB. Optional GitHub Actions later; not in the first tag. Sideload on a Pixel and a Shield is enough to dogfood.

## Order of work in the Go tree

1. [`clients/mobile`](../mobile/) — already the phone/TV engine. No CLI flags.
2. Path change: `ConnectivityManager.NetworkCallback` → `NotifyPathChange`. Ticket `0x03` on loft.
3. Do not add caps. Keep `loft-front` for macOS.

## Risks

| Risk | Mitigation |
|------|------------|
| Go RSS on a 1–2 GiB TV box | Measure on Shield first. Drop unused transports from the bind if needed. |
| Hairpin | `protect(fd)` plus `excludeRoute` (API 33+). |
| OEM boxes without the VPN consent sheet | Document; full tunnel may not start. Not a loft bug. |
| Play TV search | Leanback category on the `tv` flavor only. |
| QR with a member key | Confirm screen; do not log the URI. TV has no camera. |

## Done when

**Phone.** A member on API 26+ imports a `.loft` or a `loft://` URI, taps Connect, sees the lamp, and Chrome uses the overlay IP. Wi-Fi → LTE keeps that IP.

**TV.** The same session on Android TV / Google TV with the remote: import without a TV camera, Connect, YouTube uses the overlay IP. Ethernet → Wi-Fi keeps that IP.

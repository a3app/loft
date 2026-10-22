# Plan · loft on Android

Software 1.0.0. Protocol 1.0. This plan does not raise either version.

## Goal

A loft / SSTP / VLESS / Trojan client on a phone, then on **Android TV / Google TV**, as the system VPN. The user imports a member key (`loft://`, `sstp://`, `vless://`, `trojan://`, `.loft`), taps Connect, and device traffic goes through the session. On loft, Wi-Fi / LTE / Ethernet changes keep the overlay IP (fresh `0x02`, same `instance_id`).

Google TV is the launcher on top of Android TV. One `tv` flavor, one AAR, one `VpnService`.

## Phone — done

Java 17 Views. `LoftVpnService` is the session (`clients/mobile` AAR). Home / Connections / Edit / Scan / Settings / Log / About. Named profiles, Keystore-sealed JSON, `loft://` and `.loft` import, QS tile, Always-on via system Settings.

Path: `ConnectivityManager.NetworkCallback` → `setUnderlyingNetworks` + `NotifyPathChange`. Dual-stack from Snapshot (`IP` / `IP6`). `protect(fd)` plus FrontIP `excludeRoute` (API 33+) and positive-route subtract. Split: default route, LAN bypass, CIDR through/bypass/drop. Fronts in the editor: Auto · GET · WS · H2 · QUIC · H3 · L3 (`connect` is node-only).

## Out of scope

- Kotlin / Compose. gomobile already emits Java.
- Spawning `loft-front`.
- SOCKS5 as the inbound other apps use.
- Several VpnServices at once (Android allows one).
- IKEv2 / WireGuard inside this app.
- **VIDAA, Tizen, webOS, Roku.** No TUN.
- Play Store listing in the first shipping build (sideload is enough).
- Dist / APK on the `v*` GitHub Release until a later tag.

## Remaining

### 4. Android TV / Google TV

UX mock: [`design/tv/`](design/tv/). Product flavor `tv` is not in the tree yet (Leanback, D-pad, banner, `LEANBACK_LAUNCHER`). Same `engine.aar` and `LoftVpnService`.

- 10-foot Home / list / settings. Phone layouts stay in the phone flavor.
- No TV camera: QR on the TV (phone scans the screen), `.loft` from USB / Downloads, paste from the remote keyboard.
- Measure RSS on a 2 GiB box before promising cheap sticks. Shield / Google TV Streamer first.

### 5. Package

Signed APK / AAB. Optional GitHub Actions later; not in the first tag. Sideload on a Pixel and a Shield is enough to dogfood.

## Risks

| Risk | Mitigation |
|------|------------|
| Go RSS on a 1–2 GiB TV box | Measure on Shield first. Drop unused transports from the bind if needed. |
| Hairpin | `protect(fd)` plus `excludeRoute` (API 33+) plus FrontIP subtract. |
| OEM boxes without the VPN consent sheet | Document; full tunnel may not start. Not a loft bug. |
| Play TV search | Leanback category on the `tv` flavor only. |

## Done when

**Phone.** A member on API 26+ imports a `.loft` or a `loft://` URI, taps Connect, sees the lamp, and Chrome uses the overlay IP. Wi-Fi → LTE keeps that IP.

**TV.** The same session on Android TV / Google TV with the remote: import without a TV camera, Connect, YouTube uses the overlay IP. Ethernet → Wi-Fi keeps that IP.

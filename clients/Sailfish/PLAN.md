# Plan · loft on Sailfish OS

Software 1.0.0. Protocol 1.0. This plan does not raise either version.

## Goal

A loft / SSTP / VLESS / Trojan client on a **Sailfish OS** phone as a system overlay session. The user imports a member key (`loft://`, `sstp://`, `vless://`, `trojan://`, `.loft`), taps Connect, and device traffic goes through the session. On loft, Wi-Fi / LTE changes keep the overlay IP (fresh `0x02`, same `instance_id`).

The OS owns the TUN (`connman-vpnd`). Loft owns the wire (fronts). The GUI is Qt/QML Silica.

**Aurora OS** is the OMP fork of this client. Same `src/`, `qml/`, `connman/`. Packaging lives in [`aurora/`](aurora/) (`app.loft.aurora`, Aurora SDK). Sailfish packaging is [`sailfish/`](sailfish/) (`harbour-loft`).

## Out of scope

- gomobile / `clients/mobile`. Sailfish is GNU/Linux; the session is `loft-front`, same as macOS.
- Qt Widgets. Sailfish ships Qt Quick + Silica; Widgets are not in the public API.
- Flutter as the first GUI. Native QML first; Flutter MAY come later.
- SOCKS5 as the inbound other apps use. Phase 1 may bind SOCKS for dogfood; the product inbound is ConnMan TUN.
- Several overlay sessions at once (ConnMan holds one active VPN row).
- IKEv2 / OpenVPN / WireGuard inside this app. Those stay stock Settings types. SSTP is in-process Go on the same loft-front when wired.
- Harbour / Jolla store / Aurora Market listing in the first shipping build (sideload RPM is enough).
- Dist / RPM on the `v*` GitHub Release until a later tag.

## Phases

### 0. Catalog — this tree

Docs, plan, HTML mock ([design/](design/)). Thin QML host pastes YAML.

### 1. Thin host + loft-front

Cross `loft-front` `linux/arm64` (and `arm` if a device needs it). The app writes `client.yaml` and starts the helper. SOCKS on loopback for a browser check. No TUN yet.

Smoke: Hello, overlay IP in the app, curl through SOCKS.

### 2. ConnMan VPN plugin — written

ConnMan `.so` (`loft-plugin.c`) + helper `loft-connman` (execs `loft-front -tun NAME -tun-noexec`) + `loft-script` notify with overlay IP / DNS / MTU. `connman-vpnd` creates the tun and routes. Hairpin: `Host` → `trusted_ip` (front off the tun), same idea as macOS.

Settings → Networking loads `settings/{listitem,edit,advanced,details}.qml` so the type is **Loft**. The QML host prefers D-Bus `net.connman.vpn` Create/Connect; SOCKS `loft-front` is the fallback when the plugin is absent.

Smoke (needs a device): ping overlay gateway; the stock browser uses the overlay IP. The `.so` builds in the Sailfish SDK (`connman-devel`); Aurora SDK is the same plugin with `-DLOFT_OS=aurora`.

### 3. Profiles and machine policy

YAML profiles under the app data dir (`harbour-loft` or `app.loft.aurora`). Import/export `.loft`. One active profile (one ConnMan row).

- Hold: ConnMan default route stays claimed while the session is down (copy: *route* / *direct Internet*).
- Split: zone lists → ConnMan `via` / `direct` routes. Direction lives on the zone.
- Scan: camera QR + file. Detector: `loft://`, `sstp://`, `vless://`, `trojan://`, YAML / `.loft`. URL scheme `loft`. Confirm then save. List subtitle kind · transport · host.
- Cover: lamp + Connect/Disconnect.

### 4. Package

Signed RPM for aarch64 (and armv7hl if a device needs it).

- Sailfish: `harbour-loft`, Sailfish SDK (`sfdk`). Sideload / OpenRepos first. Harbour listing later (privileged plugin may ship as a second RPM).
- Aurora: `app.loft.aurora`, Aurora SDK (`mb2` / Aurora IDE). Sideload on a developer device.

Optional GitHub Actions later; not in the first tag.

## Order of work in the Go tree

1. Keep `loft-front` as the session process. Generic `-tun NAME` + `-tun-noexec` (attach, skip addr/routes). No Sailfish-only or Aurora-only flags.
2. Cross: `GOOS=linux GOARCH=arm64 CGO_ENABLED=0` from this Makefile. If the device glibc rejects the binary, rebuild inside the Sailfish or Aurora Build Engine.
3. Path change: ConnMan / Qt Network notify → loft-front already resumes with a fresh `0x02` and the same `instance_id`. Do not add caps.
4. Do not bind `clients/mobile` for this client.

## Risks

| Risk | Mitigation |
|------|------------|
| Regular sandbox cannot open TUN | Phase 2 uses the ConnMan plugin path; the GUI never opens `/dev/net/tun`. |
| Plugin needs a privileged RPM | Document the security profile; sideload first. Harbour may need a split GUI/plugin RPM. |
| glibc / Go static binary | Try `CGO_ENABLED=0` first; fall back to SDK gcc+cgo. |
| Hairpin of GET/WS into the tun | Exclude front IPs before default route, as on macOS. |
| Market moderation | Sideload until a later tag. Overlay copy stays loft, not a third-party VPN brand. |
| No device | Emulator for QML; TUN smoke needs hardware. |

## Done when

A member on Sailfish OS imports a `.loft` or a `loft://` URI, taps Connect, sees the lamp, and the stock browser uses the overlay IP. Switching Wi-Fi to LTE keeps that overlay IP. The Aurora RPM is the same tree with `-DLOFT_OS=aurora`.

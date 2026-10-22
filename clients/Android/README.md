# loft · Android

**English** · [Русский](README_ru.md) · [中文](README_zh.md)

Phone app + `VpnService`. The session is Go [`clients/mobile`](../mobile/). **Java 17**, not Kotlin: gomobile already emits Java.

Protocol: [docs/protocol.md](../../docs/protocol.md). Integration: [docs/android.md](../../docs/android.md). Import URI: [docs/share.md](../../docs/share.md). Plan: [PLAN.md](PLAN.md). UX: [design/DESIGN.md](design/DESIGN.md). Phone mock: [design/index.html](design/index.html). TV mock: [design/tv/](design/tv/).

The overlay is a first-class Android network: one system VPN row, `protect(fd)` on every front socket, `NetworkCallback` → `NotifyPathChange` (fresh `0x02`, same `instance_id`). Dual-stack from Snapshot. Fronts: get, ws, h2, quic, h3, l3. `connect` is node-only.

## Layout

```
clients/Android/
  PLAN.md                  phone done; Android TV mock in design/tv; Leanback flavor later
  design/                  phone UX spec and HTML mock
  design/tv/               10-foot UX spec and HTML mock
  emulator/                macOS loft.app payload (copied into dist)
  app/src/main/java/app/loft/android/
    LoftVpnService.java    tun ↔ Engine, path, dual-stack, protect
    MainActivity.java      Home / Connections / Settings
    EditActivity.java      loft / SSTP / VLESS / Trojan
    ScanActivity.java      camera QR, paste, file
  ../mobile/               shared Go engine
```

minSdk **26** (GUI / notification channel). Engine AAR is built for API **24**. targetSdk **36**. arm64 `.so` is 16 KiB page-size compatible.

## Build

```bash
make android                 # from repo root; needs Android NDK
# or: make -C clients/Android
```

Writes `dist/clients/Android/app-debug.apk` and `loft.app`. Intermediates stay under `dist/clients/Android/gradle/`, not in `clients/Android/`. Double-click `loft.app` to boot AVD `loft-phone`, install that APK, pin loft and Browser on the home screen, and start loft. AOSP has no Chrome; Browser is WebView Shell. Intel Mac + AMD GPU uses SwiftShader (`-gpu swiftshader_indirect`); host GLES leaves a black window.

Ids: `app.loft.android`. VPN type: `VpnService`. One system VPN row.

# loft · Android

**English** · [Русский](README_ru.md) · [中文](README_zh.md)

Phone app + `VpnService`. The session is Go [`clients/mobile`](../mobile/). **Java 17**, not Kotlin: gomobile already emits Java.

Protocol: [docs/protocol.md](../../docs/protocol.md). Integration: [docs/android.md](../../docs/android.md). Import URI: [docs/share.md](../../docs/share.md). UX: [design/DESIGN.md](design/DESIGN.md). Phone mock: [design/index.html](design/index.html). TV mock: [design/tv/](design/tv/).

The overlay is a first-class Android network: one system VPN row, `protect(fd)` on every front socket, `NetworkCallback` → `NotifyPathChange` (fresh `0x02`, same `instance_id`). Dual-stack from Snapshot. Fronts: get, ws, h2, quic, h3, l3. `connect` is node-only.

## Layout

```
clients/Android/
  design/                  phone UX spec and HTML mock
  design/tv/               10-foot UX spec and HTML mock
  emulator/                macOS loft.app / loft-tv.app / loft-gtv.app payload
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

Writes `dist/clients/Android/app-debug.apk`, `app-tv-debug.apk`, `loft.app`, `loft-tv.app`, and `loft-gtv.app`. Intermediates stay under `dist/clients/Android/gradle/`, not in `clients/Android/`. Three macOS launchers, one AVD each; serial is pinned to that AVD so they can run together. Arrow keys are the D-pad on TV.

| App | AVD | Image | APK | Activity |
|--|--|--|--|--|
| `loft.app` | `loft-phone` | AOSP phone API 34 | phone | `MainActivity` — pins loft, Browser, Apps, Settings |
| `loft-tv.app` | `loft-tv` | AOSP Android TV API 36 x86_64, `tv_1080p` | TV | `TvActivity` |
| `loft-gtv.app` | `loft-gtv` | Google TV API 36 x86_64, `tv_1080p` | TV | `TvActivity` |

Create the TV AVDs once: `make -C clients/Android avds` (`emulator/setup-avds`). AOSP has no Chrome; Browser on the phone image is WebView Shell. Intel Mac + AMD GPU uses SwiftShader (`-gpu swiftshader_indirect`); host GLES leaves a black window. Logs: `~/Library/Logs/loft-emulator.log`, `loft-tv-emulator.log`, `loft-gtv-emulator.log`.

Ids: phone `app.loft.android`, TV `app.loft.android.tv`. VPN type: `VpnService`. One system VPN row per device. Flavor `tv` is Leanback (`LEANBACK_LAUNCHER`). Channel `github` (default) is Dist/sideload with an in-app updater; `store` is Play and has that code compiled out. `make -C clients/Android release` signs with `secrets/android-github.p12` and writes `loft-{ver}-android.apk` plus `loft-{ver}-android-tv.apk`. First key: `make -C clients/Android github-key`.

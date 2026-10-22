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
make -C ../mobile android    # needs Android NDK; writes ../mobile/build/engine.aar
# Android Studio → open clients/Android
# or: gradle :app:assembleDebug
```

Ids: `app.loft.android`. VPN type: `VpnService`. One system VPN row.

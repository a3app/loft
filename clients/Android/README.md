# loft · Android

**English** · [Русский](README_ru.md) · [中文](README_zh.md)

Thin host + `VpnService`. The session is Go [`clients/mobile`](../mobile/). **Java**, not Kotlin: gomobile already emits Java; this process does not load the Kotlin runtime.

Protocol: [docs/protocol.md](../../docs/protocol.md). Integration: [docs/android.md](../../docs/android.md). Import URI: [docs/share.md](../../docs/share.md). Plan: [PLAN.md](PLAN.md). UX: [design/DESIGN.md](design/DESIGN.md). Mock: [design/index.html](design/index.html).

## Layout

```
clients/Android/
  PLAN.md                  phone, then Android TV / Google TV
  design/                  UX spec and HTML mock
  app/src/main/java/app/loft/android/
    MainActivity.java      paste YAML / URI, Connect / Disconnect
    LoftVpnService.java    tun fd ↔ Engine, protect(fd)
  ../mobile/               shared Go engine
```

minSdk **26** (GUI / notification channel). Engine AAR is built for API **24**. targetSdk **36**. arm64 `.so` is 16 KiB page-size compatible.

## Build

```bash
make -C ../mobile android    # needs Android NDK; writes ../mobile/build/engine.aar
# then Android Studio → open clients/Android
# or: gradle :app:assembleDebug
```

Ids: `app.loft.android`. VPN type: `VpnService`. One system VPN row.

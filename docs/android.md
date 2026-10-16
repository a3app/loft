# loft on Android

**English** · [Русский](android_ru.md)

How loft runs on a phone, then Android TV / Google TV. Catalog: [clients/Android/](../clients/Android/). Plan: [clients/Android/PLAN.md](../clients/Android/PLAN.md). Engine: [clients/mobile/](../clients/mobile/). Protocol: [protocol.md](protocol.md). Import URI: [share.md](share.md). UX: [clients/Android/design/DESIGN.md](../clients/Android/design/DESIGN.md). Mock: [clients/Android/design/index.html](../clients/Android/design/index.html).

The adapter is Java. The session is Go.

---

## 1. One process

```
apps
  → tun0 owned by VpnService
      → LoftVpnService  (app.loft.android)
          → clients/mobile  (gomobile AAR)
              → loft / SSTP / VLESS / Trojan
              → protect(fd) on front sockets
```

There is no `loft-front`. The service *is* the session.

## 2. Bind

`make -C clients/mobile android` writes `clients/mobile/build/engine.aar` (`-androidapi 24`, 16 KiB pages). The app depends on that file. NDK is required for the bind.

Java package `app.loft.engine`: `Engine`, `Snapshot`, `PacketSink`, `SocketProtector`, `Mobile.parseShare`.

## 3. Hairpin

`VpnService.protect(fd)` via `Engine.SetProtector`. Front IPs also `excludeRoute` on API 33+.

Path change: `ConnectivityManager.NetworkCallback` can call `NotifyPathChange` later; the thin adapter relies on Go reconnect after a dead socket for now.

## 4. Floors

| | |
|--|--|
| Engine AAR | API **24** |
| Phone app | API **26**, target **36** |
| Language | Java 17 |
| Android TV / Google TV | same AAR; Leanback flavor later ([PLAN](../clients/Android/PLAN.md) phase 4) |

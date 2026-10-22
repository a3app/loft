# loft on Android

**English** · [Русский](android_ru.md) · [中文](android_zh.md)

How loft runs on a phone, then Android TV / Google TV. Catalog: [clients/Android/](../clients/Android/). Plan: [clients/Android/PLAN.md](../clients/Android/PLAN.md). Engine: [clients/mobile/](../clients/mobile/). Protocol: [protocol.md](protocol.md). Import URI: [share.md](share.md). UX: [clients/Android/design/DESIGN.md](../clients/Android/design/DESIGN.md). Phone mock: [clients/Android/design/index.html](../clients/Android/design/index.html). TV mock: [clients/Android/design/tv/](../clients/Android/design/tv/).

The adapter is Java 17 Views. The session is Go. The overlay is a first-class Android network (one VPN row): `protect(fd)`, `NetworkCallback` → `NotifyPathChange`, dual-stack from Snapshot.

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

Java package `app.loft.engine`: `Engine`, `Snapshot`, `PacketSink`, `SocketProtector`, `Mobile.parseShare`, `Mobile.parseShareAll`. Snapshot carries `IP` / `IP6`, `Prefix` / `Prefix6`, `Routes`, `FrontIPs`.

## 3. Hairpin and path

`VpnService.protect(fd)` via `Engine.SetProtector`. Front IPs are subtracted from TUN routes and also `excludeRoute` on API 33+.

Path change: `ConnectivityManager.NetworkCallback` on the default network. The service calls `setUnderlyingNetworks` then `NotifyPathChange`. loft: fresh `0x02`, same `instance_id`. Overlay IP stays. SSTP/VLESS/Trojan redial; if IPCP issues a new address the TUN is rebuilt.

## 4. Floors

| | |
|--|--|
| Engine AAR | API **24** |
| Phone app | API **26**, target **36** |
| Language | Java 17 |
| Android TV / Google TV | same AAR; 10-foot mock [`design/tv/`](../clients/Android/design/tv/); Leanback flavor later ([PLAN](../clients/Android/PLAN.md) phase 4) |

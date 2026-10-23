# loft on Android

**English** · [Русский](android_ru.md) · [中文](android_zh.md)

How loft runs on a phone, then Android TV / Google TV. Catalog: [clients/Android/](../clients/Android/). Engine: [clients/mobile/](../clients/mobile/). Protocol: [protocol.md](protocol.md). Import URI: [share.md](share.md). UX: [clients/Android/design/DESIGN.md](../clients/Android/design/DESIGN.md). Phone mock: [clients/Android/design/index.html](../clients/Android/design/index.html). TV mock: [clients/Android/design/tv/](../clients/Android/design/tv/).

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

There is no `loft-front`. The service *is* the session. It starts as a foreground service of type `systemExempted` (VpnService on API 34+).

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
| Android TV / Google TV | same AAR; flavor `tv` (`app.loft.android.tv`); 10-foot GUI [`design/tv/`](../clients/Android/design/tv/) |

## 5. Emulator

`make android` writes `loft.app` next to `app-debug.apk` in [`dist/clients/Android/`](../dist/clients/Android/). Gradle intermediates stay there under `gradle/`, not in `clients/Android/`. Double-click `loft.app` on macOS: boot AVD `loft-phone`, `adb install -r` that APK, pin loft and Browser (AOSP WebView Shell) on the home screen, start `app.loft.android/.MainActivity`. Payload lives in [`clients/Android/emulator/`](../clients/Android/emulator/). SDK: `ANDROID_HOME` or `/usr/local/share/android-commandlinetools`. Log: `~/Library/Logs/loft-emulator.log`. On Intel Mac with an AMD GPU the window stays black with `-gpu auto` (gfxstream/host GLES); `loft.app` starts the AVD with `-gpu swiftshader_indirect`.

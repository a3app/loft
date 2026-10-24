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

Path change: `ConnectivityManager.NetworkCallback` on the default network. The service calls `setUnderlyingNetworks` then `NotifyPathChange`. GET rebinds the HTTP/2 pool (same sid). Other loft fronts Park; resume is a fresh `0x02` and the same `instance_id`. Overlay IP stays. Extra mux fronts join GET with `0x04`. SSTP/VLESS/Trojan redial; if IPCP issues a new address the TUN is rebuilt.

## 4. Floors

| | |
|--|--|
| Engine AAR | API **24** |
| Phone app | API **26**, target **36** |
| Language | Java 17 |
| Android TV / Google TV | same AAR; flavor `tv` (`app.loft.android.tv`); 10-foot GUI [`design/tv/`](../clients/Android/design/tv/) |
| Channel | `github` (Dist / sideload, in-app update) or `store` (Play; updater compiled out) |

## 5. Emulator

`make android` writes `loft.app`, `loft-tv.app`, and `loft-gtv.app` next to `app-debug.apk` and `app-tv-debug.apk` in [`dist/clients/Android/`](../dist/clients/Android/). Gradle intermediates stay there under `gradle/`, not in `clients/Android/`. Payload lives in [`clients/Android/emulator/`](../clients/Android/emulator/). SDK: `ANDROID_HOME` or `/usr/local/share/android-commandlinetools`. On Intel Mac with an AMD GPU the window stays black with `-gpu auto` (gfxstream/host GLES); the launchers start each AVD with `-gpu swiftshader_indirect`. Serial is matched by AVD name, so the three can run together. TV arrows are the D-pad. Create TV AVDs with `make -C clients/Android avds`.

| App | AVD | Image | Starts |
|--|--|--|--|
| `loft.app` | `loft-phone` | AOSP phone API 34 | `app.loft.android/.MainActivity`; pins loft, Browser (WebView Shell), Apps, Settings |
| `loft-tv.app` | `loft-tv` | AOSP Android TV API 36 x86_64 | `app.loft.android.tv/app.loft.android.TvActivity` |
| `loft-gtv.app` | `loft-gtv` | Google TV API 36 x86_64 | `app.loft.android.tv/app.loft.android.TvActivity` |

Logs: `~/Library/Logs/loft-emulator.log`, `loft-tv-emulator.log`, `loft-gtv-emulator.log`.

## 6. Channels

Two product flavors on the `channel` dimension, same `applicationId`, different signing keys.

| | `github` (default, Dist) | `store` |
|--|--|--|
| In-app update | Settings / About → Check. GitHub Releases, SHA-256, `PackageInstaller`. Install-unknown-apps once; later sessions silent (`USER_ACTION_NOT_REQUIRED`, update-ownership). Auto-check every 6 h on unmetered. | compiled out |
| Permission | `REQUEST_INSTALL_PACKAGES` | none |
| Key | `secrets/android-github.p12` (`make -C clients/Android github-key`) | Play App Signing |

`make -C clients/Android release` writes `loft-{ver}-android.apk`, `loft-{ver}-android-tv.apk`, and `loft-android-{ver}-SHA256SUMS.txt`. Dist uploads those names on a `v*` tag when GitHub secrets `LOFT_ANDROID_KEYSTORE` (base64 of the p12) and `LOFT_ANDROID_KEYSTORE_PASSWORD` are set. Debug builds can check; they refuse to install a release APK (different cert).

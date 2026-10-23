# loft на Android

[English](android.md) · **Русский** · [中文](android_zh.md)

Как loft работает на телефоне, затем на Android TV / Google TV. Каталог: [clients/Android/](../clients/Android/). Движок: [clients/mobile/](../clients/mobile/). Протокол: [protocol.md](protocol.md). URI импорта: [share.md](share.md). UX: [clients/Android/design/DESIGN_ru.md](../clients/Android/design/DESIGN_ru.md). Макет телефона: [clients/Android/design/index.html](../clients/Android/design/index.html). Макет TV: [clients/Android/design/tv/](../clients/Android/design/tv/).

Адаптер — Java 17 Views. Сессия — Go. Overlay — обычная сеть Android (одна строка VPN): `protect(fd)`, `NetworkCallback` → `NotifyPathChange`, dual-stack из Snapshot.

---

## 1. Один процесс

```
приложения
  → tun0 у VpnService
      → LoftVpnService  (app.loft.android)
          → clients/mobile  (gomobile AAR)
              → loft / SSTP / VLESS / Trojan
              → protect(fd) на сокетах фронта
```

Процесса `loft-front` нет. Служба *и есть* сессия. Стартует как foreground service типа `systemExempted` (VpnService на API 34+).

## 2. Bind

`make -C clients/mobile android` пишет `clients/mobile/build/engine.aar` (`-androidapi 24`, страницы 16 КиБ). Приложение зависит от этого файла. Для bind нужен NDK.

Java-пакет `app.loft.engine`: `Engine`, `Snapshot`, `PacketSink`, `SocketProtector`, `Mobile.parseShare`, `Mobile.parseShareAll`. Snapshot несёт `IP` / `IP6`, `Prefix` / `Prefix6`, `Routes`, `FrontIPs`.

## 3. Петля и путь

`VpnService.protect(fd)` через `Engine.SetProtector`. IP фронтов вычитаются из маршрутов TUN и ещё `excludeRoute` на API 33+.

Смена пути: `ConnectivityManager.NetworkCallback` на сеть по умолчанию. Служба зовёт `setUnderlyingNetworks`, затем `NotifyPathChange`. loft: свежий `0x02`, тот же `instance_id`. Overlay IP остаётся. SSTP/VLESS/Trojan — повторный набор; если IPCP выдал новый адрес, TUN собирается заново.

## 4. Полы

| | |
|--|--|
| AAR движка | API **24** |
| Приложение телефона | API **26**, target **36** |
| Язык | Java 17 |
| Android TV / Google TV | тот же AAR; flavor `tv` (`app.loft.android.tv`); GUI с дивана [`design/tv/`](../clients/Android/design/tv/) |
| Канал | `github` (Dist / sideload, обновление из приложения) или `store` (Play; код обновления вырезан) |

## 5. Эмулятор

`make android` пишет `loft.app` рядом с `app-debug.apk` в [`dist/clients/Android/`](../dist/clients/Android/). Промежуточное Gradle — там же в `gradle/`, не в `clients/Android/`. На macOS двойной щелчок: поднять AVD `loft-phone`, `adb install -r` этот APK, положить loft и Browser (AOSP WebView Shell) на домашний экран, запустить `app.loft.android/.MainActivity`. Содержимое — [`clients/Android/emulator/`](../clients/Android/emulator/). SDK: `ANDROID_HOME` или `/usr/local/share/android-commandlinetools`. Журнал: `~/Library/Logs/loft-emulator.log`. На Intel Mac с AMD GPU окно остаётся чёрным при `-gpu auto` (gfxstream/host GLES); `loft.app` поднимает AVD с `-gpu swiftshader_indirect`.

## 6. Каналы

Два product flavor по dimension `channel`, один `applicationId`, разные ключи подписи.

| | `github` (по умолчанию, Dist) | `store` |
|--|--|--|
| Обновление | Настройки / О программе → Проверить. GitHub Releases, SHA-256, `PackageInstaller`. Установка из неизвестных источников — один раз; дальше тихо (`USER_ACTION_NOT_REQUIRED`, update-ownership). Автопроверка раз в 6 ч на безлимите. | кода нет |
| Право | `REQUEST_INSTALL_PACKAGES` | нет |
| Ключ | `secrets/android-github.p12` (`make -C clients/Android github-key`) | Play App Signing |

`make -C clients/Android release` пишет `loft-{ver}-android.apk`, `loft-{ver}-android-tv.apk` и `loft-android-{ver}-SHA256SUMS.txt`. Dist заливает те же имена на тег `v*`, если заданы секреты `LOFT_ANDROID_KEYSTORE` (base64 от p12) и `LOFT_ANDROID_KEYSTORE_PASSWORD`. Стабильные имена: `loft-android.apk`, `loft-android-tv.apk`, `loft-android-SHA256SUMS.txt`. Debug-сборка умеет проверить и не ставит релизный APK (другой сертификат).

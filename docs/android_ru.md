# loft на Android

[English](android.md) · **Русский** · [中文](android_zh.md)

Как loft работает на телефоне, а затем на Android TV / Google TV. Каталог: [clients/Android/](../clients/Android/). Движок: [clients/mobile/](../clients/mobile/). Протокол: [protocol_ru.md](protocol_ru.md). URI импорта: [share_ru.md](share_ru.md). UX: [clients/Android/design/DESIGN_ru.md](../clients/Android/design/DESIGN_ru.md). Макет телефона: [clients/Android/design/index.html](../clients/Android/design/index.html). Макет TV: [clients/Android/design/tv/](../clients/Android/design/tv/).

Адаптер написан на Java 17 Views, сессия — на Go. Оверлей — полноценная сеть Android (одна строка VPN): `protect(fd)`, `NetworkCallback` → `NotifyPathChange`, dual-stack из Snapshot.

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

Процесса `loft-front` нет. Служба *и есть* сессия. Она запускается как foreground service типа `systemExempted` (VpnService на API 34+).

## 2. Bind

`make -C clients/mobile android` собирает `clients/mobile/build/engine.aar` (`-androidapi 24`, страницы 16 КиБ). Приложение зависит от этого файла. Для bind нужен NDK.

Java-пакет `app.loft.engine`: `Engine`, `Snapshot`, `PacketSink`, `SocketProtector`, `Mobile.parseShare`, `Mobile.parseShareAll`. Snapshot содержит `IP` / `IP6`, `Prefix` / `Prefix6`, `Routes`, `FrontIPs`.

## 3. Петля и путь

`VpnService.protect(fd)` через `Engine.SetProtector`. IP фронтов вычитаются из маршрутов TUN, а на API 33+ дополнительно передаются в `excludeRoute`.

Смена пути отслеживается через `ConnectivityManager.NetworkCallback` на сети по умолчанию. Служба вызывает `setUnderlyingNetworks`, затем `NotifyPathChange`. GET пересобирает пул HTTP/2 (sid тот же). Остальные loft-фронты переходят в Park и затем возобновляются свежим `0x02` с тем же `instance_id`. IP оверлея сохраняется. Лишние mux-фронты присоединяются к GET через `0x04`. SSTP/VLESS/Trojan переподключаются заново; если IPCP выдал новый адрес, TUN пересобирается.

## 4. Минимальные версии

| | |
|--|--|
| AAR движка | API **24** |
| Приложение телефона | API **26**, target **36** |
| Язык | Java 17 |
| Android TV / Google TV | тот же AAR; flavor `tv` (`app.loft.android.tv`); «диванный» GUI [`design/tv/`](../clients/Android/design/tv/) |
| Канал | `github` (Dist / sideload, обновление из приложения) или `store` (Play; код обновления вырезан) |

## 5. Эмулятор

`make android` собирает `loft.app`, `loft-tv.app` и `loft-gtv.app` рядом с `app-debug.apk` и `app-tv-debug.apk` в [`dist/clients/Android/`](../dist/clients/Android/). Промежуточные файлы Gradle лежат там же, в `gradle/`, а не в `clients/Android/`. Содержимое лежит в [`clients/Android/emulator/`](../clients/Android/emulator/). SDK: `ANDROID_HOME` или `/usr/local/share/android-commandlinetools`. На Intel Mac с AMD GPU окно остаётся чёрным при `-gpu auto` (gfxstream/host GLES); лаунчеры поднимают каждый AVD с `-gpu swiftshader_indirect`. Serial подбирается по имени AVD, поэтому все три эмулятора можно запускать одновременно. Стрелки на TV работают как D-pad. AVD для TV создаются командой `make -C clients/Android avds`.

| App | AVD | Образ | Запуск |
|--|--|--|--|
| `loft.app` | `loft-phone` | AOSP телефон API 34 | `app.loft.android/.MainActivity`; loft, Browser (WebView Shell), Apps, Settings на домашнем экране |
| `loft-tv.app` | `loft-tv` | AOSP Android TV API 36 x86_64 | `app.loft.android.tv/app.loft.android.TvActivity` |
| `loft-gtv.app` | `loft-gtv` | Google TV API 36 x86_64 | `app.loft.android.tv/app.loft.android.TvActivity` |

Журналы: `~/Library/Logs/loft-emulator.log`, `loft-tv-emulator.log`, `loft-gtv-emulator.log`.

## 6. Каналы поставки

Два product flavor в измерении `channel`: один `applicationId`, разные ключи подписи.

| | `github` (по умолчанию, Dist) | `store` |
|--|--|--|
| Обновление | Настройки / О программе → Проверить. GitHub Releases, SHA-256, `PackageInstaller`. Разрешение на установку из неизвестных источников даётся один раз, дальше обновления ставятся тихо (`USER_ACTION_NOT_REQUIRED`, update-ownership). Автопроверка раз в 6 ч в безлимитной сети. | кода нет |
| Право | `REQUEST_INSTALL_PACKAGES` | нет |
| Ключ | `secrets/android-github.p12` (`make -C clients/Android github-key`) | Play App Signing |

`make -C clients/Android release` собирает `loft-{ver}-android.apk`, `loft-{ver}-android-tv.apk` и `loft-android-{ver}-SHA256SUMS.txt`. Dist выкладывает файлы с теми же именами по тегу `v*`, если заданы секреты `LOFT_ANDROID_KEYSTORE` (base64 от p12) и `LOFT_ANDROID_KEYSTORE_PASSWORD`. Debug-сборка умеет проверять обновления, но релизный APK не ставит (другой сертификат).

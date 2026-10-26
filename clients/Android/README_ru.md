# loft · Android

[English](README.md) · **Русский** · [中文](README_zh.md)

Приложение для телефона + `VpnService`. Сессию ведёт Go-код из [`clients/mobile`](../mobile/). **Java 17**, не Kotlin: gomobile уже отдаёт Java.

Протокол: [docs/protocol.md](../../docs/protocol.md). Интеграция: [docs/android.md](../../docs/android.md). URI импорта: [docs/share.md](../../docs/share.md). UX: [design/DESIGN_ru.md](design/DESIGN_ru.md). Макет телефона: [design/index.html](design/index.html). Макет TV: [design/tv/](design/tv/).

Оверлей — полноценная сеть Android: одна системная строка VPN, `protect(fd)` на каждом сокете фронта, `NetworkCallback` → `NotifyPathChange` (свежий `0x02`, тот же `instance_id`). Dual-stack берётся из Snapshot. Фронты: get, ws, h2, quic, h3, l3. `connect` есть только на ноде.

## Дерево

```
clients/Android/
  design/                  спецификация UX и HTML-макет телефона
  design/tv/               макет с дивана
  emulator/                содержимое macOS loft.app / loft-tv.app / loft-gtv.app
  app/src/main/java/app/loft/android/
    LoftVpnService.java    tun ↔ Engine, путь, dual-stack, protect
    MainActivity.java      Главная / Подключения / Настройки
    EditActivity.java      loft / SSTP / VLESS / Trojan
    ScanActivity.java      QR с камеры, вставка, файл
  ../mobile/               общий движок Go
```

minSdk **26** (GUI / канал уведомлений). AAR движка собирается под API **24**. targetSdk **36**. `.so` для arm64 совместимы со страницами 16 КиБ.

## Сборка

```bash
make android                 # из корня репозитория; нужен Android NDK
# или: make -C clients/Android
```

Результат: `dist/clients/Android/app-debug.apk`, `app-tv-debug.apk`, `loft.app`, `loft-tv.app` и `loft-gtv.app`. Промежуточные файлы остаются в `dist/clients/Android/gradle/`, а не в `clients/Android/`. Три лаунчера для macOS, у каждого свой AVD; serial закреплён за своим AVD, поэтому их можно запускать одновременно. На TV стрелки работают как D-pad.

| App | AVD | Образ | APK | Activity |
|--|--|--|--|--|
| `loft.app` | `loft-phone` | AOSP телефон API 34 | телефон | `MainActivity`; на домашнем экране закреплены loft, Browser, Apps, Settings |
| `loft-tv.app` | `loft-tv` | AOSP Android TV API 36 x86_64, `tv_1080p` | TV | `TvActivity` |
| `loft-gtv.app` | `loft-gtv` | Google TV API 36 x86_64, `tv_1080p` | TV | `TvActivity` |

AVD для TV создаются один раз: `make -C clients/Android avds` (`emulator/setup-avds`). В AOSP нет Chrome; в образе телефона роль Browser играет WebView Shell. На Intel Mac с AMD GPU используется SwiftShader (`-gpu swiftshader_indirect`): с host GLES окно остаётся чёрным. Журналы: `~/Library/Logs/loft-emulator.log`, `loft-tv-emulator.log`, `loft-gtv-emulator.log`.

Id: телефон `app.loft.android`, TV `app.loft.android.tv`. Тип VPN: `VpnService`. Одна системная строка VPN на устройство. Flavor `tv` — Leanback. Канал `github` (по умолчанию) — Dist/sideload с обновлением из самого приложения; `store` — для Play, код обновления из него вырезан. `make -C clients/Android release` подписывает сборку ключом `secrets/android-github.p12` и выдаёт `loft-{ver}-android.apk` и `loft-{ver}-android-tv.apk`. Первый ключ: `make -C clients/Android github-key`.

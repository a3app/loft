# loft · Android

[English](README.md) · **Русский** · [中文](README_zh.md)

Приложение телефона + `VpnService`. Сессия — Go [`clients/mobile`](../mobile/). **Java 17**, не Kotlin: gomobile уже отдаёт Java.

Протокол: [docs/protocol.md](../../docs/protocol.md). Интеграция: [docs/android.md](../../docs/android.md). URI импорта: [docs/share.md](../../docs/share.md). UX: [design/DESIGN_ru.md](design/DESIGN_ru.md). Макет телефона: [design/index.html](design/index.html). Макет TV: [design/tv/](design/tv/).

Overlay — обычная сеть Android: одна системная строка VPN, `protect(fd)` на каждом сокете фронта, `NetworkCallback` → `NotifyPathChange` (свежий `0x02`, тот же `instance_id`). Dual-stack из Snapshot. Фронты: get, ws, h2, quic, h3, l3. `connect` — только на ноде.

## Дерево

```
clients/Android/
  design/                  спецификация UX и HTML-макет телефона
  design/tv/               макет с дивана
  emulator/                содержимое macOS loft.app (копируется в dist)
  app/src/main/java/app/loft/android/
    LoftVpnService.java    tun ↔ Engine, путь, dual-stack, protect
    MainActivity.java      Главная / Подключения / Настройки
    EditActivity.java      loft / SSTP / VLESS / Trojan
    ScanActivity.java      QR с камеры, вставка, файл
  ../mobile/               общий движок Go
```

minSdk **26** (GUI / канал уведомлений). AAR движка собирается под API **24**. targetSdk **36**. `.so` arm64 — страницы 16 КиБ.

## Сборка

```bash
make android                 # из корня репозитория; нужен Android NDK
# или: make -C clients/Android
```

Пишет `dist/clients/Android/app-debug.apk` и `loft.app`. Промежуточное — в `dist/clients/Android/gradle/`, не в `clients/Android/`. Двойной щелчок по `loft.app` поднимает AVD `loft-phone`, ставит этот APK, кладёт loft, Browser, Apps и Settings на домашний экран и открывает loft. Apps открывает список приложений лаунчера; Settings — настройки ОС. В AOSP нет Chrome; Browser — WebView Shell. Intel Mac + AMD GPU — SwiftShader (`-gpu swiftshader_indirect`); host GLES даёт чёрное окно.

Id: телефон `app.loft.android`, TV `app.loft.android.tv`. Тип VPN: `VpnService`. Одна системная строка на устройство. Flavor `tv` — Leanback. Канал `github` (по умолчанию) — Dist/sideload с обновлением из приложения; `store` — Play, код обновления вырезан. `make -C clients/Android release` подписывает `secrets/android-github.p12` и пишет `loft-{ver}-android.apk` и `loft-{ver}-android-tv.apk`. Первый ключ: `make -C clients/Android github-key`.

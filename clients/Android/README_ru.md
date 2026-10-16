# loft · Android

[English](README.md) · **Русский**

Тонкий хост + `VpnService`. Сессия — Go [`clients/mobile`](../mobile/). **Java**, не Kotlin: gomobile уже отдаёт Java; Kotlin runtime в этот процесс не грузим.

Протокол: [docs/protocol.md](../../docs/protocol.md). Интеграция: [docs/android.md](../../docs/android.md). URI импорта: [docs/share.md](../../docs/share.md). План: [PLAN_ru.md](PLAN_ru.md). UX: [design/DESIGN_ru.md](design/DESIGN_ru.md). Макет: [design/index.html](design/index.html).

## Дерево

```
clients/Android/
  PLAN.md                  телефон, затем Android TV / Google TV
  design/                  спецификация UX и HTML-макет
  app/src/main/java/app/loft/android/
    MainActivity.java      YAML / URI, Connect / Disconnect
    LoftVpnService.java    tun fd ↔ Engine, protect(fd)
  ../mobile/               общий движок Go
```

minSdk **26** (GUI / канал уведомлений). AAR движка собирается под API **24**. targetSdk **36**. `.so` arm64 — страницы 16 КиБ.

## Сборка

```bash
make -C ../mobile android    # нужен Android NDK; пишет ../mobile/build/engine.aar
# Android Studio → open clients/Android
# или: gradle :app:assembleDebug
```

Id: `app.loft.android`. Тип VPN: `VpnService`. Одна системная строка VPN.

# loft на Android

[English](android.md) · **Русский** · [中文](android_zh.md)

Как loft работает на телефоне, затем на Android TV / Google TV. Каталог: [clients/Android/](../clients/Android/). План: [clients/Android/PLAN.md](../clients/Android/PLAN.md). Движок: [clients/mobile/](../clients/mobile/). Протокол: [protocol.md](protocol.md). URI импорта: [share.md](share.md). UX: [clients/Android/design/DESIGN_ru.md](../clients/Android/design/DESIGN_ru.md). Макет: [clients/Android/design/index.html](../clients/Android/design/index.html).

Адаптер — Java. Сессия — Go.

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

Процесса `loft-front` нет. Служба *и есть* сессия.

## 2. Bind

`make -C clients/mobile android` пишет `clients/mobile/build/engine.aar` (`-androidapi 24`, страницы 16 КиБ). Приложение зависит от этого файла. Для bind нужен NDK.

Java-пакет `app.loft.engine`: `Engine`, `Snapshot`, `PacketSink`, `SocketProtector`, `Mobile.parseShare`.

## 3. Петля

`VpnService.protect(fd)` через `Engine.SetProtector`. IP фронтов ещё `excludeRoute` на API 33+.

Смена пути: позже `ConnectivityManager.NetworkCallback` → `NotifyPathChange`; тонкий адаптер пока ждёт обрыв сокета и повторный набор в Go.

## 4. Полы

| | |
|--|--|
| AAR движка | API **24** |
| Приложение телефона | API **26**, target **36** |
| Язык | Java 17 |
| Android TV / Google TV | тот же AAR; Leanback flavor позже ([план](../clients/Android/PLAN.md) фаза 4) |

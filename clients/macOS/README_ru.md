# loft · macOS

[English](README.md) · **Русский** · [中文](README_zh.md)

Агент в строке меню. Минимальная версия ОС: **macOS 10.13**. UX: [`design/DESIGN_ru.md`](design/DESIGN_ru.md). Макет: [`design/index.html`](design/index.html).

```
make app          # ../../dist/clients/macOS/loft.app  (patch версии +1)
make dmg          # ../../dist/clients/macOS/loft-*-macos-<arch>.dmg
make dmg BUMP=0   # тот же DMG, VERSION не трогать (CI)
make dmg ARCH=x86_64 BUMP=0   # срез Intel (кросс с Apple Silicon)
make dmg ARCH=arm64 BUMP=0    # срез Apple Silicon (кросс с Intel)
make dmgs         # оба DMG; первый может поднять VERSION, второй оставляет
make run
make test-crypto  # вектор RFC 8439 → dist/clients/macOS/obj/<arch>/test_aead
make shot         # PNG окон → /tmp/loft-shots
make front-probe  # одно рукопожатие loft-front, без TUN
```

Из корня репозитория: `make macos` / `make macos-dmg`.

В DMG лежат `loft.app`, симлинк **Applications** (перетащить для установки) и [`pack/README.txt`](pack/README.txt).

Пока в связке ключей нет Developer ID, приложение подписывается ad-hoc:

```bash
# локально
make dmg

# первый Developer ID Application в связке
CODESIGN_IDENTITY=auto make dmg

# явно
CODESIGN_IDENTITY="Developer ID Application: Name (TEAMID)" make dmg

# нотаризация по желанию (профиль связки из `xcrun notarytool store-credentials`)
CODESIGN_IDENTITY=auto NOTARY_PROFILE=loft make dmg
```

`pack/sign.sh` подписывает `loft-priv`, `loft-front`, Swift dylibs, а затем сам бандл. Entitlements: [`pack/entitlements.plist`](pack/entitlements.plist) (сеть + unsigned executable memory для `loft-front`). App Sandbox отключён: он мешает TUN и setuid-помощнику.

`.o` и `test_aead` складываются в `dist/clients/macOS/obj/<arch>/` рядом с приложением. Исходник AEAD-теста: [`tests/test_aead.c`](tests/test_aead.c). `--probe` и `--shot` остаются в бинарнике приложения, потому что им нужен AppKit.

Каждый `make app` поднимает patch-версию в [`VERSION`](VERSION) (`1.0.0` → `0.9.1` → `0.9.2`) и прописывает её в `Info.plist`. Окно About показывает эту строку. Версия ПО протокола по-прежнему берётся из корневого `VERSION`. С `BUMP=0` в `Info.plist` попадает текущая версия, а сам файл не перезаписывается. Имена DMG в Dist: `macos-arm` и `macos-intel`. `ARCH` (`arm64` / `x86_64`) — срез, по умолчанию `uname -m`. CI (`make macos-dmgs BUMP=0`) собирает оба DMG на раннере Apple Silicon: arm64 нативно, `ARCH=x86_64` кросс-сборкой.

Интерфейс написан на Swift 5 + AppKit (Go внутри процесса не годится: начиная с Go 1.20 он не поддерживает 10.13). Swift stdlib копируется в `Contents/Frameworks`. Сессию оверлея ведёт **`loft-front`** (Go), он лежит в `Contents/MacOS/` рядом с `loft`. [`gooverlay/`](gooverlay/) подменяет Darwin-проверку сертификатов в `crypto/x509`, чтобы dyld не требовал символов Security из macOS 12. `loft-front` поддерживает GET, WS, H2, QUIC и H3, mux, clockless auth, mask, CmdLeave, ICMP оверлея как Dgram `:1`. В Hello передаются mux-фронты профиля (GET плюс `ws` / `h2` / `quic` / `h3`), чтобы к сессии можно было прикрепиться через 0x04. `l3` работает отдельной сессией. Hello-caps берутся из Go-клиента, не из `GETFront.swift`.

| Локально | В канале |
|----------|------------|
| TUN (utun, маршрут по умолчанию) через loft-front | одно подключение, mux-фронты GET/WS/H2/QUIC/H3, протокол 1.0 |
| SSTP | PPP поверх HTTPS к SSTP-серверу Windows/accel-ppp |

При первом Open запрашивается пароль администратора (для `ifconfig` / `route`). Если Loft падает, Hold перекрывает нативный маршрут по умолчанию. **Reset routes** возвращает его.

Профиль — это один файл `client.yaml` / `.loft` в `~/Library/Application Support/loft/profiles/`. Импорт, экспорт и правка вручную — в **Open Loft…**.

Иконки: `../../docs/brand/` (`app/loft.icns`, `menubar/loftTemplate.png`). Знак, палитра, шрифт: [`brand/README_ru.md`](../../docs/brand/README_ru.md).

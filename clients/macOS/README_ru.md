# loft · macOS

[English](README.md) · **Русский** · [中文](README_zh.md)

Агент в строке меню. Минимум ОС: **macOS 10.13**. UX: [`design/DESIGN_ru.md`](design/DESIGN_ru.md). Макет: [`design/index.html`](design/index.html).

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

Подпись ad-hoc, пока в связке нет Developer ID:

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

`pack/sign.sh` подписывает `loft-priv`, `loft-front`, Swift dylibs, затем бандл. Entitlements: [`pack/entitlements.plist`](pack/entitlements.plist) (сеть + unsigned executable memory для `loft-front`). App Sandbox нет — TUN и setuid-помощнику он мешает.

`.o` и `test_aead` пишутся в `dist/clients/macOS/obj/<arch>/` рядом с приложением. Исходник AEAD-теста: [`tests/test_aead.c`](tests/test_aead.c). `--probe` и `--shot` остаются на двоичном файле приложения: им нужен AppKit.

Каждый `make app` поднимает patch в [`VERSION`](VERSION) (`1.0.0` → `0.9.1` → `0.9.2`) и штампует `Info.plist`. About читает эту строку. Версия ПО протокола остаётся корневым `VERSION`. `BUMP=0` штампует текущий файл и не пишет его. В имени DMG есть `ARCH` (`arm64` / `x86_64`), по умолчанию `uname -m`. CI (`make macos-dmgs BUMP=0`) собирает оба на раннере Apple Silicon: нативный arm64 и кросс `ARCH=x86_64`.

Интерфейс — Swift 5 + AppKit (Go 1.20 был последним тулчейном с целью 10.13). Swift stdlib копируется в `Contents/Frameworks`. Сессия оверлея — **`loft-front`** (Go) в `Contents/MacOS/` рядом с `loft`. Он говорит GET, WS, H2, QUIC и H3, mux, clockless auth, mask, CmdLeave, ICMP оверлея как Dgram `:1`. Hello-caps берутся из Go-клиента, не из `GETFront.swift`.

| Локально | В канале |
|----------|------------|
| TUN (utun, маршрут по умолчанию) через loft-front | фронт GET, WS, H2, QUIC или H3, протокол 1.0 |
| SSTP | PPP поверх HTTPS к Windows/accel-ppp SSTP |

Первый Open спрашивает пароль администратора (`ifconfig` / `route`). Hold закрывает нативный default, когда Loft умирает. **Reset routes** возвращает его.

Профиль — один `client.yaml` / `.loft` в `~/Library/Application Support/loft/profiles/`. Импорт, экспорт и правка вручную — в **Open Loft…**.

Иконки: `../../docs/brand/` (`app/loft.icns`, `menubar/loftTemplate.png`). Марка, палитра, шрифт: [`brand/README_ru.md`](../../docs/brand/README_ru.md).

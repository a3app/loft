# Клиенты

[English](README.md) · **Русский** · [中文](README_zh.md)

| | |
|--|--|
| [`go/cmd/loft`](go/cmd/loft/) | CLI: SOCKS5, TUN. Эталон протокола 1.0 |
| [`go/cmd/loft-front`](go/cmd/loft-front/) | Процесс сессии. Его запускают macOS и Sailfish |
| [`go/cmd/loft-probe`](go/cmd/loft-probe/) | ICMP/TCP/UDP/DNS/HTTP через сессию |
| [`go/cmd/loft-bench`](go/cmd/loft-bench/) | Автозамер GET/WS против пути connect на macOS |
| [`macOS/`](macOS/) | Строка меню. UX: [`macOS/design/DESIGN_ru.md`](macOS/design/DESIGN_ru.md) |
| [`Keenetic/`](Keenetic/) | Entware / OpkgTun. План: [`Keenetic/PLAN.md`](Keenetic/PLAN.md). Макет: [`Keenetic/design/index.html`](Keenetic/design/index.html) |
| [`MikroTik/`](MikroTik/) | Контейнер RouterOS 7. Макет: [`MikroTik/design/index.html`](MikroTik/design/index.html) |
| [`iOS/`](iOS/) | Packet Tunnel. Тонкий хост + appex. Макет: [`iOS/design/index.html`](iOS/design/index.html). Apple TV: [`iOS/PLAN.md`](iOS/PLAN.md) фаза 6 |
| [`Android/`](Android/) | VpnService. Тонкий хост Java. Flavor телефона и TV. Макет: [`Android/design/index.html`](Android/design/index.html) |
| [`Sailfish/`](Sailfish/) | Оверлей ConnMan. Тонкий хост QML + loft-front. ОС Аврора — RPM OMP. Макет: [`Sailfish/design/index.html`](Sailfish/design/index.html) |
| [`mobile/`](mobile/) | Общий движок Go (`gomobile bind`). Без GUI. |

Общий код: `internal/`. Артефакты: `dist/clients/`.

```bash
make client        # dist/clients/go/loft
make front         # dist/clients/go/loft-front
make probe         # dist/clients/go/loft-probe
make bench         # dist/clients/go/loft-bench
make macos         # dist/clients/macOS/loft.app
make macos-dmg     # dist/clients/macOS/loft-*-macos-<arch>.dmg
make macos-dmgs    # оба DMG, arm64 и x86_64 (CI)
make android       # dist/clients/Android/loft.app + loft-tv.app + loft-gtv.app + APKs
make ios           # dist/clients/iOS/loft.app (macOS) + iphonesimulator/loft.app
make dist-clients  # dist/clients/go/loft-<ver>-<os>-<arch>.tar.gz
```

Протокол: [docs/protocol_ru.md](../docs/protocol_ru.md). Запуск: [docs/guide_ru.md](../docs/guide_ru.md).

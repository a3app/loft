# Клиенты

[English](README.md) · **Русский** · [中文](README_zh.md)

| | |
|--|--|
| [`go/cmd/loft`](go/cmd/loft/) | CLI: SOCKS5, TUN. Эталонная реализация протокола 1.0 |
| [`go/cmd/loft-front`](go/cmd/loft-front/) | Процесс сессии, его запускают клиенты для macOS и Sailfish |
| [`go/cmd/loft-probe`](go/cmd/loft-probe/) | ICMP/TCP/UDP/DNS/HTTP через сессию |
| [`go/cmd/loft-bench`](go/cmd/loft-bench/) | Автоматический замер пропускной способности GET/WS в сравнении с путём connect на macOS |
| [`macOS/`](macOS/) | Приложение в строке меню. UX: [`macOS/design/DESIGN_ru.md`](macOS/design/DESIGN_ru.md) |
| [`Keenetic/`](Keenetic/) | Entware / OpkgTun. `make -C clients/Keenetic ipk`. Страница `http://<lan>:8090/`. |
| [`MikroTik/`](MikroTik/) | Контейнер RouterOS 7. Макет: [`MikroTik/design/index.html`](MikroTik/design/index.html) |
| [`iOS/`](iOS/) | Packet Tunnel. Тонкое приложение-хост + appex. Макет: [`iOS/design/index.html`](iOS/design/index.html). Apple TV: [`iOS/PLAN.md`](iOS/PLAN.md), фаза 6 |
| [`Android/`](Android/) | VpnService. Тонкий хост на Java. Flavor для телефона и для TV. Макет: [`Android/design/index.html`](Android/design/index.html) |
| [`Windows/`](Windows/) | Клиент в трее. x86/x64 Win7 SP1+; arm64 Win10/11 ARM. Оболочка Win32 + loft-svc. UX: [`Windows/design/DESIGN_ru.md`](Windows/design/DESIGN_ru.md) |
| [`Sailfish/`](Sailfish/) | Оверлей ConnMan. Тонкий хост на QML + loft-front. Для ОС Аврора — RPM-пакет OMP. Макет: [`Sailfish/design/index.html`](Sailfish/design/index.html) |
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
make windows       # dist/clients/Windows/x64 и x86
make dist-clients  # dist/clients/go/loft-<ver>-<os>-<arch>.tar.gz
```

Протокол: [docs/protocol_ru.md](../docs/protocol_ru.md). Запуск: [docs/guide_ru.md](../docs/guide_ru.md).

# loft · Sailfish OS

[English](README.md) · **Русский** · [中文](README_zh.md)

Клиент Sailfish OS: одна сессия оверлея через **ConnMan**. Приложение QML держит профили и лампу; `loft-front` — процесс сессии; `connman-vpnd` владеет tun.

ОС Аврора — пакет OMP этого дерева: [`aurora/`](aurora/) (`app.loft.aurora`). RPM Sailfish: [`sailfish/`](sailfish/) (`harbour-loft`).

Протокол: [docs/protocol_ru.md](../../docs/protocol_ru.md). Стыковка: [docs/sailfish_ru.md](../../docs/sailfish_ru.md). Вариант Авроры: [docs/aurora_ru.md](../../docs/aurora_ru.md). URI импорта: [docs/share_ru.md](../../docs/share_ru.md). План: [PLAN.md](PLAN.md). UX: [design/DESIGN_ru.md](design/DESIGN_ru.md). Макет: [design/index.html](design/index.html).

Тонкий хост QML в этом дереве. Кросс помощника: `make front`.

## Рамки

| На телефоне | Роль |
|-------------|------|
| loft (get / ws / h2 / quic / h3) | оверлей loft. Одна строка ConnMan. |
| SSTP | Та же строка, когда подключим. TLS + PPP, IP из IPCP. Go `internal/sstp`. |
| VLESS | Та же строка, когда loft фильтруют. Импорт — QR `vless://`. |
| Trojan | Та же строка, когда loft фильтруют. Импорт — QR `trojan://`. Go `internal/trojan`. |
| SOCKS5 как вход продукта | На Sailfish нет. Чужие приложения в локальный SOCKS не ходят. Вход — TUN ConnMan. |
| OpenVPN, IPsec, WireGuard в этом приложении | Нет. Это штатные типы Настроек. |

ConnMan держит **одну** активную строку VPN. Несколько профилей loft — переключатель, не параллельные TUN.

Плагин должен пинить IP фронтов мимо tun, чтобы сессия не зациклилась.

## Дерево

```
clients/Sailfish/
  README.md          этот файл
  PLAN.md            фазы сборки
  design/            спецификация UX и HTML-макет
  qml/               GUI Silica (тонкий хост вставки)
  src/               C++ QProcess → loft-front
  connman/           плагин VPN, loft-connman, скрипт notify
  settings/          тип Loft в Настройках → Сеть
  sailfish/          desktop и spec harbour-loft
  aurora/            desktop и spec app.loft.aurora (OMP)
  CMakeLists.txt     -DLOFT_OS=sailfish|aurora
  ../go/cmd/loft-front
```

Сессия — [`loft-front`](../go/cmd/loft-front/), тот же помощник, что запускает приложение macOS. gomobile bind нет. iOS/Android оставляют `clients/mobile`.

## Нужно

| | Sailfish | Аврора |
|--|----------|--------|
| ОС | Sailfish OS 4.x / 5.x | ОС Аврора 5.x |
| GUI | Qt 5 / QML Silica, Sailfish SDK | Qt 5 / QML Silica, Аврора SDK |
| Id | `harbour-loft` | `app.loft.aurora` |
| Помощник | `loft-front` `linux/arm64` (`make front`) | тот же бинарник, пересборка если glibc другой |
| CMake | `-DLOFT_OS=sailfish` | `-DLOFT_OS=aurora` (сам, если найден `auroraapp`) |

Архитектуры: сначала aarch64, armv7hl если устройство просит. Эмулятор — i486/x86_64, только QML.

## Сборка

```bash
make front          # кросс Go: bin/loft-front (linux/arm64, без CGO)
# Sailfish SDK → open clients/Sailfish, -DLOFT_OS=sailfish
# Aurora IDE  → open clients/Sailfish, -DLOFT_OS=aurora
# или, если SDK в PATH:
#   sfdk build          # Sailfish
#   mb2 build           # Аврора (есть auroraapp)
```

Имя типа в системе: **Loft**. Протокол: **loft** / **LOFT**.

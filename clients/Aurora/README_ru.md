# loft · ОС Аврора

[English](README.md) · **Русский** · [中文](README_zh.md)

Клиент ОС Аврора: одна сессия оверлея через **ConnMan**. Приложение QML держит профили и лампу; `loft-front` — процесс сессии; `connman-vpnd` владеет tun.

Протокол: [docs/protocol_ru.md](../../docs/protocol_ru.md). Интеграция: [docs/aurora_ru.md](../../docs/aurora_ru.md). URI импорта: [docs/share_ru.md](../../docs/share_ru.md). План: [PLAN_ru.md](PLAN_ru.md). UX: [design/DESIGN_ru.md](design/DESIGN_ru.md). Макет: [design/index.html](design/index.html).

Тонкий хост QML в этом дереве. Кросс помощника: `make front`.

## Рамки

| На телефоне | Роль |
|-------------|------|
| loft (get / ws / h2 / quic / h3) | оверлей loft. Одна строка ConnMan. |
| SSTP | Та же строка, когда подключим. TLS + PPP, IP из IPCP. Go `internal/sstp`. |
| VLESS | Та же строка, когда loft фильтруют. Импорт — QR `vless://`. |
| Trojan | Та же строка, когда loft фильтруют. Импорт — QR `trojan://`. Go `internal/trojan`. |
| SOCKS5 как вход продукта | На Авроре нет. Чужие приложения в локальный SOCKS не ходят. Вход — TUN ConnMan. |
| OpenVPN, IPsec, WireGuard в этом приложении | Нет. Это штатные типы Настроек. |

Аврора держит **одну** активную строку VPN. Несколько профилей loft — переключатель, не параллельные TUN.

Плагин должен пинить IP фронтов мимо tun, чтобы сессия не зациклилась.

## Дерево

```
clients/Aurora/
  README.md          этот файл
  PLAN.md            фазы сборки
  design/            спецификация UX и HTML-макет
  qml/               GUI Silica (тонкий хост вставки)
  src/               C++ QProcess → loft-front
  connman/           плагин VPN, loft-connman, скрипт notify
  settings/          тип Loft в Настройках → Сеть
  rpm/               spec RPM
  CMakeLists.txt     Aurora IDE / mb2
  ../go/cmd/loft-front
```

Сессия — [`loft-front`](../go/cmd/loft-front/), тот же помощник, что запускает приложение macOS. gomobile bind нет. iOS/Android оставляют `clients/mobile`.

## Нужно

| | |
|--|--|
| ОС | ОС Аврора **5.x** |
| GUI | Qt 5 / QML Silica, Аврора SDK |
| Помощник | `loft-front` `linux/arm64` (`make front`) |
| Пакет | RPM, id `app.loft.aurora` |

Архитектуры: сначала aarch64, armv7hl если устройство просит. Эмулятор — i486/x86_64, только QML.

## Сборка

```bash
make front          # кросс Go: bin/loft-front (linux/arm64, без CGO)
# Aurora IDE → open clients/Aurora
# или, если SDK в PATH:
#   mb2 build
```

Id: `app.loft.aurora`. Имя типа в системе: **Loft**. Протокол: **loft** / **LOFT**.

# loft · KeeneticOS

[English](README.md) · **Русский** · [中文](README_zh.md)

Клиент для Entware: один интерфейс **OpkgTun**, который KeeneticOS использует в приоритетах подключений и политиках маршрутизации. Настройки доступны на LAN-порту, вход — через сессию веб-админки Keenetic.

Протокол: [docs/protocol_ru.md](../../docs/protocol_ru.md). Интеграция: [docs/keenetic_ru.md](../../docs/keenetic_ru.md). План: [PLAN.md](PLAN.md). UX: [design/DESIGN_ru.md](design/DESIGN_ru.md). Макет: [design/index.html](design/index.html).

Пока это только каталог: собранного бинарника ещё нет.

## Рамки

| На роутере | Роль |
|------------|------|
| loft (get / ws / h2 / quic / h3 / l3) | оверлей loft. Один OpkgTun. |
| VLESS | Позже. Второй исходящий канал loft на случай, если loft фильтруют. |
| SSTP, WireGuard, OpenVPN, IPsec, OpenConnect | Штатные средства KeeneticOS, не этот пакет. |

Клиент **не** ставит маршрут по умолчанию: таблицей маршрутизации управляет NDM. IP-адреса фронтов остаются на маршруте через провайдера, иначе сессия зациклится.

## Структура (план)

```
clients/Keenetic/
  README.md          этот файл
  PLAN.md            фазы сборки
  design/            UX и HTML-макет
  cmd/               (позже) loft-keenetic: сессия + панель
  pack/              (позже) init.d, ipk, утилиты ndmc
```

Сессию ведёт Go-клиент из `clients/go` с TUN и выключенным `default_route`. Панель — небольшой HTTP-сервер, который проверяет cookie Keenetic `/auth`.

## Что нужно

KeeneticOS **5.0+** (OpkgTun в политиках). Компоненты: OPKG, TUN/TAP. Entware во встроенной памяти (NAND) или на USB-накопителе с EXT4. Архитектуры: aarch64, armv7, mipsel.

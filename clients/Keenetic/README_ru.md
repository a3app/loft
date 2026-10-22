# loft · KeeneticOS

[English](README.md) · **Русский** · [中文](README_zh.md)

Клиент Entware: один интерфейс **OpkgTun**, который KeeneticOS ставит в приоритеты подключений и политики. Настройки — на LAN-порту; вход — сессия веб-админки Keenetic.

Протокол: [docs/protocol_ru.md](../../docs/protocol_ru.md). Интеграция: [docs/keenetic_ru.md](../../docs/keenetic_ru.md). План: [PLAN.md](PLAN.md). UX: [design/DESIGN_ru.md](design/DESIGN_ru.md). Макет: [design/index.html](design/index.html).

Это каталог. Сборки ещё нет.

## Рамки

| На роутере | Роль |
|------------|------|
| loft (get / ws / h2 / quic / h3 / l3) | оверлей loft. Один OpkgTun. |
| VLESS | Позже. Второй исходящий loft, если loft режут. |
| SSTP, WireGuard, OpenVPN, IPsec, OpenConnect | Штатный KeeneticOS. Не этот пакет. |

Клиент **не** ставит маршрут по умолчанию. Таблицу держит NDM. IP фронтов остаются у провайдера, иначе сессия зациклится.

## Раскладка (план)

```
clients/Keenetic/
  README.md          этот файл
  PLAN.md            фазы сборки
  design/            UX и HTML-макет
  cmd/               (позже) loft-keenetic: сессия + панель
  pack/              (позже) init.d, ipk, утилиты ndmc
```

Сессия — клиент Go в `clients/go` с TUN и выключенным `default_route`. Панель — маленький HTTP, проверяет cookie Keenetic `/auth`.

## Нужно

KeeneticOS **5.0+** (OpkgTun в политиках). Компоненты: OPKG, TUN/TAP. Entware в NAND или USB EXT4. Архитектуры: aarch64, armv7, mipsel.

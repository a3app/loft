# loft · RouterOS

[English](README.md) · **Русский** · [中文](README_zh.md)

Клиент-контейнер для MikroTik **RouterOS 7**. Тот же бинарник `loft`, что и Linux CLI, внутри `/container`. Это не `.npk` MikroTik.

Протокол: [docs/protocol_ru.md](../../docs/protocol_ru.md). Как устроено: [docs/mikrotik_ru.md](../../docs/mikrotik_ru.md). UX: [design/DESIGN_ru.md](design/DESIGN_ru.md). Макет: [design/index.html](design/index.html).

## Что получает оператор

| | |
|--|--|
| SOCKS5 `172.31.20.2:1080` | Сразу, когда контейнер `running` и в логе `socks5`. Браузер и SSH ходят сюда. |
| Страница `http://172.31.20.2:8090/` | Статус, ключ, URL ноды, **исходящий интерфейс RouterOS** (`ether1`, `lte1`, …). Пишет YAML и `uplink.rsc`. |
| Шлюз для LAN | Когда в логе `tun up`. Тогда `policy.rsc` помечает список адресов в таблицу `loft`. |
| Фронты | Исходят с `eth0` (`loft -if eth0`), GET/WS/H2/QUIC/H3 не заворачиваются в оверлей. |

RouterOS решает **куда** идут пакеты (mangle, таблицы, address-list). loft решает **как** сессия выглядит снаружи.

## Платы

| | |
|--|--|
| Идёт | arm, arm64, x86 / CHR — RB5009, hAP ax²/ax³, CCR2004+, CHR |
| Не идёт | mipsbe / mmips / smips — hEX, hAP lite и большая часть дешёвых плат |

Нужны RouterOS **7.12+**, пакет `container`, `device-mode container=yes` (один раз Reset) и диск (`/disk/print`, обычно `disk1`).

## Дерево

```
clients/MikroTik/
  README.md          этот файл
  design/            UX и HTML-проходка
  pack/              Dockerfile, client.yaml, *.rsc
  Makefile           linux loft + docker save
```

## Установка

На ПК (Docker):

```bash
make -C clients/MikroTik image GOARCH=arm64   # или arm | amd64
```

Залить `pack/loft-linux-<arch>.tar` → `<диск>/loft/loft.tar` и `pack/client.yaml` → `<диск>/loft/data/client.yaml`. Прописать `token` и `url`.

На роутере, New Terminal:

1. `pack/check.rsc`
2. `pack/install.rsc` (поправить `:local disk`)
3. `/container/start loft`
4. Страница: `http://172.31.20.2:8090/` — ключ, URL, выход. Если WAN меняли: `/import disk1/loft/data/uplink.rsc`.
5. SOCKS: `172.31.20.2:1080`. Шлюз: `pack/policy.rsc` только после `tun up`.

Снять: `pack/uninstall.rsc`. YAML на диске остаётся.

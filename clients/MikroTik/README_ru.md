# loft · RouterOS

[English](README.md) · **Русский** · [中文](README_zh.md)

Клиент для MikroTik **RouterOS 7** в виде контейнера: внутри `/container` работает тот же бинарник `loft`, что и Linux CLI. Это не пакет `.npk` от MikroTik.

Протокол: [docs/protocol_ru.md](../../docs/protocol_ru.md). Как устроено: [docs/mikrotik_ru.md](../../docs/mikrotik_ru.md). UX: [design/DESIGN_ru.md](design/DESIGN_ru.md). Макет: [design/index.html](design/index.html).

## Что получает оператор

| | |
|--|--|
| SOCKS5 `172.31.20.2:1080` | Доступен, как только контейнер в состоянии `running`, а в логе появилось `socks5`, с TUN и без. Без пароля: `listen.socks5: off` или LAN-страница выключает его, там же порт. |
| Страница `http://172.31.20.2:8090/` | Статус, ключ, URL ноды, **исходящий интерфейс RouterOS** (`ether1`, `lte1`, …). Сохраняет YAML и `uplink.rsc`. |
| Шлюз для LAN | Появляется, когда в логе есть `tun up`. После этого `policy.rsc` направляет адреса из списка в таблицу `loft`. |
| Фронты | Выходят через `veth-loft` (`loft -if 172.31.20.1`), поэтому GET/WS/H2/QUIC/H3 не заворачиваются обратно в оверлей. |

RouterOS решает, **куда** идут пакеты (mangle, таблицы маршрутизации, address-list). loft решает, **как** сессия выглядит снаружи.

## Платы

| | |
|--|--|
| Работает | arm, arm64, x86 / CHR — RB5009, hAP ax²/ax³, CCR2004+, CHR |
| Не работает | mipsbe / mmips / smips — hEX, hAP lite и большинство дешёвых плат |

Нужны RouterOS **7.12+**, пакет `container`, `device-mode container=yes` (один раз, с нажатием Reset) и диск (`/disk/print`, обычно `disk1`).

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

Загрузите `pack/loft-linux-<arch>.tar` в `<диск>/loft/loft.tar`, а `pack/client.yaml` — в `<диск>/loft/data/client.yaml`. Пропишите `token` и `url`.

На роутере, в New Terminal:

1. `pack/check.rsc`
2. `pack/install.rsc` (поправьте `:local disk`)
3. `/container/start loft`
4. Страница `http://172.31.20.2:8090/`: ключ, URL, исходящий интерфейс. Если меняли WAN, выполните `/import disk1/loft/data/uplink.rsc`.
5. SOCKS: `172.31.20.2:1080`. Шлюз: `pack/policy.rsc`, только после `tun up`.

Удаление: `pack/uninstall.rsc`. YAML на диске остаётся.

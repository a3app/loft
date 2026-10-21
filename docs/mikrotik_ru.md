# loft на RouterOS

[English](mikrotik.md) · **Русский** · [中文](mikrotik_zh.md)

Как loft работает на плате MikroTik. Каталог: [clients/MikroTik/](../clients/MikroTik/). Макет: [clients/MikroTik/design/index.html](../clients/MikroTik/design/index.html). Протокол: [protocol_ru.md](protocol_ru.md).

Образ собирается `make -C clients/MikroTik image`. В Dist его пока нет. Доппакеты RouterOS выпускает только MikroTik; loft едет контейнером.

---

## 1. Две плоскости

RouterOS решает **куда** идут пакеты (mangle, таблицы, address-list). loft решает **как** сессия выглядит снаружи (фронты loft).

```
хост LAN
  → SOCKS 172.31.20.2:1080            (сразу после socks5)
  → [tun up] mangle список loft-lan
      → таблица loft → 172.31.20.2
          → TUN loft
              → loft  get|ws|h2|quic|h3  с eth0
                  → TLS 1.3 → CDN или нода
```

SSTP, WireGuard, IPsec, L2TP остаются штатными клиентами RouterOS. loft в Winbox VPN не встаёт.

## 2. Контейнер, не NPK

RouterOS 7.12+, доппакет `container` (arm, arm64, x86 / CHR). Один раз `device-mode container=yes`, затем Reset (CHR: выключить питание). Диск под `root-dir` и `tmpdir` — обычно `disk1`, не NAND.

```
/interface/veth        veth-loft  172.31.20.2/24  gw 172.31.20.1
/interface/bridge      br-loft
/ip/address            172.31.20.1/24 на br-loft
/ip/firewall/nat       masquerade src 172.31.20.0/24
/container             name=loft  mount /data  file=loft.tar
```

Точка входа: `loft -c /data/client.yaml -if eth0`. YAML — файл оператора; LAN-страница `http://172.31.20.2:8090/` его пишет. Образ: scratch + хранилище CA + статичный linux `loft`.

## 3. Сначала SOCKS

`listen.socks5: 0.0.0.0:1080` — путь, который всегда работает. Ноутбук в LAN (или сам роутер) ходит в `172.31.20.2:1080`. У RouterOS нет своего SOCKS-клиента; браузер, curl и SSH `-o ProxyCommand` есть.

`policy.rsc` на одном SOCKS не гоняем.

## 4. Шлюз после tun up

`/dev/net/tun` внутри контейнера RouterOS не обещан. Если в логе `tun up`:

- таблица `loft`, default через `172.31.20.2`
- address-list `loft-lan` (префикс правит оператор)
- mangle prerouting: src список, dst `!172.31.20.0/24`, mark-routing `loft`

У самого RouterOS нового default route нет. Без TUN 0/0 через IP контейнера зацикливается (default контейнера — шлюз veth на ROS).

## 5. LAN-страница и выход

`listen.http: 0.0.0.0:8090` — маленькая страница оператора (статус, ключ, URL, фронт, выход). По желанию `listen.http_pass`. Cookie Winbox туда не ходит.

**Выход** — имя WAN в RouterOS (`ether1`, `lte1`, `pppoe-out1`). Контейнер всегда биндит фронты на `eth0`. Сохранение пишет `/data/uplink.rsc`. Импорт:

```
/import disk1/loft/data/uplink.rsc
```

Это сажает `172.31.20.0/24` в таблицу `loft-wan` → названный интерфейс. Пустой выход снимает правило (основная таблица).

## 6. Петля

Политика, которая кидает LAN `0/0` в контейнер, утащит и GET/WS на ноду. loft биндит фронты на `eth0` (`-if eth0`). Mangle обходит `172.31.20.0/24`. В YAML `kill_switch: false`; внутри netns loft всё равно ставит split default — так контейнер и становится шлюзом.

## 7. Платы

| | |
|--|--|
| Идёт | arm, arm64, x86, x86_64 — RB5009, hAP ax²/ax³, CCR2004+, CHR |
| Не идёт | mipsbe / mmips / smips — hEX, hAP lite и большая часть дешёвых плат |

`architecture-name` из `/system/resource` должен совпасть с тарболом (`arm` / `arm64` / `amd64` для x86 и x86_64).

## 8. Чем этот файл не является

Не `.npk` вендора. Не новый тип VPN в Winbox. Не loft-node на этой же плате. Не патч WebFig.

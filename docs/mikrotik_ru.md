# loft на RouterOS

[English](mikrotik.md) · **Русский** · [中文](mikrotik_zh.md)

Как loft работает на плате MikroTik. Каталог: [clients/MikroTik/](../clients/MikroTik/). Макет: [clients/MikroTik/design/index.html](../clients/MikroTik/design/index.html). Протокол: [protocol_ru.md](protocol_ru.md).

Образ собирается командой `make -C clients/MikroTik image`. В Dist его пока нет. Дополнительные пакеты RouterOS выпускает только сама MikroTik, поэтому loft поставляется как образ контейнера.

---

## 1. Две плоскости

RouterOS решает, **куда** идут пакеты (mangle, таблицы маршрутизации, address-list). loft решает, **как** сессия выглядит снаружи (фронты loft).

```
хост LAN
  → SOCKS 172.31.20.2:1080            (сразу после socks5)
  → [tun up] mangle список loft-lan
      → таблица loft → 172.31.20.2
          → TUN loft
              → loft  get|ws|h2|quic|h3  с veth-loft
                  → TLS 1.3 → CDN или нода
```

SSTP, WireGuard, IPsec и L2TP остаются штатными клиентами RouterOS. В списке VPN в Winbox loft не появляется.

## 2. Контейнер, не NPK

Нужны RouterOS 7.12+ и дополнительный пакет `container` (arm, arm64, x86 / CHR). Один раз выполнить `device-mode container=yes`, затем Reset (на CHR — выключить и снова включить питание). Диск под `root-dir` и `tmpdir` — обычно `disk1`, но никогда не NAND.

```
/interface/veth        veth-loft  172.31.20.2/24  gw 172.31.20.1
/interface/bridge      br-loft
/ip/address            172.31.20.1/24 на br-loft
/ip/firewall/nat       masquerade src 172.31.20.0/24
/container             name=loft  mount /data  file=loft.tar
```

Точка входа: `loft -c /data/client.yaml -if 172.31.20.1`. `-if` получает адрес шлюза veth, а не имя интерфейса: внутри контейнера RouterOS интерфейс называется как veth (`veth-loft`), `eth0` там нет, и loft привязывается к интерфейсу, в подсети которого лежит этот адрес. YAML — файл оператора, его записывает LAN-страница `http://172.31.20.2:8090/`. Образ: scratch + хранилище CA + статический linux-бинарник `loft`.

## 3. Сначала SOCKS

`listen.socks5: 0.0.0.0:1080` — путь, который всегда работает. Ноутбук в LAN (или сам роутер) подключается к `172.31.20.2:1080`. У RouterOS нет собственного SOCKS-клиента, а у браузера, curl и SSH `-o ProxyCommand` он есть.

Не запускайте `policy.rsc`, если работает только SOCKS.

## 4. Шлюз после tun up

Наличие `/dev/net/tun` внутри контейнера RouterOS не гарантировано. Если в логе есть `tun up`:

- таблица `loft`, default через `172.31.20.2`
- address-list `loft-lan` (префикс правит оператор)
- mangle prerouting: src список, dst `!172.31.20.0/24`, `dst-address-type=!local`, mark-routing `loft`. Без `!local` LAN теряет сам роутер: его DNS, WebFig и Winbox уходят в туннель

Сам RouterOS нового маршрута по умолчанию не получает. Без TUN маршрут 0/0 через IP контейнера зацикливается (маршрут по умолчанию у контейнера — шлюз veth на ROS).

## 5. LAN-страница и выход

`listen.http: 0.0.0.0:8090` отдаёт небольшую страницу оператора (статус, ключ, URL, фронт, выход). Пароль можно задать в `listen.http_pass`. Cookie Winbox на эту страницу не распространяется.

**Выход** — имя WAN-интерфейса в RouterOS (`ether1`, `lte1`, `pppoe-out1`). Контейнер всегда привязывает фронты к `veth-loft`. При сохранении записывается `/data/uplink.rsc`. Импортируйте его:

```
/import disk1/loft/data/uplink.rsc
```

Это направляет `172.31.20.0/24` в таблицу `loft-wan` → указанный интерфейс. Если выход пустой, правило снимается и используется основная таблица.

## 6. Петля

Политика, которая отправляет LAN `0/0` в контейнер, захватит и GET/WS-соединения с нодой. loft привязывает фронты к `veth-loft` (`-if 172.31.20.1`). Mangle пропускает `172.31.20.0/24`. В YAML стоит `kill_switch: false`; внутри netns loft всё равно ставит раздельные маршруты по умолчанию (split default), и именно благодаря этому контейнер становится шлюзом.

## 7. Платы

| | |
|--|--|
| Работает | arm, arm64, x86, x86_64 — RB5009, hAP ax²/ax³, CCR2004+, CHR |
| Не работает | mipsbe / mmips / smips — hEX, hAP lite и большинство дешёвых плат |

`architecture-name` из `/system/resource` должен совпадать с тарболом (`arm` / `arm64` / `amd64` для x86 и x86_64).

## 8. Чем этот файл не является

Это не `.npk` от вендора, не новый тип VPN в Winbox, не loft-node на той же плате и не патч WebFig.

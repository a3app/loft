# loft · проходка RouterOS

[English](DESIGN.md) · **Русский** · [中文](DESIGN_zh.md)

Интерактивный макет: [`index.html`](index.html). Марка: [`brand/`](../../../docs/brand/). Интеграция: [docs/mikrotik_ru.md](../../../docs/mikrotik_ru.md).

Макет — путь оператора в Winbox / New Terminal и LAN-страница `http://172.31.20.2:8090/`. Mangle, таблицы и address-list остаются в RouterOS. loft — контейнер, который держит сессию.

YAML по-прежнему на диске. Страница его пишет.

---

## 1. Продукт

Один контейнер `loft`. Один veth `172.31.20.2/24`. SOCKS5 с первой загрузки. Шлюз для LAN только после строки `tun up` в логе.

Оператор:

1. Вставляет `check.rsc` в New Terminal.
2. Кладёт `loft.tar` и `client.yaml` на диск.
3. Вставляет `install.rsc`, ждёт `stopped`, стартует контейнер.
4. Открывает `http://172.31.20.2:8090/` — ключ, URL ноды, **исходящий интерфейс RouterOS** (`ether1`, `lte1`, `pppoe-out1`).
5. Если выход меняли: `/import disk1/loft/data/uplink.rsc` в New Terminal.
6. Направляет ноутбук в SOCKS `172.31.20.2:1080`.
7. Если в логе `tun up` — вставляет `policy.rsc` для списка LAN.

`.npk` вендора нет. Новой строки в Winbox «Интерфейсы / VPN» нет. SSTP и WireGuard остаются штатными клиентами RouterOS.

## 2. Экраны

| Экран | Что |
|-------|-----|
| Проверка | Имя, версия, `architecture-name`, пакет `container`, `device-mode`, диски. Если что-то красное — install не гоняем. |
| Файлы | `<диск>/loft/loft.tar` и `<диск>/loft/data/client.yaml`. Токен и URL ноды живут в YAML. |
| Контейнер | Имя `loft`, veth-loft, `start-on-boot`, лог. Статус `running`. |
| SOCKS | Сразу после строки `socks5`. Браузер и SSH — `172.31.20.2:1080`. |
| Панель :8090 | Статус, ключ, URL, фронт, **выход**. Пишет `client.yaml` и `uplink.rsc`. По желанию `listen.http_pass`. |
| Шлюз | `policy.rsc`: таблица `loft`, 0/0 через 172.31.20.2, список `loft-lan`, mangle обходит `172.31.20.0/24`. Скрыт, пока нет `tun up`. |
| Журнал | Хвост `/log print where topics~"container"`. Ключа нет. |

Простой вид (по умолчанию): Проверка → Файлы → Контейнер → Панель. Шлюз — во второй группе.

Выход — имя WAN в RouterOS. Бинд контейнера остаётся `eth0`. Пустой выход — таблица по умолчанию.

## 3. Хром RouterOS

Winbox не перекрашиваем. Макет показывает, как **loft** выглядит *у них* в Container, Files и New Terminal: именованный контейнер, veth, таблица, которую оператор вставляет сам.

Winbox «VPN» (SSTP, WireGuard, IPsec, L2TP) — штатное. loft туда не встаёт.

## 4. Визуал

Те же токены loft, что на macOS: Ink `#1A1714`, Lamp `#E8B84A`, Paper `#E8DFD2`, Avenir Next. Рамка Winbox в макете светло-серая (их админка), чтобы было видно оба мира.

Шрифт: заголовки Demi, текст Regular, ключи SF Mono. L-марка — тот же путь 96×96, что на macOS: корпус камня, окно slate выкл, лампа вкл. Запущен — окно лампы горит. Стоп — лампа погашена, L камня.

В подвале живой путь: `http://172.31.20.2:8090/` на LAN-странице, `SOCKS 172.31.20.2:1080` на SOCKS, `/data/client.yaml · /data/uplink.rsc` на Подключении.

## 5. Строки

См. таблицу в [DESIGN.md](DESIGN.md).

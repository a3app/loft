# План · loft на RouterOS

[English](PLAN.md) · **Русский** · [中文](PLAN_zh.md)

Версия ПО — `VERSION` дерева. Протокол 1.0. План версии не повышает.

## Цель

Член сети loft на MikroTik, который поднимается из Winbox/WebFig без отдельного Linux перед LAN. SOCKS5 с первой загрузки; шлюз для LAN, когда в контейнере есть TUN.

## Вне рамок

- `.npk` MikroTik (их выпускает только вендор).
- Новая строка в Winbox «Интерфейсы / VPN».
- loft-node и decoy на этой же плате.
- SSTP, WireGuard, IPsec, L2TP, которые уже есть в RouterOS.
- Плата MIPS.

## Фазы

### 0. Каркас — это дерево

Документы, план, HTML-проходка, Dockerfile, `client.yaml`, `check.rsc` / `install.rsc` / `policy.rsc` / `uninstall.rsc`. `make image` собирает OCI-тарбол. В Dist его пока нет.

### 1. Дым на CHR и одной ARM64

- Образ `linux/amd64` на CHR, `linux/arm64` на RB5009 или hAP ax³.
- Ключ члена и GET-фронт. В логе: `socks5`, затем `outbound … up overlay=`.
- SOCKS с LAN: `curl --socks5 172.31.20.2:1080 https://example.net`.
- Фронты остаются на `eth0` (`-if eth0`) при поднятом TUN.

### 2. Шлюз

- Если есть `/dev/net/tun`: `tun up`, затем `policy.rsc`.
- Пинг шлюза оверлея с хоста из `loft-lan`.
- Если TUN нет: остаёмся на SOCKS; одна строка в логе; `policy.rsc` не гоняем.

### 3. LAN-страница

HTTP на `172.31.20.2:8090` (`listen.http`). Статус, ключ, URL, фронт, исходящий интерфейс RouterOS. Пишет YAML и `uplink.rsc`. Cookie Winbox туда не ходит. По желанию `listen.http_pass`.

### 4. Dist

Позже можно класть `loft-linux-arm64.tar` / `amd64` / `arm` рядом с CLI. Первый тег RouterOS Dist не ждёт.

## Готово когда

Оператор на RB5009 или CHR вставляет `check.rsc` и `install.rsc`, стартует `loft`, ноутбук в LAN ходит в оверлей через SOCKS. Если TUN есть — `policy.rsc` сажает список устройств в оверлей без default route на самом RouterOS.

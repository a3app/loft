---
title: loft
lang: ru
---

# loft

[English](index.md) · **Русский** · [中文](README_zh.md)

loft — оверлей внутри TLS. Потоки мультиплексируются поверх GET, WebSocket, HTTP/2, QUIC или HTTP/3 и шифруются ChaCha20-Poly1305. Снаружи сессия выглядит как обычный HTTPS, поэтому её пропускают CDN, обратные прокси и TLS-терминаторы. Сессия переживает короткие обрывы связи и потерю пакетов.

Протокол — **loft/1.0**, ссылки на подключения — **`loft://`**. Apache License 2.0.

Это сайт спецификации. Эталонная нода и клиенты лежат в [репозитории](https://github.com/a3app/loft).

## Как это устроено

```mermaid
flowchart LR
    subgraph Client["клиент loft"]
        App["SSH · браузер · curl"] --> In["SOCKS5 / TUN"]
        In --> Mux["mux Open / Data / Close / Dgram"]
        Mux --> Seal["ChaCha20-Poly1305"]
        Seal --> Front["get · ws · h2 · quic · h3"]
    end
    Front -->|"TLS 1.3 HTTPS"| Edge["CDN / обратный прокси"]
    Edge --> Node["loft-node"]
    Node --> Dest["лаборатория · парк · площадки"]
```

1. Приложения ходят в локальный SOCKS5 или TUN. `Holder` удерживает этот слушатель, пока меняется фронт.
2. Все потоки идут через один mux и шифруются ChaCha20-Poly1305.
3. Снаружи это обычный HTTPS, поэтому перед нодой может стоять CDN или обратный прокси.
4. При коротком обрыве mux «паркуется» на `gap_hold_ms` (по умолчанию 8 с). Для возобновления отправляется свежий `0x02` с тем же `instance_id`; внутренние TCP-соединения не рвутся.

## Спецификация

| | |
|--|--|
| [protocol_ru.md](protocol_ru.md) | Протокол. По нему можно написать сторонний клиент или ноду. |
| [architecture_ru.md](architecture_ru.md) | Это дерево: controller, relay, клиент, оверлей. |
| [code-tour_ru.md](code-tour_ru.md) | Путеводитель по коду: с чего начать читать, путь соединения, словарь. |
| [guide_ru.md](guide_ru.md) | Сборка, конфиг, decoy, roster, Debian. |
| [yandex-cdn_ru.md](yandex-cdn_ru.md) | Yandex Cloud CDN: пошаговая настройка. |
| [keenetic_ru.md](keenetic_ru.md) | KeeneticOS: OpkgTun, приоритеты, вход в панель. |
| [mikrotik_ru.md](mikrotik_ru.md) | RouterOS 7: контейнер, SOCKS, policy routing. |
| [ios_ru.md](ios_ru.md) | iOS: Packet Tunnel, Personal VPN. Apple TV позже. |
| [android_ru.md](android_ru.md) | Android: VpnService. Android TV / Google TV позже. |
| [sailfish_ru.md](sailfish_ru.md) | Sailfish OS: оверлей ConnMan, loft-front. ОС Аврора — пакет OMP. |
| [share_ru.md](share_ru.md) | URI импорта: `loft://`, `.loft` (одно или несколько подключений), QR. |

| Ещё | |
|--|--|
| [../README_ru.md](../README_ru.md) | Обзор |
| [../server/README_ru.md](../server/README_ru.md) | loft-node |
| [../server/debian/README_ru.md](../server/debian/README_ru.md) | Установщик и `.deb` |
| [../clients/README_ru.md](../clients/README_ru.md) | CLI, macOS, Keenetic, MikroTik, iOS, Android, Sailfish |
| [brand/README_ru.md](brand/README_ru.md) | Фирменный стиль |

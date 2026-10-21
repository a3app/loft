# URI импорта

[English](share.md) · **Русский** · [中文](share_zh.md)

Строка импорта на клиенте. Это **не** возможность протокола loft/1.0 и сессию не устанавливает. Нода этот URL не видит.

Адрес loft — `loft://`: тип подключения и транспорт в одной строке. Один исходящий на URI. Файл `.loft` (YAML) держит **одно или несколько подключений** и **может** смешивать все поддерживаемые типы (`loft`, `sstp`, `vless`, `trojan`). Формат файла: [§8](#8-файл-loft).

Протокол: [protocol_ru.md](protocol_ru.md). UX iOS: [clients/iOS/design/DESIGN_ru.md](../clients/iOS/design/DESIGN_ru.md). UX Android: [clients/Android/design/DESIGN_ru.md](../clients/Android/design/DESIGN_ru.md). UX macOS: [clients/macOS/design/DESIGN_ru.md](../clients/macOS/design/DESIGN_ru.md). UX Sailfish: [clients/Sailfish/design/DESIGN_ru.md](../clients/Sailfish/design/DESIGN_ru.md).

---

## 1. Детектор

Клиент, который принимает QR, вставку, фото или файл, пробует по порядку:

1. Обрезать UTF-8. Снять BOM.
2. `loft://` → [§2](#2-loft).
3. `vless://` → ссылка VLESS ([§5](#5-vless)).
4. `trojan://` → ссылка Trojan ([§6](#6-trojan)).
5. `sstp://` → ссылка SSTP ([§7](#7-sstp)).
6. YAML с `token:` / `password:` / `outbounds:` / `connections:` → импорт как `.loft` ([§8](#8-файл-loft)).
7. Иначе отказ.

Тот же детектор — на камере, на картинке из Фото или Файлов, на `.txt` / `.loft` / YAML и в поле вставки.

Полезная нагрузка QR — URI (или YAML) в UTF-8. Коррекции **M** достаточно. Обычный `loft://` влезает в QR версии 10. Пакет с несколькими подключениями в QR может не влезть; тогда файл.

---

## 2. loft://

Канонический адрес loft:

```
loft://<token>@<host>[:port][/path][?query][#name]
```

Клиенты выдают и принимают `loft://`.

RFC 3986. `token` и `name` процентировать (`application/x-www-form-urlencoded` для фрагмента не подходит). Порт по умолчанию **443**. IPv6-хост в скобках.

Схема **и есть** тип подключения (`loft`). Query `type` — **транспорт** (`get` `ws` `h2` `quic` `h3`). Строка списка показывает оба: `loft · get · cdn.example.com`.

| Часть | Куда в `client.yaml` |
|-------|----------------------|
| схема `loft` | `kind: loft` |
| `token` (userinfo) | `token` (`lt_…`) |
| `host`[:`port`] | хост исходящего `url` |
| `/path` | исходящий `path` (можно пустой) |
| `#name` | имя подключения; если пусто — `host` |

### Query

| Ключ | Значения | По умолчанию | YAML |
|------|----------|--------------|------|
| `type` | `get` `ws` `h2` `quic` `h3` | `get` | исходящий `type` (транспорт) |
| `cdn` | имя профиля (`yandex`, …) | нет | исходящий `cdn` |
| `fp` | отпечаток TLS (`chrome`, …) | `chrome` | исходящий `fingerprint` |
| `scheme` | `https` `http` `wss` `ws` | из `type` | схема `url` |
| `pbk` | открытый X25519 ноды, base64url или hex, 32 байта | нет | исходящий `mask_pub` |

`scheme` по умолчанию: `ws` → `wss`; остальное → `https`.

Собрать URL исходящего: `{scheme}://{host}:{port}{path}`.

`insecure_skip_verify`, `header_budget` и `inflight` не класть. Клиент берёт свои обычные значения. Ключ участника в query не класть.

Неизвестные ключи query **должен** игнорировать (вперёд совместимо). Неизвестный `type` — ошибка.

### Примеры

GET на CDN:

```
loft://lt_change-me-long-random@cdn.example.com/static/v3/build?type=get&cdn=yandex&fp=chrome#Home
```

становится:

```yaml
name: Home
kind: loft
token: "lt_change-me-long-random"
select: auto
outbounds:
  - name: Home
    type: get
    cdn: yandex
    url: https://cdn.example.com
    path: /static/v3/build
    fingerprint: chrome
```

WebSocket:

```
loft://lt_change-me-long-random@app.example.com/api/v1/sync?type=ws&fp=chrome#Office
```

→ `url: wss://app.example.com/api/v1/sync`.

QUIC без пути:

```
loft://lt_change-me-long-random@app.example.com:443?type=quic#Fast
```

→ `url: https://app.example.com:443`.

### Экспорт

Клиент, который умеет шарить, выдаёт `loft://` для **выбранного** исходящего **выбранного** подключения (один QR, один фронт). Несколько фронтов этого подключения или несколько подключений: экспорт `.loft` ([§7](#7-файл-loft)). URI — секрет: в нём ключ участника. Показывать на устройстве; в журнал не писать.

iOS регистрирует схему URL `loft`. Android — тот же фильтр intent. Sailfish (и Аврора) — `x-scheme-handler/loft`. Тап по `loft://…` в Сообщениях открывает приложение и предлагает импорт.

---

## 3. Камера и файл

| Источник | API |
|----------|-----|
| Живой QR (iOS) | `AVCaptureMetadataOutput`, тип QR. `NSCameraUsageDescription`. |
| Фото | iOS: `PHPicker`. Android: Photo Picker. macOS: панель открытия. QR на статичном кадре. |
| Файл | Картинки, `public.plain-text`, YAML, `.loft`, `.vpn`. QR со статичного кадра; иначе байты по §1. |

После попадания: экран подтверждения с **именем**, **типом**, **транспортом**, **хостом**. Ключ скрыт. Сохранение пишет Keychain + YAML в App Group. Подключить — отдельный тап.

---

## 4. Чем это не является

Не возможность Hello/Status. Не новая команда mux. Не замена `.loft`. Не URL подписки, который клиент опрашивает.

---

## 5. VLESS

Обычная строка импорта существующих клиентов VLESS. Тип `vless`. Транспорт — VLESS `type` / `security`. Поддерживаются TCP+TLS, REALITY, Vision (`xtls-rprx-vision`), XHTTP. Подпись списка: `vless · reality · host.example.com`.

```
vless://<uuid>@<host>[:port]?security=…&sni=…&type=…&flow=…&pbk=…&sid=…&fp=…#Name
```

Разбор теми же правилами, что `internal/vless` (`ParseURL`): `security` `tls` / `xtls` / `reality`, `type` (`tcp`, `xhttp`), `flow` с `vision`, `pbk` / `sid` включают Reality. Порт по умолчанию 443.

В редакторе две проекции той же строки импорта, как в v2rayN / NekoBox / Hiddify / 3x-ui:

- **Ссылка** — одна строка `vless://` (вставка из панели или Telegram).
- **Вручную** — адрес, порт, UUID, TLS/REALITY, TCP/XHTTP, затем поля режима (SNI, отпечаток, `pbk` / `sid`, Vision, путь/Host XHTTP).

Ручной ввод собирает `vless://`. Вставленная ссылка с транспортом, которого нет в этой сборке (`ws`, `grpc`, `kcp`), не трогается, пока не переключат на «Вручную».

На iOS / Android это подключение loft на **том же** системном VPN (фаза 5 плана). Это не вторая строка VPN.

---

## 6. Trojan

Обычная строка импорта существующих клиентов Trojan (trojan-gfw / Xray). Тип `trojan`. Пароль — userinfo URI. Поддерживаются TCP+TLS, REALITY, XHTTP stream-one, UDP ASSOCIATE. WS / gRPC / kcp нет. Подпись списка: `trojan · reality · host.example.com`.

```
trojan://<password>@<host>[:port]?security=…&sni=…&type=…&pbk=…&sid=…&fp=…#Name
```

Разбор теми же правилами, что `internal/trojan` (`ParseURL`): `security` `tls` / `reality`, `type` (`tcp`, `xhttp`), `pbk` / `sid` включают Reality, `allowInsecure=1` отключает проверку dest. Порт по умолчанию 443. Vision только у VLESS.

На iOS / Android это та же строка системного VPN, что loft. Код: `internal/trojan`.

---

## 7. SSTP

Тот же системный VPN, что loft. Тип `sstp`. Транспорт — TLS + PPP. Подпись списка: `sstp · tls · vpn.example.com`. Код: `internal/sstp`. Не возможность loft/1.0.

```
sstp://<user>:<password>@<host>[:port][/path]?sni=…&pap=1#Name
```

Порт по умолчанию **443**. Путь по умолчанию пустой (клиент берёт обычный URI SSTP Connect). `user` и `password` процентировать. Query `sni` — TLS ServerName; `pap=1` разрешает PAP/CHAP-MD5 (по умолчанию только MSCHAPv2).

YAML (как экспорт macOS):

```yaml
name: Office
kind: sstp
username: alice
password: "secret"
outbounds:
  - name: Office
    type: sstp
    url: https://vpn.example.com
```

JSON `.vpn` с host / user / password принимается как это подключение. Смена пути — повторный набор; IPCP может выдать новый IP.

---

## 8. Файл `.loft`

Семейный формат всех приложений loft (iOS, Android, macOS, Sailfish, позже TV). YAML. Файл **и есть** секрет.

Две формы. Обе законны.

### Одно подключение

Сегодняшний `client.yaml` плюс необязательный `kind`. Нет `kind` — вывести из `outbounds[].type` (`get`/`ws`/`h2`/`quic`/`h3` → `loft`, `sstp` → `sstp`, `vless` → `vless`, `trojan` → `trojan`).

```yaml
name: Home
kind: loft
token: "lt_change-me-long-random"
select: auto
outbounds:
  - name: cdn
    type: get
    url: https://cdn.example.com
    path: /static/v3/build
```

### Несколько подключений

Верхний `connections:` — список. Каждый элемент — одно подключение любого типа. Один файл **может** смешать loft, SSTP, VLESS и Trojan.

```yaml
connections:
  - name: Home
    kind: loft
    token: "lt_change-me-long-random"
    select: auto
    outbounds:
      - name: cdn
        type: get
        url: https://cdn.example.com
        path: /static/v3/build
  - name: Office
    kind: sstp
    username: alice
    password: "secret"
    outbounds:
      - name: Office
        type: sstp
        url: https://vpn.example.com
  - name: Travel
    kind: vless
    outbounds:
      - name: Travel
        type: vless
        url: vless://uuid@de1.example.com:443?security=reality&sni=www.example.com
  - name: Edge
    kind: trojan
    password: secret
    outbounds:
      - name: Edge
        type: trojan
        url: trojan://secret@de1.example.com:443?security=reality&sni=www.example.com
```

Документ **не должен** совмещать верхние `token:` / `outbounds:` с непустым `connections:`.

Импорт: каждый элемент списка становится профилем на диске. Телефон по-прежнему держит **один** туннель сразу. macOS может держать несколько сессий.

### Экспорт

| Что выбрано | Файл |
|-------------|------|
| Одно подключение, один фронт | QR / вставка `loft://` (или `sstp://` / `vless://` / `trojan://`) |
| Одно подключение, несколько фронтов | `Name.loft` (один документ) |
| Выбранное подключение | `Name.loft` (один документ) |
| Несколько / все подключения | `Family.loft` (`connections:`) |

Экспорт выбранного подключения — действие списка (iOS/Android: чип на строке с лампой; долгое нажатие; macOS: контекстное меню строки). Настройки **могут** по-прежнему экспортировать все.

Описание подключения в UI — **тип · транспорт · хост**, никогда транспорт один.

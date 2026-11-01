# URI импорта

[English](share.md) · **Русский** · [中文](share_zh.md)

Строка импорта на клиенте. Это **не** возможность протокола loft/1.0, и сессию она не устанавливает. Нода этот URL не видит.

Адрес loft — `loft://`: тип подключения и транспорт в одной строке. Ключи query `get` `ws` `h2` `h3` `quic` задают фронты **одного** подключения. Без них URI описывает один исходящий (`type` плюс путь URI). Файл `.loft` (YAML) содержит **одно или несколько подключений** и **может** смешивать все поддерживаемые типы (`loft`, `sstp`, `vless`, `trojan`). Формат файла: [§8](#8-файл-loft).

Протокол: [protocol_ru.md](protocol_ru.md). UX iOS: [clients/iOS/design/DESIGN_ru.md](../clients/iOS/design/DESIGN_ru.md). UX Android: [clients/Android/design/DESIGN_ru.md](../clients/Android/design/DESIGN_ru.md). UX macOS: [clients/macOS/design/DESIGN_ru.md](../clients/macOS/design/DESIGN_ru.md). UX Sailfish: [clients/Sailfish/design/DESIGN_ru.md](../clients/Sailfish/design/DESIGN_ru.md).

---

## 1. Детектор

Клиент, принимающий QR, вставку, фото или файл, проверяет по порядку:

1. Обрезать пробелы по краям UTF-8 и снять BOM.
2. `loft://routes/` → профиль маршрутов ([§9](#9-профили-маршрутов)), не подключение. Импорт в «Списках» идёт по этому пути.
3. `loft://` → [§2](#2-loft).
4. `vless://` → ссылка VLESS ([§5](#5-vless)).
5. `trojan://` → ссылка Trojan ([§6](#6-trojan)).
6. `sstp://` → ссылка SSTP ([§7](#7-sstp)).
7. YAML с `token:` / `password:` / `outbounds:` / `connections:` → импорт как `.loft` ([§8](#8-файл-loft)). YAML `kind: routes` — [§9](#9-профили-маршрутов), не подключение.
8. Иначе отказ.

Один и тот же детектор работает для камеры, картинки из Фото или Файлов, файлов `.txt` / `.loft` / YAML и поля вставки.

Полезная нагрузка QR — URI (или YAML) в UTF-8. Уровня коррекции **M** достаточно. Обычный `loft://` помещается в QR версии 10. Пакет из нескольких подключений может не поместиться в QR; тогда нужен файл.

---

## 2. loft://

Канонический адрес loft:

```
loft://<token>@<host>[:port][/path][?query][#name]
```

Клиенты выдают и принимают `loft://`.

Синтаксис по RFC 3986. `token` и `name` кодируются процентным кодированием (`application/x-www-form-urlencoded` для фрагмента не подходит). Порт по умолчанию **443**. IPv6-адрес хоста пишется в квадратных скобках.

Схема **и есть** тип подключения (`loft`). Хост:порт — это authority (`example.com:443`). Пути фронтов задаются ключами query. Query `type` обозначает **транспорт** только в старой форме. Строка в списке: `loft · get · cdn.example.com`.

| Часть | Куда в `client.yaml` |
|-------|----------------------|
| схема `loft` | `kind: loft` |
| `token` (userinfo) | `token` (`lt_…`) |
| `host`[:`port`] | хост ноды. Фронты обращаются к нему. Единственное исключение — `edge`, и только для первого GET |
| `/path` | старая форма: исходящий `path`. Игнорируется, если есть `get=` / `ws=` / `h2=` |
| `#name` | имя подключения; если пусто — `host` |

### Query

| Ключ | Значения | По умолчанию | YAML |
|------|----------|--------------|------|
| `get` | HTTP-путь | `/static/v3/build`, если ключ есть, но пуст | исходящий GET |
| `ws` | HTTP-путь | `/api/v1/sync`, если ключ есть, но пуст | исходящий WS |
| `h2` | HTTP-путь | `/api/v1/tunnel`, если ключ есть, но пуст | исходящий H2 |
| `h3` | HTTP-путь | `/api/v1/tunnel`, если ключ есть, но пуст | исходящий H3 |
| `quic` | пустой флаг | — | исходящий QUIC (без пути) |
| `type` | `get` `ws` `h2` `quic` `h3` `l3` | `get` | один исходящий, только если нет `get`/`ws`/`h2`/`h3`/`quic` |
| `cdn` | имя профиля (`yandex`, …) | нет | исходящий `cdn` |
| `edge` | абсолютный URL `http` или `https` | нет | клиентский `edge`. Только GET |
| `fp` | отпечаток TLS (`chrome`, …) | `chrome` | исходящий `fingerprint` |
| `scheme` | `https` `http` `wss` `ws` | по фронту | схема `url` |
| `pbk` | открытый ключ X25519 ноды, base64url или hex, 32 байта | нет | исходящий `mask_pub` |

Наличие ключа `get` / `ws` / `h2` / `h3` / `quic` включает соответствующий фронт. Пустой `get=` означает путь GET по умолчанию. Несколько таких ключей дают несколько исходящих **одного** подключения (`select: auto`). После Status источником истины служит карта фронтов, которую сообщает нода.

`scheme` по умолчанию: для `ws` — `wss`, для остальных — `https`.

URL каждого исходящего собирается так: `{scheme}://{host}:{port}` плюс путь этого фронта.

`edge`, если задан, — абсолютный URL `http` или `https`. Первый GET идёт на него. Путь берётся из этого URL, если он не `/`, иначе из `get=`. Следующие GET и `ws` / `h2` / `quic` / `h3` идут на `host`. Без `edge` все фронты идут на `host`. `cdn` по-прежнему означает имя профиля, а не этот URL. `edge: plain|tight` в конфиге ноды — пресет Status, а не этот параметр. Клиент, который не знает `edge`, игнорирует его и обращается к `host`.

`insecure_skip_verify`, `header_budget` и `inflight` в URI не включать: клиент использует свои обычные значения. Ключ участника в query тоже не включать.

Неизвестные ключи query клиент **должен** игнорировать (совместимость с будущими версиями). Неизвестный `type` — ошибка.

### Примеры

Одно подключение, несколько фронтов:

```
loft://lt_change-me-long-random@example.com:443/?get=/static/v3/build&ws=/api/v1/sync&h2=/api/v1/tunnel&quic&fp=chrome#bob
```

Хост ноды, CDN только для GET:

```
loft://lt_change-me-long-random@loft.example.com:444/?get=/static/v3/build&ws=/api/v1/sync&h2=/api/v1/tunnel&quic&edge=https://cdn.example.com#Home
```

`url` остаётся `https://loft.example.com:444`. При подключении клиент отправляет GET на `https://cdn.example.com` (путь `/static/v3/build`) раньше GET на ноду. `ws`, `h2` и `quic` остаются на `loft.example.com:444`.

GET через CDN (старая форма, по-прежнему принимается):

```
loft://lt_change-me-long-random@cdn.example.com/static/v3/build?type=get&cdn=yandex&fp=chrome#Home
```

превращается в:

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

Клиент с функцией «Поделиться» выдаёт `loft://` для **выбранного** подключения (с ключом query на каждый фронт loft). Для нескольких подключений или разных типов — экспорт в `.loft` ([§8](#8-файл-loft)). URI — секрет: в нём ключ участника. Показывать его можно на устройстве, писать в журнал нельзя.

iOS регистрирует схему URL `loft`, Android — такой же intent-фильтр, Sailfish (и Аврора) — `x-scheme-handler/loft`. Нажатие на `loft://…` в Сообщениях открывает приложение и предлагает импорт.

---

## 3. Камера и файл

| Источник | API |
|----------|-----|
| Живой QR (iOS) | `AVCaptureMetadataOutput`, тип QR. `NSCameraUsageDescription`. |
| Фото | iOS: `PHPicker`. Android: Photo Picker. macOS: панель открытия. QR со статичного кадра. |
| Файл | Картинки, `public.plain-text`, YAML, `.loft`, `.vpn`. QR со статичного кадра; иначе байты по §1. |

После распознавания открывается экран подтверждения с **именем**, **типом**, **транспортом** и **хостом**. Ключ скрыт. При сохранении данные пишутся в Keychain и YAML в App Group. Подключение — отдельное нажатие.

---

## 4. Чем это не является

Это не возможность Hello/Status, не новая команда mux, не замена `.loft` и не URL подписки, который клиент периодически опрашивает.

---

## 5. VLESS

Обычная строка импорта, как у существующих клиентов VLESS. Тип `vless`. Транспорт задают VLESS-параметры `type` / `security`. Поддерживаются TCP+TLS, REALITY, Vision (`xtls-rprx-vision`), XHTTP. Подпись в списке: `vless · reality · host.example.com`.

```
vless://<uuid>@<host>[:port]?security=…&sni=…&type=…&flow=…&pbk=…&sid=…&fp=…#Name
```

Разбор идёт по тем же правилам, что в `internal/vless` (`ParseURL`): `security` `tls` / `xtls` / `reality`, `type` (`tcp`, `xhttp`), `flow` с `vision`; `pbk` / `sid` включают Reality. Порт по умолчанию 443.

Редактор показывает одну и ту же строку импорта в двух видах, как v2rayN / NekoBox / Hiddify / 3x-ui:

- **Ссылка** — одна строка `vless://` (вставка из панели или Telegram).
- **Вручную** — адрес, порт, UUID, TLS/REALITY, TCP/XHTTP, затем поля режима (SNI, отпечаток, `pbk` / `sid`, Vision, путь/Host для XHTTP).

Ручной ввод собирает `vless://`. Вставленная ссылка с транспортом, которого нет в этой сборке (`ws`, `grpc`, `kcp`), остаётся нетронутой, пока не переключиться на «Вручную».

На iOS / Android это подключение работает через **тот же** системный VPN, что и loft (фаза 5 плана), а не как вторая строка VPN.

---

## 6. Trojan

Обычная строка импорта, как у существующих клиентов Trojan (trojan-gfw / Xray). Тип `trojan`. Пароль передаётся в userinfo URI. Поддерживаются TCP+TLS, REALITY, XHTTP stream-one, UDP ASSOCIATE. WS / gRPC / kcp не поддерживаются. Подпись в списке: `trojan · reality · host.example.com`.

```
trojan://<password>@<host>[:port]?security=…&sni=…&type=…&pbk=…&sid=…&fp=…#Name
```

Разбор идёт по тем же правилам, что в `internal/trojan` (`ParseURL`): `security` `tls` / `reality`, `type` (`tcp`, `xhttp`); `pbk` / `sid` включают Reality, `allowInsecure=1` отключает проверку dest. Порт по умолчанию 443. Vision есть только у VLESS.

На iOS / Android это та же строка системного VPN, что и у loft. Код: `internal/trojan`.

---

## 7. SSTP

Тот же системный VPN, что и у loft. Тип `sstp`. Транспорт — TLS + PPP. Подпись в списке: `sstp · tls · vpn.example.com`. Код: `internal/sstp`. Это не возможность loft/1.0.

```
sstp://<user>:<password>@<host>[:port][/path]?sni=…&pap=1#Name
```

Порт по умолчанию **443**. Путь по умолчанию пустой (клиент использует стандартный URI SSTP Connect). `user` и `password` кодируются процентным кодированием. Query `sni` задаёт TLS ServerName; `pap=1` разрешает PAP/CHAP-MD5 (по умолчанию только MSCHAPv2).

YAML (как при экспорте в macOS):

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

JSON `.vpn` с host / user / password тоже импортируется как такое подключение. При смене пути клиент заново дозванивается; IPCP может выдать новый IP.

---

## 8. Файл `.loft`

Общий формат для всех приложений loft (iOS, Android, macOS, Sailfish, позже TV). YAML. Сам файл **и есть** секрет.

Допустимы обе формы.

### Одно подключение

Нынешний `client.yaml` плюс необязательный `kind`. Если `kind` нет, он выводится из `outbounds[].type` (`get`/`ws`/`h2`/`quic`/`h3`/`l3` → `loft`, `sstp` → `sstp`, `vless` → `vless`, `trojan` → `trojan`).

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

На верхнем уровне — список `connections:`. Каждый элемент — одно подключение любого типа. Один файл **может** смешивать loft, SSTP, VLESS и Trojan.

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

Документ **не должен** содержать одновременно `token:` / `outbounds:` верхнего уровня и непустой `connections:`.

При импорте каждый элемент списка становится отдельным профилем на диске. Телефон по-прежнему держит только **один** туннель одновременно. macOS может держать несколько сессий.

### Экспорт

| Что выбрано | Файл |
|-------------|------|
| Одно подключение, один фронт | QR / вставка `loft://` (или `sstp://` / `vless://` / `trojan://`) |
| Одно подключение, несколько фронтов | `Name.loft` (один документ) |
| Выбранное подключение | `Name.loft` (один документ) |
| Несколько / все подключения | `Family.loft` (`connections:`) |

Экспорт выбранного подключения — действие в списке (iOS/Android: чип на строке с лампой или долгое нажатие; macOS: контекстное меню строки). Из настроек по-прежнему **можно** экспортировать все подключения.

В интерфейсе подключение описывается как **тип · транспорт · хост** и никогда — одним транспортом.

---

## 9. Профили маршрутов

Подписанный блоб списков (§10.3) отображается в документ SplitStore (`src=op`). Этим документом делятся как **профилем маршрутов**, не как подключением loft. Детектор подключений **обязан** его отказать (`loft://routes` без токена, иначе получилось бы подключение с именем `routes`).

```
loft://routes/<base64url(SplitJSON)>
```

`ParseRoutes` принимает и сам объект SplitJSON, и YAML:

```yaml
kind: routes
name: RU напрямую
dns: 77.88.8.8
src: op
zones: […]
services: […]
```

либо `kind: routes` и `profile:` с тем же payload в base64url. **+ Импорт профиля** в «Списках» добавляет его в каталог; активный профиль не трогается. Профиль оператора, который приходит внутри сессии (`lists_inline`), перезаписывает слот `op` и только для чтения.

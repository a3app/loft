# Архитектура эталонной ноды

[English](architecture.md) · **Русский** · [中文](architecture_zh.md)

ПО 1.0.24. Протокол 1.0 (loft/1.0). Протокол: [protocol_ru.md](protocol_ru.md). Эксплуатация: [guide_ru.md](guide_ru.md).

Документ описывает дерево на Go в этом репозитории. Сторонняя реализация **может** отличаться устройством каталогов и всё равно говорить по протоколу.

---

## 1. Кратко

Эталонная нода — один двоичный файл `loft-node` в двух ролях: контроллер и relay. Эталонный клиент — `loft` (CLI: SOCKS5 и необязательный TUN). `loft-front` — та же сессия без флагов CLI для системных маршрутов; приложение в строке меню macOS запускает его как помощник. `loft-probe` проверяет ICMP/TCP/UDP/DNS/HTTP через сессию. Общий код живёт в `internal/`.

---

## 2. Дерево

```
docs/                 протокол, архитектура, гайд
  brand/              марка и иконки
internal/
  wire/               кадры, Hello, Status, roster, списки, флот, возможности, коды
  crypto/             HKDF, ChaCha20-Poly1305, auth 0x02 + X25519, Ed25519 seed
  identity/           ключ участника lt_ + 48 hex; SHA-256 в network.conf; открытый текст в members.keys
  mux/                потоки, обратное давление, CmdDgram, CmdLeave
  netconf/            network.conf, аренды, ACL, VLAN, DNS, таймеры сессии
  transport/          фронты, decoy, mask, транк relay, оверлей, p2p, заглушка DNS, запрос roster
  config/             client.yaml, server.yaml
  cdn/                профили края (Yandex, Cloudflare); не возможности протокола. Оператор: docs/yandex-cdn_ru.md
  version/            имя программы, semver, диапазон протокола
server/
  cmd/loft-node
  origin/             страница прикрытия, GET, WebSocket, HTTP/2 CONNECT, публичный roster/списки
  configs/            server.yaml, relay.yaml
  debian/             установщик, unit systemd, .deb
  panel/              веб-панель loft-panel (план и макет: panel/design)
clients/
  go/cmd/loft         CLI: SOCKS5, TUN, kill-switch
  go/cmd/loft-front   помощник сессии (бандл macOS; RPM Авроры, libexec)
  go/cmd/loft-probe   ICMP/TCP/UDP/DNS/HTTP оверлея без переписывания маршрутов
  go/tools/wsprobe    одноразовое GET-рукопожатие (вручную; не в dist)
  macOS/Sources       Swift 5 + AppKit, строка меню; запускает loft-front
  macOS/pack          DMG (симлинк Applications), подпись, entitlements
  macOS/design        спецификация UX и макет
  macOS/tests         вектор RFC 8439 AEAD (make -C clients/macOS test-crypto)
  Keenetic/           каталог Entware OpkgTun (план и макет; сборки ещё нет)
  MikroTik/           контейнер RouterOS 7 (образ + .rsc; сначала SOCKS; не в Dist)
  iOS/                Packet Tunnel (тонкий хост + appex; gomobile xcframework; Apple TV в плане, фаза 6)
  Android/            VpnService (тонкий хост Java; gomobile AAR; UX-макет; Android TV / Google TV в плане, фаза 4)
  Aurora/             оверлей ConnMan (хост QML + плагин loft-connman + loft-front)
  mobile/             общий движок Go для iOS/Android (loft, SSTP, VLESS)
```

`wire` — протокол. `transport` — как это дерево его несёт. `cdn` — профиль развёртывания: порядок заголовков и SNI именованного края. На канале как возможность протокола он **не должен** появляться.

---

## 3. Сессия

Сессия участника — соединение, auth blob, Status, затем mux.

```
client.Holder
    Supervise(cfg)                 перебирает исходящие и фронты
        Open(target)               get | ws | h2 | quic | h3
            TLS 1.3 (uTLS)
            auth 0x02 (nonce || eph_c || HMAC)
            Hello MemberCaps (без CapRelay; ExtraCaps у loft-node relay)
            eph_n || AEAD(Status)
            mux Open / Data / Close / Dgram / Leave
```

`Supervise` при `select: auto` пробует настроенные исходящие по порядку списка. После первого Status оставляет только пересечение фронтов. При отказе идёт к следующему оставшемуся фронту, затем к неподписанным `replicas`, затем к подписанному roster. Нода без `fronts` рекламирует только GET; `ws`, `h2`, `quic` и `h3` перечисляют явно.

`WatchPath` снимает исходящий IPv4 по умолчанию примерно четыре раза в секунду. На GET смена адреса Wi-Fi / LTE / VPN меняет сокеты HTTP/2; sid, mux и seq остаются (без `0x02`). `ws`, `h2`, `quic` и `h3` бросают фронт; Supervise делает Park и возобновляет свежим `0x02` и тем же `instance_id`. SOCKS5 и TUN остаются привязаны (`Holder`); IP оверлея не меняется. При возможности `hold` Park держит mux-потоки `gap_hold_ms` (по умолчанию 8 с), внутренний TCP не получает RST. GET-рукопожатие после Park подхватывает этот mux по `instance_id` (новый HTTP sid, тот же mux). Этот цикл крутят и `loft`, и `loft-front`.

Запрос roster требует `controller_pub`. `tofu: true` закрепляет первого действительного подписанта и пишет `{instanceDir}/controller.pub`. Пустой ключ без этого флага roster не принимает; статические исходящие всё равно звонят.

Nonce внутреннего AEAD уникален на ключ сессии (32-битный префикс + 64-битный счётчик). Park с тем же ключом продолжает счётчик. Новый `0x02` считает ключ из X25519; ikm HKDF — секрет DH.

Клиент decoy SNI не выбирает. Его даёт Status, roster или сертификат зонда. Исходящий CDN сохраняет hostname CDN.

Локальный вход (SOCKS5, TUN) вызывает `Holder.Open(host:port)` или шлёт датаграмму. Адреса оверлея разбирает `NetSwitch` на ноде. Status `dns` — список IP резолверов; нода отвечает `gateway:53` из `dns.upstream` (DoH, DoT или UDP). Необязательный хвост Status `ip6` / `gateway6` при отсутствии означает только IPv4.

---

## 4. Роли ноды

### 4.1 Контроллер

Хранит:

- ключи участников как SHA-256 в `network.conf`; открытый текст в `{dir}/members.keys` (`keep_keys` даёт оператору перечитать файл). `auth shared enable` принимает `LOFT_TOKEN` как секрет клиента, только пока это кольцо пустое.
- аренды и IP оверлея
- ACL и VLAN
- Ed25519 seed `{dir}/controller.seed`
- живой roster и списки адресов

Он **может** слушать 443 для участников. Транки relay слушать **должен**.

Seed подписи создаётся при первом старте. Открытый ключ отдаётся клиентам как `controller_pub`. Эталонный клиент не закрепляет неизвестного подписанта, пока не задано `tofu: true`.

### 4.2 Relay

Завершает публичный TLS. Открывает один транк к контроллеру с возможностью `relay` в Hello. На этой сессии:

- `ctl:1` — enrol, beat, slice, drain
- на каждого участника — `hub:1` (HTTP/1.1) или `hub:2` (HTTP/2)

Relay копирует HTTP после TLS на `hub:1` / `hub:2`. Внутренний mux AEAD и FinishAuth остаются на контроллере. Relay оверлейные кадры не расшифровывает. Аренды не выдаёт. Ключи участников не хранит. IP оверлея назначает контроллер.

IP relay сменный. Снятие из roster увеличивает serial. Клиенты, у которых уже есть Status, держат сессию до отказа, затем берут новый roster.

### 4.3 Origin

`server/origin` — HTTPS-приложение, которое видит публичный путь:

- страница прикрытия (`cover_dir` или встроенная страница)
- GET-фронт, WebSocket, HTTP/2 CONNECT
- публичный roster и JSON списков

Зонд, ушедший в стыковку decoy, до этого обработчика не доходит: его пересылают на настроенный origin до HTTP.

### 4.4 Хост Debian

unit systemd, установщик и `.deb`: [server/debian/README_ru.md](../server/debian/README_ru.md). Токен в `/etc/loft/loft.env`. Документ оверлея в `/var/lib/loft`. Пользователь процесса `loft`, прослушивание 443 с `CAP_NET_BIND_SERVICE`.

---

## 5. Decoy и mask

`transport.WrapDecoy` заглядывает в TCP ClientHello.

```
Accept
  read ClientHello (или оставшиеся байты)
  if valid session_id and own SNI
      сертификат ноды (участник на advertise, CDN после sid)
  else if own SNI from cdn_peers
      сертификат ноды (CDN origin pull)
  else if session_id = sid v3 (X25519 ноды) или sid v2 (HMAC участника)
      if mask ready → лист ECDSA формы dest + выравнивание записей
      else            → обработчик HTTP loft
  else
      TCP-стыковка с decoy origin (совпадение SNI, иначе основной)
UDP
  decoy on → стыковка датаграмм с dest
  decoy off → loft QUIC/H3
```

Куда стыковать — конфигурация ноды. Клиент это узнаёт. Имена хостов в YAML — примеры. Константами протокола они не являются.

Mask выпускает лист ECDSA P-256 с личностью origin и подписывает CertificateVerify как `ecdsa_secp256r1_sha256`. Серийник привязан к ECDH с X25519 ноды. Зонды по-прежнему стыкуются. Записи приложения **могут** следовать длинам ciphertext origin.

---

## 6. Ткань оверлея

`transport.Overlay` привязывает каждую допущенную сессию к IP оверлея. Open на адрес внутри CIDR сети классифицируется по `peers`:

| режим | действие |
|------|----------|
| hub | Reset `policy_drop` |
| mesh | стыковка с mux-сессией пира как Open `127.0.0.1:port` |
| vlan | mesh, если VLAN совпал |

TCP Open и ICMP оверлея идут через `Network.Allow` (теги, CIDR, VLAN, `default deny`). `ip route DST VIA|member ID|tag NAME` — CIDR за участником: Open `host:port` стыкуется на этого участника с исходным target. Status `routes` перечисляет эти CIDR с уже разрешённым overlay IP; владельца VIA нет в списке. Open в интернет (вне CIDR и этих маршрутов) не меняется. Прямой P2P берёт Status `policy` и тот же Allow. `session ttl_ms` закрывает живую сессию; `lease_hold_ms` держит overlay IP после обрыва.

Принимающий клиент принимает Open и открывает target. Стыковка overlay-IP — `127.0.0.1:port`. Routed — `host:port` на стеке хоста участника.

Необязательный P2P: клиент слушает, объявляет `loft.peer:<port>` и 32-байтный X25519 pub, Status несёт хост, увиденный на TLS, порт и pub. Прямой TCP — предисловие `LFT` 42 байта (HMAC от ECDH пары с pub пира), затем тот же ACL, что стыковка через хаб, когда Status несёт policy. При отказе — стыковка через хаб. `p2p: false` на клиенте слушать не начинает. Seed: `{instanceDir}/p2p.seed`.

---

## 7. Списки адресов

Контроллер публикует именованные множества. Клиент сопоставляет имена с действиями (`use:`). Порядок при нескольких попаданиях: drop, затем direct, затем via. Для несовпавших назначений по умолчанию — `via`.

SOCKS сопоставляет hostname запроса. TUN сопоставляет IP назначения по CIDR. Виды доменов к сырым IP-сокетам не применяются.

Списки запрашиваются, когда Status `lists_serial` или `lists_hash` новее кэша. Подпись — тот же Ed25519, что у roster.

---

## 8. Локальный вход

| слушатель | по умолчанию | заметки |
|-----------|--------------|---------|
| SOCKS5 | `127.0.0.1:1080` (GUI / `client.yaml`); `127.0.0.1:18080` (`loft` / `loft-front`, если порт не задан) | RFC 1928. CONNECT и UDP ASSOCIATE, когда `dgram` включён. |
| TUN | выкл; `tun: auto` создаёт utun | пользовательский стек gVisor. UDP через CmdDgram. ICMP echo: себе локально; оверлей и интернет как Dgram `:1` (нода шлёт настоящий ICMP echo). Darwin транслирует фиктивный `169.254.8.1` ↔ IP оверлея на ICMP. |
| kill-switch | `listen.kill_switch` | когда последний TUN умирает, системный маршрут по умолчанию остаётся снятым, пока пользователь не вернёт его |

TUN исключает исходящие хосты сессии loft (CDN, нода), чтобы туннель не проглотил свой путь.

Клиент в строке меню macOS — Swift 5 + AppKit в [`clients/macOS/Sources/`](../clients/macOS/Sources/). UX: [`clients/macOS/design/DESIGN_ru.md`](../clients/macOS/design/DESIGN_ru.md). Живая сессия — `loft-front` в бандле (`Contents/MacOS/loft-front`), не тип `GETFront.swift` внутри процесса. Hello этого помощника — клиент на Go (GET и WS). Удержание маршрута, запасной путь и раздельные списки в дизайн-документе — политика машины. Они сидят выше флагов CLI на Go.

---

## 9. Запасной путь

Порядок попыток после мёртвой сессии:

1. Оставшиеся фронты пересечения, тот же исходящий.
2. Следующий исходящий в порядке `select: auto`.
3. Неподписанные `replicas` из Status.
4. Подписанные записи roster, по весу, минуя drain.

Таймеры повторного подключения приходят из Status: `reconnect_min_ms`, `reconnect_max_ms`, `reconnect_fast_n`, `reconnect_fast_for_ms`. Сначала быстрые попытки, затем экспоненциальная пауза.

Реплика принимает свежий `0x02`. Hello `instance_id` держит аренду оверлея.

Mux `Pull` сначала снимает Open/Ping/Pong/Dgram/Leave и мелкий Data (≤ 4 КиБ), потом основной Data/Pad, чтобы полная загрузка не стопорила ICMP, TCP ACK и loaded-ping Cloudflare. Close/Reset остаются в очереди Data, чтобы штатное закрытие не перепрыгнуло оставшиеся байты. Очередь Data — 48 кадров. Предвыборка WS — 2×48 КиБ; у управляющих кадров своё сообщение 8 КиБ.

`session rate_bps` (умолчание сети, `config member ID rate_bps` — override) — токен-бакет на балк `CmdData`/`CmdDgram` сессии участника, оба направления. 0 = выкл. Управление и мелкие кадры не шейпятся. Status `rate_bps` объявляет потолок; нода применяет. Стволы реле без потолка.

---

## 10. Параллельность

Одна mux-сессия на экземпляр участника. Потоки — горутины с ограниченным приёмным буфером. Полезная нагрузка mux ≤ 16384; `Stream.Write` и `EncodeBatch` дробят CmdData, чтобы `io.Copy` на 32 КиБ не отбросил кадр.

У GET-фронта ограниченное окно одновременных запросов (протокол 2–8, по умолчанию 4; в этом дереве по умолчанию 8). Цикл клиента — единственный Puller исходящего канала и выдаёт GET `seq` в порядке Pull. Нода применяет исходящее по seq; только GET по порядку вытягивает входящий mux, чтобы потерянный HTTP-ответ не унёс кадры, уже взятые из mux (YouTube/Speedtest TLS). Темп простоя GET — `keepalive_ms / 2` (не меньше 1 с) с джиттером ±⅓. Open и крошечный Data ждут 15 мс, датаграммы 80 мс, чтобы ICMP ехал в том же теле. Общий GET `header_budget` в этом дереве — 16 КиБ HTTP-заголовка (декодированный mux ~12 КиБ после base64url+AEAD, ~13 КиБ после Cookie Z85); профиль Yandex фиксирует 16 КиБ encoded. Status `h2_conns` (на ноде `get_h2`, 1–4) — дополнительные HTTP/2-соединения как второй конверт бюджета заголовков; inflight по-прежнему сумма. Необязательный клиентский `h2_max_age_ms` (0 — держать, пока не будет ошибки) меняет этот TCP; mux и GET sid остаются. Смена локального исходящего адреса меняет пул HTTP/2 так же (без нового `0x02`). GET, в чьём исходящем уже ≥ 2 КиБ CmdData, режет тело до 96 КиБ, чтобы слот Cookie быстрее вернулся; только скачивание остаётся 256 КиБ.

Транки relay — одна mux-сессия на процесс relay, много потоков `hub:*`.

`Supervise` — один цикл на клиентский процесс. Несколько YAML-профилей (несколько профилей macOS) — несколько процессов или несколько Holder. Это не возможность `multi`. Возможность `multi` — несколько значений `instance_id` на одном ключе участника.

---

## 11. Что живёт снаружи

Виртуальные машины, публичный DNS, публичные сертификаты и обновления двоичных файлов лежат вне этого протокола. unit systemd, POSIX-установщик и `.deb` живут в [`server/debian/`](../server/debian/). По каналу идут enrol, beat, slice, drain, roster и списки. Оператор выдаёт ключи участникам и пишет `network.conf` плюс `members.keys`.

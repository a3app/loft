# Запуск loft

[English](guide.md) · **Русский**

ПО 1.0.3. Протокол 1.0 (loft/1.0). Протокол: [protocol_ru.md](protocol_ru.md). Устройство: [architecture_ru.md](architecture_ru.md).

Это эталонная реализация.

---

## 1. Сборка

Go 1.24 или новее, из корня репозитория:

```bash
make test
make node
make client    # dist/clients/go/loft
make probe     # dist/clients/go/loft-probe
make macos     # dist/clients/macOS/loft.app
make macos-dmg # dist/clients/macOS/loft-*-macos-<arch>.dmg  (симлинк Applications)
make macos-dmgs # оба DMG, arm64 и x86_64 (CI)
make dist      # tarball/.deb ноды + архивы CLI
```

```bash
go test ./...
go build -ldflags "-s -w -X github.com/a3app/loft/internal/version.Version=$(cat VERSION)" -o loft-node ./server/cmd/loft-node
go build -ldflags "-s -w -X github.com/a3app/loft/internal/version.Version=$(cat VERSION)" -o dist/clients/go/loft ./clients/go/cmd/loft
go build -ldflags "-s -w -X github.com/a3app/loft/internal/version.Version=$(cat VERSION)" -o dist/clients/go/loft-probe ./clients/go/cmd/loft-probe
```

`--version` печатает имя программы, semver и диапазон протокола.

`make client` собирает `dist/clients/go/loft`. `make probe` — `loft-probe`. Приложению macOS ещё нужен `loft-front`:

```bash
go build -ldflags "-s -w -X github.com/a3app/loft/internal/version.Version=$(cat VERSION)" -o dist/clients/go/loft-front ./clients/go/cmd/loft-front
```

---

## 2. Роли ноды

**Контроллер** хранит ключи, аренды, билеты, ACL и ключ подписи roster и списков. Он **может** слушать 443 для участников.

**Relay** завершает публичный TLS и копирует открытый текст на контроллер по транку. Его IP сменный. Личность участника и IP оверлея остаются на контроллере.

Обе роли — один двоичный файл. Роль задаётся YAML: `role: controller` или `role: relay`.

---

## 3. Сеть оверлея

Путь: `{dir}/network.conf` (старый `{dir}/network.yaml` ещё загружается). Без этого файла нода не стартует.

```
network home
name home
enable
ipif overlay 10.8.0.0/24
  gateway 10.8.0.1
  pool 10.8.0.10 10.8.0.220
  mtu 1280
  peers hub
  p2p enable
  dns enable
  dns 10.8.0.1
  dns upstream https://1.1.1.1/dns-query
  dns upstream tls://1.1.1.1
  listen 0.0.0.0 443 front ws
create member alice
config member alice multi enable 4
config member alice key_sha256 <hex>
enable member alice
ip access-list overlay
  permit tcp host 10.8.0.2 host 10.8.0.5 eq 22
```

`dns.servers` попадает в Status и прописывается на клиентский TUN. На запросы к шлюзу на порт 53 (UDP CmdDgram и TCP Open) отвечает нода. `dns.upstream` — как нода достаёт ответы: `https://` это DNS-over-HTTPS, `tls://` это DNS-over-TLS (порт 853), просто IP — UDP/53. `quic://` зарезервирован. Пустой `servers` означает шлюз. `enabled: false` системный DNS не трогает.

Ключ участника — `lt_` плюс 48 шестнадцатеричных символов (24 байта). `network.conf` хранит SHA-256. Открытый текст живёт в `{dir}/members.keys` (режим 0600): HMAC и HKDF с хеша не посчитать. При `keep_keys enable` (так по умолчанию) оператор может перечитать файл и снова выдать YAML клиента. `keep_keys disable` показывает YAML только в момент выдачи; ноде файл с открытым текстом всё равно нужен.

Когда `members.keys` пуст, нода ещё принимает `LOFT_TOKEN` как секрет клиента (старый общий токен). Как только появляется любой ключ участника, клиенты аутентифицируются им; `LOFT_TOKEN` остаётся секретом ноды для mask и seed контроллера.

YAML клиента несёт `member_id` и `token` этого участника. Hello `member_id` берётся из совпавшего ключа, не из заявления клиента.

| `peers` | Overlay Open к другому участнику |
|---------|----------------------------------|
| `hub` | Reset `policy_drop`. Open в интернет не меняется. |
| `mesh` | Прямой TCP, если `p2p` позволяет, иначе стыковка через ноду. |
| `vlan` | Как mesh, только та же VLAN. |

Необязательные команды: `create vlan`, `ip route`, `ip access-list` (расширенный ACL Cisco, первое совпадение, `permit`/`deny`), таймеры `session` (`handshake_ms`, `idle_ms`, `keepalive_ms`, `lease_hold_ms`, `gap_hold_ms`).

Клиент **может** поставить `p2p: false` и тогда пользуется только стыковкой через хаб. Прямой TCP: клиент слушает, объявляет `loft.peer:<port>`, Status несёт хост, который нода увидела на TLS. За NAT попытка не удаётся, и тот же поток идёт через ноду. Через relay увиденный хост — сам relay.

---

## 4. Контроллер

Пример: `server/configs/server.yaml`.

```bash
export LOFT_TOKEN='long-secret'
./loft-node -c server/configs/server.yaml -dir /var/lib/loft
```

| Флаг | Смысл |
|------|---------|
| `-c` | YAML: роль, decoy, фронты, roster, TLS |
| `-dir` | `network.conf`, `members.keys`, `controller.seed` |
| `-listen` | Переопределить адрес прослушивания |
| `-cert` `-key` | PEM, если нет в YAML |
| `--version` | Версии |

| Поле | Смысл |
|------|--------|
| `listen` | `:443` |
| `cert` `key` | Сертификат ноды. С decoy+mask клиенту loft показывают лист формы dest. |
| `token` | Секрет ноды (`LOFT_TOKEN`): mask, seed контроллера. Ключи участников — `members.keys`. |
| `get_path` `ws_path` `h2_path` `h3_path` | Пути фронтов |
| `get_h2` | Число HTTP/2-соединений GET, 1–4 (по умолчанию 1). Inflight общий на все. |
| `fronts` | Пусто = все. Иначе только перечисленные имена |
| `decoy` | список `host:port`. Первая запись — личность ноды. Пусто = выкл |
| `mask` | Рукопожатие ECDSA формы dest для клиентов loft; зонды по-прежнему стыкуются |
| `role` | `controller` |
| `advertise` | Публичный URL этого фронта в roster |
| `roster_path` | Публичный GET, по умолчанию `/static/v3/manifest.json` |
| `enrol` | `list`: id должен быть в `relays:`. `open`: любой транк `CapRelay` |
| `relays` | Список допущенных: id, url, decoy, fronts, weight, path |
| `lists` | Имя → адреса (домен, `.suffix`, CIDR) |
| `lists_path` | Публичный GET, по умолчанию `/static/v3/lists.json` |
| `canary` | Канарейка пути: `fat_hello`, `split_hello`, `pad` |
| `cover_dir` | Корень статики. Пусто = встроенная страница |
| `replicas` | Неподписанные дополнительные `host:port` для запасного пути |
| `quic_alpn` | По умолчанию `h3` |

Seed подписи: `{dir}/controller.seed`. Открытый ключ отдайте клиентам как `controller_pub`.

Живой roster = `advertise` плюс отвечающие heartbeat relay без drain. Три пропущенных удара (~30 с) или мёртвый поток `ctl` снимают relay и увеличивают serial.

Пути **следует** делать похожими на ресурсы или API обычного сайта. В этом дереве по умолчанию: `/static/v3/build`, `/api/v1/sync`, `/api/v1/tunnel`, `/static/v3/manifest.json`, `/static/v3/lists.json`.

---

## 5. Relay

Пример: `server/configs/relay.yaml`.

```bash
./loft-node -c server/configs/relay.yaml -dir /var/lib/loft-relay
```

| Поле | Смысл |
|------|--------|
| `role` | `relay` |
| `id` | Должен совпасть с `relays[].id` при `enrol: list` |
| `controller` | URL хаба: `wss://host/api/v1/sync` или `https://host` |
| `advertise` | URL, которым клиенты ходят на этот край |
| `decoy` `mask` | Личность этого IP |
| `token` | Тот же, что у контроллера, или `relay_token` на обоих |
| `fronts` | Фронты, которые отдаёт этот край |
| `cert` `key` | Публичный TLS этого IP |

Relay открывает транк с возможностью `relay` в Hello, затем `ctl:1` (enrol, beat) и на каждого участника `hub:1` или `hub:2`.

Сертификаты, DNS и сама машина готовятся вне протокола. Enrol — как контроллер узнаёт, что край жив.

---

## 6. Клиент

Двоичный файл `loft`. Пример: `clients/go/configs/client.yaml`.

```bash
./dist/clients/go/loft -c clients/go/configs/client.yaml
ALL_PROXY=socks5://127.0.0.1:1080 curl https://example.com
```

Локальные слушатели: SOCKS5 (`127.0.0.1:1080` в `client.yaml` и в GUI). CLI `loft` / `loft-front` без явного порта слушают `127.0.0.1:18080`, чтобы сидеть рядом с открытым клиентом. TUN — если `tun: auto`. `-if en0` (или локальный/шлюзовой IPv4) сажает сокеты фронта на этот путь; тот же флаг у `loft-front`, `loft-probe`, `loft-bench`.

Смена пути Wi-Fi / LTE / VPN: SOCKS и TUN остаются, IP оверлея остаётся, процесс-помощник остаётся. TCP фронта перенабирается с билетом возобновления. При возможности `hold` mux-потоки живут `gap_hold_ms` (по умолчанию 8 с), внутренний TCP не рвётся. Leave сессию всё равно закрывает.

В `client.yaml` `pause: starlink` — не слать mux Data в известные окна хэндовера LEO раз в 15 с. Управление всё равно идёт. Это локально, не поле протокола.

### Проверка исходящего канала оверлея

`loft-probe` проверяет ICMP, TCP, UDP, DNS, HTTP и HTTPS через loft без переписывания системных маршрутов и без открытия utun. Нужен, когда ping жив, а сайты нет, или когда маршрут по умолчанию выключен.

```bash
make probe
./dist/clients/go/loft-probe                                          # icmp + tcp + dns + http/https
./dist/clients/go/loft-probe 8.8.8.8                                  # ICMP echo через оверлей
./dist/clients/go/loft-probe -proto tcp 1.1.1.1:443
./dist/clients/go/loft-probe -proto http https://ifconfig.me/ip       # публичный IP, как его видит нода
./dist/clients/go/loft-probe -via socks -proto tcp example.com:443    # живой SOCKS loft, без лишней сессии
./dist/clients/go/loft-probe -c "$HOME/Library/Application Support/loft/profiles/example.yaml" 8.8.8.8
./dist/clients/go/loft-probe -if en0 -c "$HOME/Library/Application Support/loft/profiles/example.yaml" 8.8.8.8
```

`-via mux` открывает свою GET/WS-сессию (покрывает ICMP). `-via socks` берёт живой SOCKS5 (GUI `127.0.0.1:1080`, отладочный CLI `127.0.0.1:18080`; TCP/UDP/HTTP; ICMP пропускается). `-via auto` предпочитает профиль, затем живой SOCKS.

| Поле | Смысл |
|------|--------|
| `token` | Ключ участника |
| `instance_id` | Экземпляр оверлея. Пусто: `{config}.instance` рядом с YAML, создаётся при первом запуске |
| `select` | `auto` или имя исходящего |
| `fronts` | Фронты, от которых этот клиент отказывается |
| `bootstrap` | HTTPS URL манифеста roster, до аутентификации |
| `controller_pub` | 32 байта Ed25519 (64 hex). Пусто = доверие при первом использовании |
| `outbounds` | `type`, `url`, `path`, `cdn`, `fingerprint` |
| `use` | Имя списка → `direct` \| `via` \| `drop` |
| `default` | Действие, если ни один список не совпал (по умолчанию `via`) |
| `p2p` | `false` выключает прямые попытки оверлея |
| `lists_url` | Переопределить GET списков адресов |
| `listen.socks5` | Адрес прослушивания |
| `listen.tun` | Пусто = выкл; `auto` создаёт utun |
| `listen.tun_addr` | Префикс TUN; адрес — IP оверлея из Status |
| `listen.kill_switch` | Держать системный маршрут по умолчанию, когда TUN умирает |

Поля исходящего: `name`, `type` (`get` \| `ws` \| `h2` \| `quic` \| `h3`), `url`, `path`, `cdn` (`yandex` \| `cloudflare` \| пусто), `fingerprint` (`chrome` \| `firefox` \| `go`), `header_budget`, `inflight`, `insecure_skip_verify`.

Пропущенный `header_budget` — 16 КиБ HTTP-заголовка (профиль Yandex CDN фиксирует 12 КиБ). Пропущенный `inflight` в этом дереве — 8 (диапазон протокола 2–8, в спецификации по умолчанию 4). Дальнейший исходящий GET — заголовок `X-Request-Id`; первый GET — Cookie `session=` плюс `X-Auth: 1`. ICMP оверлея — CmdDgram на `dst:1`; нода отвечает шлюзу и отправляет настоящий ICMP echo в интернет.

Клиент decoy SNI не выбирает. Его объявляет нода. CDN-фронт сохраняет hostname CDN.

Yandex Cloud CDN (группа источников, ресурс, кэш выкл, GET 12 КиБ): [yandex-cdn_ru.md](yandex-cdn_ru.md).

`bootstrap` **должен** быть хостом с настоящим сертификатом или CDN. GET на decoy IP без HMAC сессии стыкуется с origin.

Roster: `GET /static/v3/manifest.json` → `{"v":1,"p":"<base64url>"}`. Ключа участника в файле нет. После рукопожатия Status обновляет roster. При отказе клиент пробует фронты, неподписанные `replicas`, затем roster.

### Списки адресов

Контроллер публикует именованные множества. Клиент сопоставляет имена с действиями:

```yaml
# server.yaml
lists:
  banks: [example-bank.test, .bank.example]
  video: [youtube.com]
  ads: [ads.example]

# client.yaml
default: via
use:
  banks: direct
  video: via
  ads: drop
```

Если хост попал в несколько сопоставленных списков: drop, затем direct, затем via. SOCKS сопоставляет hostname. TUN сопоставляет IP (CIDR). Запрос: `GET /static/v3/lists.json`. Status несёт serial и хеш.

Имена списков — словарь оператора. Протокол считает их непрозрачными строками.

---

## 7. Decoy

Неавторизованный TCP (любой SNI, битый hello, plaintext) стыкуется с `decoy:`. UDP на том же порту стыкуется с тем же origin. ClientHello loft (действительный `session_id`) обрабатывает нода. При `mask: true` этот путь показывает лист ECDSA формы dest; зонды по-прежнему видят origin. `cdn_peers` перечисляет CIDR, которым разрешено тянуть origin на own SNI без sid.

`session_id` — 32 байта. sid v3 — HKDF под ECDH с X25519 ноды (`mask_pub` / share-URI `pbk`). sid v2 (запасной) — HKDF под ключом участника. Украденный ключ участника маркирует только hello v2.

Имена origin в примерном YAML — примеры. Берите origin, чей публичный сайт путь не готов снять, и чей TLS нода умеет стыковать. В протокол они не компилируются.

---

## 8. Вне протокола

Виртуальные машины, DNS, публичные сертификаты и обновления двоичных файлов лежат вне протокола. По каналу идут enrol, beat, slice, drain, roster и списки.

Типичное развёртывание:

1. Выпустить сертификат на публичное имя (или терминировать TLS на CDN).
2. Написать `network.conf` и запустить контроллер с `-dir`.
3. Опубликовать `controller_pub` и URL `bootstrap` на хосте, который не является IP со стыковкой decoy.
4. Запустить relay с `id`, совпадающим с `relays:`.
5. Выдать участникам ключ и `client.yaml` (или файл `.loft`).

---

## 9. Отказы

| Симптом | Что проверить |
|---------|----------------|
| `token is required` | `LOFT_TOKEN` или `token:` |
| `network disabled` | `enable` в `network.conf` |
| TLS висит | decoy SNI, mask, сертификат, `session_id` |
| Манифест — чужой HTML | bootstrap попал на decoy IP без HMAC |
| Relay `enrol_no id` | id нет в `relays:` при `enrol: list` |
| QUIC падает при включённом `h3` | UDP ALPN `h3` занят CONNECT; переход на `h3` |
| `auth_already_online` | другой экземпляр держит ключ; включите `multi` или остановите другой |
| `auth_limit` | `multi_limit` |
| `policy_drop` | overlay Open при `peers: hub` или drop ACL |
| SOCKS жив, TUN молчит | `tun: auto`, привилегии, исключённые исходящие хосты |
| Системный маршрут по умолчанию пропал после смерти TUN | `kill_switch`; вернуть маршруты или переподключиться |
| Ping ~1 с на GET | темп простоя GET — `keepalive/2` (не меньше 1 с); переподключение, чтобы клиент догонял после Dgram |
| Исходящий канал стопорится / TLS bad record | пропущенный GET `inflight` раньше по умолчанию был 1; в этом дереве теперь 8 |

---

## 10. Debian

Нода на хосте Debian: [server/debian/README_ru.md](../server/debian/README_ru.md).

```bash
make dist-node
# скопировать dist/node/linux-amd64/loft-node-<version>-linux-amd64.tar.gz на хост
tar -xzf loft-node-*-linux-amd64.tar.gz
cd loft-node-*-linux-amd64
sudo ./install.sh --unattended
```

Relay:

```bash
sudo ./install.sh --role=relay --id=ru1 \
  --controller=wss://controller.example/api/v1/sync \
  --advertise=https://203.0.113.10
```

Пакет Debian: `make dist-node`, затем `dpkg -i dist/node/linux-amd64/loft-node_*_amd64.deb`.

Установщик создаёт пользователя `loft`, `/etc/loft/loft.env` (токен), самоподписанный сертификат, unit systemd и `/root/loft-client.yaml`. Токен остаётся в файле окружения. `loft-node -print-pub` печатает `controller_pub`.

---

## 11. Тесты

```bash
make test
go test ./internal/wire ./internal/crypto ./internal/mux ./internal/netconf ./internal/transport
```

Тесты протокола покрывают кадры, Hello/Status, подмену roster, списки, флот. Тесты транспорта — decoy `session_id`, запрос roster, политику оверлея. Mask на TCP loopback **может** пропускаться на части хостов. `make test` ещё запускает `server/debian/install_os_test.sh`.

---

## 12. GitHub Actions

Push и pull request в `main` запускают `make test`.

Тег `vX.Y.Z` должен совпасть с корневым `VERSION`. Этот тег собирает:

- tarball и `.deb` ноды (linux amd64, arm64)
- архивы CLI (`loft`, `loft-front`, `loft-probe`, `loft-bench` для linux/darwin, amd64/arm64)
- macOS DMG, ad-hoc подпись: `loft-*-macos-arm64.dmg` и `loft-*-macos-x86_64.dmg` (`BUMP=0`, CI не переписывает `clients/macOS/VERSION`). Раннер — Apple Silicon; Intel — кросс-сборка Swift/Go (`ARCH=x86_64`).

и публикует GitHub Release. Те же файлы ещё кладутся без версии в имени (`loft-macos-arm64.dmg`, `loft-linux-amd64.tar.gz`, …), чтобы [ссылки в README](../README_ru.md#скачать) на `/releases/latest/download/…` шли в текущий тег. Повторный прогон на том же теге заменяет файлы. Без тега Actions → Dist → Run workflow только кладёт артефакты.

Локальные пакеты: `make dist`. Локальный DMG без повышения маркетинговой версии macOS: `make macos-dmg BUMP=0`. Developer ID и нотаризация остаются на Маке со связкой; в Actions их нет.

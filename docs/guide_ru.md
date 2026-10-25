# Запуск loft

[English](guide.md) · **Русский** · [中文](guide_zh.md)

ПО 1.0.28. Протокол 1.0 (loft/1.0). Протокол: [protocol_ru.md](protocol_ru.md). Устройство: [architecture_ru.md](architecture_ru.md).

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
make android   # dist/clients/Android/loft.app + loft-tv.app + loft-gtv.app + APKs
make ios       # dist/clients/iOS/loft.app
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

**Контроллер** хранит ключи, аренды, ACL и ключ подписи roster и списков. Он **может** слушать 443 для участников.

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

**Перечитывание.** `systemctl reload loft-node` (SIGHUP) заново читает `network.conf` и `members.keys`, не останавливая ноду. Члены, чья запись не изменилась, остаются подключены; удалённый, выключенный или изменённый член отключается и переподключается с новыми настройками. Изменение того, что клиент узнаёт при Hello (CIDR, шлюз, MTU, peers, p2p, DNS, маршруты, VLAN, таймеры сессии; правила при включённом p2p), переподключает всех — тоже без перезапуска. Файл, который не загрузился, пишется в журнал, а работающая конфигурация остаётся. `node.conf`, сертификат и токен по-прежнему требуют перезапуска.

**Каналы выхода.** По умолчанию члены выходят по маршруту самого сервера. `egress.conf` рядом с `network.conf` (его пишет loft-panel) отправляет их иначе:

```
channel wg-nl dev wg-nl check ping probe 1.1.1.1 every 10 fails 3   # интерфейс ядра (WireGuard, OpenVPN, ZeroTier)
channel wg-fi dev wg-fi
channel px socks5 192.0.2.40:1080 user u pass "p w" udp proxy check tcp probe example.com:443
channel web http 192.0.2.41:3128 tls      # HTTP- или HTTPS-прокси (CONNECT)
channel office sstp vpn.example.org:443 user office pass x auth mschapv2
prio main wg-nl wg-fi px then direct limit wg-fi 50/10   # список приоритетов: первый работающий канал, затем direct или block; wg-fi ограничен 50/10 Мбит/с
prio both wg-nl wg-fi then direct multi  # многопутевой: новые соединения по очереди идут в работающие каналы
prio work office then block
rule member bob via work
rule tag staff via main
rule list blocked via main                # список из lists в node.conf
default via main
```

Правила проверяются по порядку, затем `default`; каждое называет список приоритетов (или `direct`, `block`). Список берёт первый канал, который может нести трафик, затем следующий, затем шаг `then`. Канал пропускается, пока он `off`, без интерфейса, не смог подключиться за последние 20 с или не проходит проверку связи: `check ping` (интерфейсы ядра), `check tcp`, `check tls` (рукопожатие после подключения) или `check auto` (HTTPS на ya.ru, nic.ru, google.com; достаточно одного) шлёт пробу каждые `every` секунд (10) и считает связь потерянной после `fails` неудач подряд (3); одна удача возвращает канал. `direct check KIND …` — та же проба маршрута хоста по умолчанию; пока она не проходит, списки приоритетов пропускают `direct`. Правило может по-прежнему назвать канал — тогда порядок задаёт его цепочка `fail` (канал, `direct`, `block`), как в старых файлах. `multi` делает список многопутевым: каждое новое TCP-соединение начинает с канала дальше по кругу (UDP члена к одному хосту держит один путь). `limit КАНАЛ ВХОД/ИСХОД` ограничивает канал для всех, кто выходит по списку, в Мбит/с (ВХОД — к членам, 0 — без ограничения). Прокси может идти `via` другой канал. `dev` привязывает сокет к интерфейсу (SO_BINDTODEVICE); системе нужно правило маршрутизации (`ip rule add oif DEV lookup TABLE` и маршрут по умолчанию в TABLE) — его ставит loft-panel. По правилам идут TCP-потоки и датаграммы членов; ICMP и DNS-запросы к ноде выходят напрямую. Нода перечитывает файл по SIGHUP и каждые 5 с пишет `egress-status.json` (состояние, байты, соединения).

`LOFT_TOKEN` — секрет ноды (mask, seed контроллера). Клиенты входят ключами из `members.keys`. `auth shared enable` (`auth_shared: true` в YAML) принимает `LOFT_TOKEN` как секрет клиента, пока кольцо пустое — старый оверлей на одном токене. Непустое кольцо токен ноды не подмешивает, даже с флагом. Новая установка пишет `auth shared disable` и выдаёт ключ `operator`.

YAML клиента несёт `member_id` и `token` этого участника. Hello `member_id` берётся из совпавшего ключа, не из заявления клиента.

| `peers` | Overlay Open к другому участнику |
|---------|----------------------------------|
| `hub` | Reset `policy_drop`. Open в интернет не меняется. CIDR за участником всё равно стыкуется. |
| `mesh` | Прямой TCP, если `p2p` позволяет, иначе стыковка через ноду. |
| `vlan` | Как mesh, только та же VLAN. |

Необязательные команды: `create vlan`, `ip route DST VIA|member ID|tag NAME` (следующий хоп — overlay IP, id участника или первый живой/закреплённый с этим тегом), `ip access-list` (расширенный ACL Cisco, первое совпадение, `permit`/`deny`, `tag NAME` / `group NAME`, `default deny`), `config member ID tags …`, таймеры `session` (`handshake_ms`, `idle_ms`, `keepalive_ms`, `lease_hold_ms`, `gap_hold_ms`, `ttl_ms`) и `session rate_bps`. `ttl_ms` — максимум живой сессии (0 = выкл). `lease_hold_ms` — удержание IP после обрыва. `rate_bps` — потолок балка Data/Dgram сессии участника, оба направления (0 = выкл). Override: `config member ID rate_bps N`.

```
config member lab ipaddress 10.8.0.5
config member lab tags lab
config member alice tags ops
ip route 192.168.10.0/24 member lab
ip route 10.20.0.0/16 tag lab
ip access-list overlay
  default deny
  permit tcp tag ops tag db eq 5432
  permit ip tag ops 192.168.10.0/24
session ttl_ms 86400000
```

`default deny` оставляет шлюз оверлея (DNS). Mesh без этой строки остаётся открытым между участниками. Status пушит CIDR всем, кроме участника, чей overlay IP равен VIA. TCP Open на `192.168.10.5:22` стыкуется как `192.168.10.5:22` на lab; Open на overlay IP по-прежнему `127.0.0.1:port`. Прямой P2P берёт тот же ACL (policy в Status). Старый Status без policy эту проверку пропускает.

Клиент **может** поставить `p2p: false` и тогда пользуется только стыковкой через хаб. Прямой TCP: клиент слушает, объявляет `loft.peer:<port>` и 32-байтный X25519 pub, Status несёт хост, который нода увидела на TLS. Предисловие — LFT (ECDH пары). За NAT попытка не удаётся, и тот же поток идёт через ноду. Через relay увиденный хост — сам relay. Клиент переподключается примерно на 80% `session_ttl_ms`.

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
| `fronts` | Пусто = `get`. `ws`, `h2`, `quic`, `h3` — явно. `connect` — стыковка HTTP CONNECT без mux; выключена, пока не указана. `l3` — HTTP/2 CONNECT, затем IP-дейтаграммы; выключен, пока не указан |
| `inner` | Дополнительные шифры данных, которые нода может подтвердить: `clear`, `aes`. Пусто = только ChaCha20-Poly1305. ChaCha20-Poly1305 всегда разрешён, выключить его нельзя. `get` не подтверждает `clear` и `aes`. Применяется после рестарта. SIGHUP `node.conf` не перечитывает. |
| `edge` | Пресет GET: `plain` или `tight` (байт Status 0 или 1). `tight`: бюджет Cookie 16 КиБ, inflight 16, таймаут 6 с. Для origin за CDN или обратным прокси, который сбрасывает толстый одновременный всплеск HEADERS. По умолчанию `plain`. |
| `decoy` | список `host:port`. Первая запись — личность ноды. Пусто = выкл |
| `decoy_proxy` | `enable`: каждая TCP-стыковка начинается строкой PROXY v1 с адресом клиента. Только для своего веб-сервера (nginx `listen … proxy_protocol`); чужой сайт сломается. По умолчанию выкл. UDP не затрагивает |
| `mask` | Рукопожатие ECDSA формы dest для клиентов loft; зонды по-прежнему стыкуются |
| `role` | `controller` |
| `advertise` | Публичный URL этого фронта в roster |
| `roster_path` | Публичный GET, по умолчанию `/static/v3/manifest.json` |
| `enrol` | `list`: id должен быть в `relays:`. `open`: любой транк `CapRelay` |
| `auth shared` | `enable`: `LOFT_TOKEN` как секрет клиента при пустом `members.keys`. По умолчанию `disable`. |
| `relays` | Список допущенных: id, url, decoy, fronts, weight, path. Заданные здесь поля перекрывают url, decoy, fronts и weight этого relay |
| `lists` | Имя → адреса. Ведущая `.` или `*.` — суффикс; строка CIDR — kind 3; прочая строка — точный домен |
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

Локальные слушатели: SOCKS5 (`127.0.0.1:1080` в `client.yaml` и в GUI). CLI `loft` / `loft-front` без явного порта слушают `127.0.0.1:18080`, чтобы сидеть рядом с открытым клиентом. TUN — если `tun: auto`. `-if en0` (или локальный/шлюзовой IPv4) сажает сокеты фронта на этот путь; тот же флаг у `loft-front`, `loft-probe`, `loft-bench`. У `loft-front` / `loft-bench` `-h2-max-age` — то же, что YAML `h2_max_age_ms` (0 — держать, пока ребро само не оборвёт).

Смена пути Wi-Fi / LTE / VPN: SOCKS и TUN остаются, IP оверлея остаётся, процесс-помощник остаётся. GET меняет HTTP/2 с нового исходящего; sid и mux остаются. Остальные фронты — Park; продолжение — свежий `0x02` и тот же `instance_id`. При возможности `hold` припаркованные mux-потоки живут `gap_hold_ms` (по умолчанию 8 с), внутренний TCP не рвётся. Leave сессию всё равно закрывает.

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
| `fronts` | Дополнительный фильтр. Пусто = типы исходящих (сток — GET) |
| `inner` | Запрос шифра данных: `clear` или `aes`. Пусто или `chacha` — байт в Hello не пишется (ChaCha20-Poly1305). Нода подтверждает этот идентификатор или сессия остаётся на ChaCha20-Poly1305. `get` остаётся на ChaCha20-Poly1305. `loft-front -inner` — то же поле. |
| `bootstrap` | HTTPS URL манифеста roster, до аутентификации |
| `controller_pub` | 32 байта Ed25519 (64 hex). Нужен, чтобы принять roster |
| `tofu` | `true` закрепляет первого действительного подписанта, если `controller_pub` пуст (по умолчанию выкл) |
| `outbounds` | `type`, `url`, `path`, `cdn`, `fingerprint` |
| `use` | Имя списка → `direct` \| `via` \| `drop` |
| `default` | Действие, если ни один список не совпал (по умолчанию `via`) |
| `p2p` | `false` выключает прямые попытки оверлея. Прямой TCP — LFT (ECDH пары) |
| `lists_url` | Переопределить GET списков адресов |
| `listen.socks5` | Адрес прослушивания |
| `listen.tun` | Пусто = выкл; `auto` создаёт utun |
| `listen.tun_addr` | Префикс TUN; адрес — IP оверлея из Status |
| `listen.kill_switch` | Держать системный маршрут по умолчанию, когда TUN умирает |
| `listen.http` | LAN-страница оператора (MikroTik `:8090`) |
| `listen.http_pass` | Пароль этой страницы, по желанию |
| `listen.bind` | То же, что `-if`; флаг CLI главнее |
| `listen.uplink` | Имя WAN RouterOS для выхода контейнера (`ether1`, `lte1`) |

Поля исходящего: `name`, `type` (`get` \| `ws` \| `h2` \| `quic` \| `h3`), `url`, `path`, `cdn` (`yandex` \| `cloudflare` \| пусто), `fingerprint` (`chrome` \| `firefox` \| `go`), `header_budget`, `inflight`, `h2_max_age_ms`, `insecure_skip_verify`.

`fingerprint` `chrome` — ClientHello Chrome, который этот клиент собирает через uTLS. `firefox` — другая форма браузера. `go` — список шифров Go; фильтры JA3/JA4 уже отмечают его как не-браузер. Протокол на пути с такими фильтрами требует форму браузера. uTLS он не требует.

Пропущенный `header_budget` — 16 КиБ HTTP-заголовка (профиль Yandex CDN фиксирует 12 КиБ). Пропущенный `inflight` в этом дереве — 8 (диапазон протокола 2–8, в спецификации по умолчанию 4). Пропущенный `h2_max_age_ms` — 0: HTTP/2 GET живёт, пока ребро само не оборвёт. Положительное значение (пол 15 с, потолок 1 ч) меняет этот TCP; mux и идентификатор GET-сессии остаются. Исходящий GET — Cookie `session=`; первый GET — тег `0x02`, идентификатор сессии уже в пути. ICMP оверлея — CmdDgram на `dst:1`; нода отвечает шлюзу и отправляет настоящий ICMP echo в интернет.

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

Неавторизованный TCP (любой SNI, битый hello, plaintext) стыкуется с `decoy:`. UDP на том же порту стыкуется с тем же origin, кроме QUIC loft, чей Initial несёт метку loft: фронты quic и h3 работают и за decoy (клиенты начиная с этого выпуска). ClientHello loft (действительный `session_id`) обрабатывает нода. При `mask: true` этот путь показывает лист ECDSA формы dest; зонды по-прежнему видят origin. `cdn_peers` перечисляет CIDR, которым разрешено тянуть origin на own SNI без sid. Когда origin — nginx этого же сервера, `decoy_proxy enable` даёт ему настоящий адрес клиента (журналы, ограничения, защита входа в панель).

`session_id` — 32 байта. sid v3 — HKDF под ECDH с X25519 ноды (`mask_pub` / share-URI `pbk`). sid v2 (запасной) — HKDF под ключом участника. Украденный ключ участника маркирует только hello v2.

При включённом mask hello на SNI decoy принимается только как sid v3 (`mask_pub` / `pbk`); sid v2 стыкуется с origin. Если ClientHello предлагает X25519MLKEM768 (группа `0x11EC`), mask выбирает эту группу ([RFC 10024](https://www.rfc-editor.org/rfc/rfc10024), Post-Quantum Traditional (PQ/T) Hybrid Key Agreement Mechanisms for TLS 1.3). Лист по-прежнему не цепочка публичного УЦ. Повтор того же `client_random` в течение 10 минут стыкуется как зонд. На ноде с mask Initial QUIC принимается только с меткой v3.

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

и публикует GitHub Release. Dist ещё забирает тег в [proxy.golang.org](https://proxy.golang.org) и просит [pkg.go.dev](https://pkg.go.dev/github.com/a3app/loft) собрать страницу модуля под этот релиз. В имени файла — версия (`loft-1.0.28-macos-arm64.dmg`, `loft-node_1.0.28_amd64.deb`). Повторный прогон на том же теге заменяет файлы. Без тега Actions → Dist → Run workflow только кладёт артефакты.

Локальные пакеты: `make dist`. Локальный DMG без повышения маркетинговой версии macOS: `make macos-dmg BUMP=0`. Developer ID и нотаризация остаются на Маке со связкой; в Actions их нет.

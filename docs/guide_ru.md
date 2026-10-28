# Запуск loft

[English](guide.md) · **Русский** · [中文](guide_zh.md)

ПО 1.0.38. Протокол 1.0 (loft/1.0). Протокол: [protocol_ru.md](protocol_ru.md). Устройство: [architecture_ru.md](architecture_ru.md).

Здесь описана эталонная реализация.

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
make ios       # dist/clients/iOS/loft.app (macOS) + iphonesimulator/loft.app
make windows   # dist/clients/Windows/x64 и x86
make dist      # tarball/.deb ноды + архивы CLI
```

```bash
go test ./...
go build -ldflags "-s -w -X github.com/a3app/loft/internal/version.Version=$(cat VERSION)" -o loft-node ./server/cmd/loft-node
go build -ldflags "-s -w -X github.com/a3app/loft/internal/version.Version=$(cat VERSION)" -o dist/clients/go/loft ./clients/go/cmd/loft
go build -ldflags "-s -w -X github.com/a3app/loft/internal/version.Version=$(cat VERSION)" -o dist/clients/go/loft-probe ./clients/go/cmd/loft-probe
```

`--version` печатает имя программы, semver и диапазон версий протокола.

`make client` собирает `dist/clients/go/loft`, `make probe` — `loft-probe`. Приложению macOS нужен ещё `loft-front`:

```bash
go build -ldflags "-s -w -X github.com/a3app/loft/internal/version.Version=$(cat VERSION)" -o dist/clients/go/loft-front ./clients/go/cmd/loft-front
```

---

## 2. Роли ноды

**Контроллер** хранит ключи, аренды, ACL и ключ, которым подписываются roster и списки. Он **может** принимать участников на порту 443.

**Relay** завершает публичный TLS и передаёт расшифрованный трафик на контроллер по транку. IP relay расходный. Личность участника и его IP в оверлее остаются на контроллере.

Обе роли выполняет один и тот же двоичный файл. Роль задаётся в YAML: `role: controller` или `role: relay`.

---

## 3. Сеть оверлея

Путь: `{dir}/network.conf` (старый `{dir}/network.yaml` по-прежнему загружается). Без этого файла нода не стартует.

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

`dns.servers` передаётся в Status и прописывается на TUN клиента. На запросы к шлюзу на порт 53 (UDP CmdDgram и TCP Open) отвечает сама нода. `dns.upstream` задаёт, откуда нода берёт ответы: `https://` — DNS-over-HTTPS, `tls://` — DNS-over-TLS (порт 853), просто IP — UDP/53. `quic://` зарезервирован. Пустой `servers` означает адрес шлюза. При `enabled: false` системный DNS не трогается.

Ключ участника — это `lt_` и 48 шестнадцатеричных символов (24 байта). В `network.conf` хранится SHA-256 ключа. Сами ключи в открытом виде лежат в `{dir}/members.keys` (режим 0600): по хешу HMAC и HKDF не посчитать. Оператор может перечитать этот файл и снова выдать YAML клиента. В старых конфигах может остаться строка `keep_keys`; нода её игнорирует.

**Перечитывание.** `systemctl reload loft-node` (SIGHUP) заново читает `network.conf` и `members.keys`, не останавливая ноду. Участники, чья запись не изменилась, остаются подключены; удалённый, выключенный или изменённый участник отключается и переподключается с новыми настройками. Если меняется то, что клиент узнаёт при Hello (CIDR, шлюз, MTU, peers, p2p, DNS, маршруты, VLAN, таймеры сессии; правила при включённом p2p), переподключаются все — тоже без перезапуска. Если файл не загрузился, ошибка пишется в журнал, а нода продолжает работать на прежней конфигурации. Для `node.conf`, сертификата и токена по-прежнему нужен перезапуск.

**Каналы выхода.** По умолчанию трафик участников выходит по маршруту самого сервера. `egress.conf` рядом с `network.conf` (его пишет loft-panel) направляет его иначе:

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

Правила проверяются по порядку, затем `default`; каждое указывает список приоритетов (или `direct`, `block`). Список берёт первый канал, способный нести трафик, затем следующий, затем шаг `then`. Канал пропускается, пока он `off`, у него нет интерфейса, за последние 20 с к нему не удалось подключиться или он не проходит проверку связи. Проверка `check ping` (интерфейсы ядра), `check tcp`, `check tls` (рукопожатие после подключения) или `check auto` (HTTPS на ya.ru, nic.ru, google.com; достаточно одного) отправляет пробу каждые `every` секунд (10) и считает связь потерянной после `fails` неудач подряд (3); одной удачной пробы достаточно, чтобы канал вернулся. `direct check KIND …` — такая же проба маршрута по умолчанию самого хоста; пока она не проходит, списки приоритетов пропускают `direct`. Правило по-прежнему может указать канал напрямую — тогда порядок задаёт цепочка `fail` этого канала (канал, `direct`, `block`), как в старых файлах. `multi` делает список многопутевым: каждое новое TCP-соединение начинается со следующего по кругу канала (UDP участника к одному хосту идёт одним путём). `limit КАНАЛ ВХОД/ИСХОД` ограничивает канал в Мбит/с для всех, кто выходит по этому списку (ВХОД — в сторону участников, 0 — без ограничения). Прокси можно пустить `via` через другой канал. `dev` привязывает сокет к интерфейсу (SO_BINDTODEVICE); для этого системе нужно правило маршрутизации (`ip rule add oif DEV lookup TABLE` и маршрут по умолчанию в TABLE) — его настраивает loft-panel. По правилам идут TCP-потоки и датаграммы участников; ICMP и DNS-запросы к ноде выходят напрямую. Нода перечитывает файл по SIGHUP и каждые 5 с пишет `egress-status.json` (состояние, байты, соединения).

`LOFT_TOKEN` — секрет ноды (mask). Клиенты входят по ключам из `members.keys`. `auth shared enable` (`auth_shared: true` в YAML) принимает `LOFT_TOKEN` как секрет клиента, пока кольцо ключей пусто — так работал старый оверлей на одном токене. Если кольцо не пусто, токен ноды к нему не добавляется даже с этим флагом. Новая установка пишет `auth shared disable` и выдаёт ключ `operator`.

YAML клиента содержит `member_id` и `token` этого участника. `member_id` в Hello берётся из совпавшего ключа, а не из того, что заявил клиент.

| `peers` | Overlay Open к другому участнику |
|---------|----------------------------------|
| `hub` | Reset `policy_drop`. Open в интернет не меняется. CIDR за участником всё равно стыкуется. |
| `mesh` | Прямой TCP, если позволяет `p2p`, иначе стыковка через ноду. |
| `vlan` | Как mesh, но только внутри одной VLAN. |

Необязательные команды: `create vlan`, `ip route DST VIA|member ID|tag NAME` (следующий хоп — overlay IP, id участника или первый живой/закреплённый участник с этим тегом), `ip access-list` (расширенный ACL в стиле Cisco, первое совпадение, `permit`/`deny`, `tag NAME` / `group NAME`, `default deny`), `config member ID tags …`, таймеры `session` (`handshake_ms`, `idle_ms`, `keepalive_ms`, `lease_hold_ms`, `gap_hold_ms`, `ttl_ms`) и `session rate_bps`. `ttl_ms` — максимальная длительность живой сессии (0 = выкл). `lease_hold_ms` — сколько IP удерживается после обрыва. `rate_bps` — потолок объёмного трафика Data/Dgram для сессии участника в обе стороны (0 = выкл). Переопределение для участника: `config member ID rate_bps N`.

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

`default deny` по-прежнему пропускает к шлюзу оверлея (DNS). Без этой строки mesh между участниками остаётся открытым. Status рассылает CIDR всем участникам, кроме того, чей overlay IP равен VIA (у этого хоста LAN и так есть). TCP Open на `192.168.10.5:22` стыкуется с `192.168.10.5:22` на участнике lab; Open на overlay IP по-прежнему стыкуется с `127.0.0.1:port`. Прямой P2P использует тот же ACL (policy в Status). Старый Status без policy эту проверку пропускает.

Клиент **может** выставить `p2p: false` и тогда пользуется только стыковкой через хаб. Прямой TCP устроен так: клиент слушает порт, объявляет `loft.peer:<port>` и 32-байтный открытый ключ X25519, а Status передаёт хост, который нода увидела в TLS-соединении. Соединение зашифровано с первого байта (ECDH пары и свежие ключи) и не содержит фиксированных байт. За NAT попытка не удаётся, и тот же поток идёт через ноду. При работе через relay увиденный хост — сам relay. Клиент заново выполняет рукопожатие примерно на 80% `session_ttl_ms`.

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
| `-cert` `-key` | PEM, если их нет в YAML |
| `--version` | Показать версии |

| Поле | Смысл |
|------|--------|
| `listen` | `:443` |
| `cert` `key` | Сертификат ноды. При decoy+mask клиент loft видит лист в форме dest. |
| `token` | Секрет ноды (`LOFT_TOKEN`): mask. Ключи участников лежат в `members.keys`. |
| `get_path` `ws_path` `h2_path` `h3_path` | Пути фронтов |
| `get_h2` | Число HTTP/2-соединений GET, 1–4 (по умолчанию 1). Inflight у них общий. |
| `fronts` | Пусто = `get`. `ws`, `h2`, `quic`, `h3` включаются явно. `connect` — стыковка HTTP CONNECT без mux; выключена, пока не указана. `l3` — HTTP/2 CONNECT, затем IP-датаграммы; выключен, пока не указан |
| `inner` | Дополнительные шифры данных, которые нода может подтвердить: `clear`, `aes`. Пусто = только ChaCha20-Poly1305. ChaCha20-Poly1305 разрешён всегда, выключить его нельзя. `get` не подтверждает ни `clear`, ни `aes`. Вступает в силу после перезапуска. SIGHUP `node.conf` не перечитывает. |
| `cc` | Управление перегрузкой, которое клиентам можно просить: `bbr`, `fixed` (`off` или пусто — все сессии на системном умолчании). Просить ли — решает каждый клиент; нода только разрешает. Переключаются только сессии поверх QUIC (`quic`, `h3` или `l3` поверх HTTP/3); TCP-фронты остаются на умолчании ядра. Не через relay и не на `get`. Вступает в силу после перезапуска. |
| `cc_max UP DOWN` | Потолки для `fixed` в Мбит/с: как быстро клиенту можно отправлять и как быстро нода отправляет ему. `0` — без потолка. |
| `edge` | Пресет GET: `plain` или `tight` (байт Status 0 или 1). `tight`: бюджет Cookie 16 КиБ, inflight 16, таймаут 6 с. Для origin за CDN или обратным прокси, который отбрасывает крупный одновременный всплеск HEADERS. По умолчанию `plain`. |
| `decoy` | Список `host:port`. Первая запись задаёт личность ноды. Пусто = выкл |
| `decoy_proxy` | `enable`: каждая TCP-стыковка начинается строкой PROXY v1 с адресом клиента. Только для своего веб-сервера (nginx `listen … proxy_protocol`); чужой сайт от этого сломается. По умолчанию выкл. UDP не затрагивается |
| `mask` | Рукопожатие ECDSA в форме dest для клиентов loft; зонды по-прежнему стыкуются с origin |
| `role` | `controller` |
| `advertise` | Публичный URL этого фронта для roster |
| `roster_path` | Публичный GET, по умолчанию `/static/v3/manifest.json` |
| `auth shared` | `enable`: `LOFT_TOKEN` служит секретом клиента, пока `members.keys` пуст. По умолчанию `disable`. |
| `relays` | Relay, которым разрешён enrol: id, url, decoy, fronts, weight, path, `key_sha256`. Поля, заданные здесь, перекрывают url, decoy, fronts и weight этого relay. Сами ключи relay лежат в `{dir}/relays.keys` (id → ключ, режим 0600) |
| `lists` | Имя → адреса. Ведущая `.` или `*.` означает суффикс; строка CIDR — kind 3; любая другая строка — точный домен |
| `lists_path` | Публичный GET, по умолчанию `/static/v3/lists.json` |
| `canary` | Канарейки пути: `fat_hello`, `split_hello`, `pad` |
| `cover_dir` | Корень статики. Пусто = встроенная страница |
| `replicas` | Неподписанные дополнительные `host:port` для запасного пути |
| `quic_alpn` | По умолчанию `h3` |

Seed подписи хранится в `{dir}/controller.seed`. Он случайный, создаётся при первом запуске (или командой `-print-pub`) и из токена не выводится. Открытый ключ раздайте клиентам как `controller_pub`. Сменить ключ без перевыпуска клиентов: `loft-node -dir {dir} -roll-key 30` (или панель, «Доступ» → «Сменить ключ…»), затем перезапуск; клиенты и relay со старым ключом перейдут на новый сами по подписанной передаче в течение 30 дней (протокол, §10.4).

Живой roster — это `advertise` плюс relay, которые шлют heartbeat и не находятся в drain. Три пропущенных удара (~30 с) или мёртвый поток `ctl` снимают relay и увеличивают serial.

Пути **следует** делать похожими на ресурсы или API обычного сайта. По умолчанию в этом дереве: `/static/v3/build`, `/api/v1/sync`, `/api/v1/tunnel`, `/static/v3/manifest.json`, `/static/v3/lists.json`.

---

## 5. Relay

Пример: `server/configs/relay.yaml`.

```bash
./loft-node -c server/configs/relay.yaml -dir /var/lib/loft-relay
```

| Поле | Смысл |
|------|--------|
| `role` | `relay` |
| `id` | Должен совпадать с `relays[].id` на контроллере и с id, для которого выпущен ключ |
| `controller` | URL хаба: `wss://host/api/v1/sync` или `https://host` |
| `advertise` | URL, по которому клиенты приходят на этот край |
| `decoy` `mask` | Личность этого IP |
| `controller_pub` | Открытый ключ контроллера (64 hex); slice, подписанный другим ключом, отклоняется |
| `{dir}/relay.key` | Ключ, выпущенный контроллером для этого relay (режим 0600). `LOFT_TOKEN` здесь — собственный секрет relay (mask), а не токен контроллера |
| `fronts` | Фронты, которые обслуживает этот край |
| `cert` `key` | Публичный TLS этого IP |

Relay открывает транк с возможностью `relay` в Hello, затем поток `ctl:1` (enrol, beat) и на каждого участника — `hub:1` или `hub:2`.

Сертификаты, DNS и саму машину готовят вне протокола. Через enrol контроллер узнаёт, что край жив.

### Резервный контроллер

Резерв — это relay, который хранит копию состояния контроллера и может его заменить. Отметьте relay в панели как «Резервный controller» (`relay … replica` в `node.conf`) и поставьте его с `--role replica`; команда установки уже содержит адрес контроллера. После `enrol_ok` контроллер отправляет резерву `network.conf`, `members.keys`, `relays.keys`, `controller.seed`, `controller.roll`, `lists.conf`, `node.conf` и токен ноды и повторяет это при каждом изменении; резерв хранит их в `{dir}/standby`, а панель показывает, когда он получил копию последний раз. В команде установки каждого relay после контроллера перечислены резервы (`controller URL адрес-резерва …`), и relay пробует их по порядку.

Чтобы резерв принял работу, когда контроллера нет, на резерве от root:

```bash
loft-node -dir /var/lib/loft -c /etc/loft/node.conf -promote
systemctl restart loft-node
```

Резерв становится контроллером с теми же участниками, ключами relay, ключом контроллера и секретом маскировки, поэтому клиентам и relay новые ссылки не нужны; адрес, сертификат и decoy у него остаются свои. Клиенты находят его по roster, relay — по своему списку контроллеров. Старый контроллер не должен вернуться контроллером рядом с ним: переустановите его резервом нового. Если его всё же запустят, первый relay, который до него достучится (relay раз в две минуты обходит все адреса контроллера), покажет ему запись о замене; он остановится, оставит файл `fenced` в каталоге ноды и не запустится контроллером, пока этот файл не удалят.

**Автоматическое повышение.** Отметьте у резерва «Повышать автоматически» (`auto_promote` в его строке `relay`). Тогда relay становятся свидетелями: каждые 10 с каждый relay отправляет каждому резерву отчёт, подписанный своим ключом relay и с меткой времени, видит ли он контроллер (HTTPS POST внутри TLS на собственный публичный адрес резерва; резервы — это следующие после первого адреса в строке `controller` у relay; соединение держится одно, отчёты идут раз в 10 с ± 2 с, так что отдельный порт не нужен). Резерв принимает работу, если сам не видит контроллер `promote_after` (по умолчанию 300 с) и больше половины relay, но не меньше двух, за последние 30 с сообщают то же самое; поэтому обрыв связи только между резервом и контроллером его не повышает. Он пишет `promoted` в `{dir}` и завершается; systemd перезапускает его контроллером из копии. `loft-node -promote` после этого закрепляет это в `node.conf` и `loft.env`.

---

## 6. Клиент

Двоичный файл `loft`. Пример: `clients/go/configs/client.yaml`.

```bash
./dist/clients/go/loft -c clients/go/configs/client.yaml
ALL_PROXY=socks5://127.0.0.1:1080 curl https://example.com
```

Локальные слушатели: SOCKS5 (`127.0.0.1:1080` в `client.yaml` и в GUI). CLI `loft` / `loft-front` без явного порта слушают `127.0.0.1:18080`, чтобы работать рядом с запущенным клиентом. TUN — если `tun: auto`. `-if en0` (или локальный/шлюзовой IPv4) привязывает сокеты фронта к этому пути; тот же флаг есть у `loft-front`, `loft-probe`, `loft-bench`. У `loft-front` / `loft-bench` флаг `-h2-max-age` соответствует YAML `h2_max_age_ms` (0 — держать соединение до ошибки).

При смене пути (Wi-Fi / LTE / VPN) SOCKS и TUN продолжают работать, IP оверлея и процесс-помощник сохраняются. GET заново поднимает HTTP/2 через новый исходящий канал; sid и mux сохраняются. Остальные фронты уходят в Park; продолжение — свежий `0x02` с тем же `instance_id`. При возможности `hold` припаркованные mux-потоки живут `gap_hold_ms` (по умолчанию 8 с), и внутренний TCP не рвётся. Leave всё равно закрывает сессию.

`pause: starlink` в `client.yaml` отключает отправку mux Data в известные окна хэндовера LEO, которые повторяются каждые 15 с. Управляющие кадры всё равно идут. Это локальная настройка, а не поле протокола.

### Проверка исходящего канала оверлея

`loft-probe` проверяет ICMP, TCP, UDP, DNS, HTTP и HTTPS через loft, не переписывая системные маршруты и не открывая utun. Пригодится, когда ping проходит, а сайты не открываются, или когда маршрут по умолчанию выключен.

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

`-via mux` открывает собственную GET/WS-сессию (покрывает и ICMP). `-via socks` использует живой SOCKS5 (GUI — `127.0.0.1:1080`, отладочный CLI — `127.0.0.1:18080`; TCP/UDP/HTTP; ICMP пропускается). `-via auto` сначала пробует профиль, затем живой SOCKS.

| Поле | Смысл |
|------|--------|
| `token` | Ключ участника |
| `instance_id` | Экземпляр оверлея. Пусто: `{config}.instance` рядом с YAML, создаётся при первом запуске |
| `select` | `auto` или имя исходящего |
| `fronts` | Дополнительный фильтр. Пусто = типы исходящих (по умолчанию GET) |
| `inner` | Запрос шифра данных: `clear` или `aes`. Пусто или `chacha` — байт в Hello не пишется (ChaCha20-Poly1305). Нода подтверждает этот идентификатор, иначе сессия остаётся на ChaCha20-Poly1305. `get` остаётся на ChaCha20-Poly1305. `loft-front -inner` задаёт то же поле. |
| `cc` | Запрос управления перегрузкой: `off` (по умолчанию), `bbr`, `fixed`. Действует только на `quic` и `h3`; TCP-фронты остаются на умолчании. В сети, которая выделяет необычный трафик, оставьте выключенным. Нода отвечает тем, что используют обе стороны; нода, которая это не разрешает, оставляет умолчание. |
| `cc_up_mbps`, `cc_down_mbps` | Для `fixed`: как быстро этот клиент отправляет и как быстро может принимать, Мбит/с. Ставьте реальную скорость линии: фиксированная скорость не снижается при потерях. `0` оставляет это направление на умолчании. |
| `bootstrap` | HTTPS URL манифеста roster, запрашивается до аутентификации |
| `controller_pub` | 32 байта Ed25519 (64 hex). Нужен, чтобы принять roster и списки адресов |
| `tofu` | `true` закрепляет первого действительного подписанта, если `controller_pub` пуст (по умолчанию выкл) |
| `outbounds` | `type`, `url`, `path`, `cdn`, `fingerprint` |
| `use` | Имя списка → `direct` \| `via` \| `drop` |
| `default` | Действие, если ни один список не совпал (по умолчанию `via`) |
| `p2p` | `false` выключает попытки прямого соединения в оверлее. Прямой TCP зашифрован (ECDH пары) |
| `lists_url` | Переопределить адрес GET для списков адресов |
| `listen.socks5` | Адрес прослушивания |
| `listen.tun` | Пусто = выкл; `auto` создаёт utun |
| `listen.tun_addr` | Префикс TUN; адрес — IP оверлея из Status |
| `listen.kill_switch` | Удерживать системный маршрут по умолчанию, когда TUN падает |
| `listen.http` | LAN-страница оператора (MikroTik `:8090`) |
| `listen.http_pass` | Необязательный пароль этой страницы |
| `listen.bind` | То же, что `-if`; флаг CLI имеет приоритет |
| `listen.uplink` | Имя WAN-интерфейса RouterOS для выхода контейнера (`ether1`, `lte1`) |

Поля исходящего: `name`, `type` (`get` \| `ws` \| `h2` \| `quic` \| `h3`), `url`, `path`, `cdn` (`yandex` \| `cloudflare` \| пусто), `fingerprint` (`chrome` \| `firefox` \| `go`), `header_budget`, `inflight`, `h2_max_age_ms`, `insecure_skip_verify`.

`fingerprint` `chrome` — ClientHello Chrome, который клиент собирает через uTLS. `firefox` — форма другого браузера. `go` — штатный список шифров Go; фильтры JA3/JA4 уже распознают его как не-браузер. На пути с такими фильтрами протокол требует форму браузера. uTLS он при этом не требует.

Если `header_budget` не задан, используется 16 КиБ HTTP-заголовка (профиль Yandex CDN фиксирует те же 16 КиБ). Если не задан `inflight`, в этом дереве берётся 8 (диапазон протокола 2–8, по спецификации по умолчанию 4). Если не задан `h2_max_age_ms`, берётся 0: HTTP/2-соединение GET держится до ошибки. Положительное значение (не меньше 15 с, не больше 1 ч) задаёт, когда это TCP-соединение заменяется; mux и идентификатор GET-сессии сохраняются. Исходящий GET передаёт данные в Cookie `session=`; первый GET несёт тег `0x02`, а идентификатор сессии уже стоит в пути. ICMP оверлея передаётся как CmdDgram на `dst:1`; нода сама отвечает на запросы к шлюзу и отправляет настоящий ICMP echo в интернет.

Клиент не выбирает decoy SNI: его объявляет нода. CDN-фронт сохраняет hostname CDN.

Yandex Cloud CDN (группа источников, ресурс, кэш выкл, GET 16 КиБ): [yandex-cdn_ru.md](yandex-cdn_ru.md).

`bootstrap` **должен** быть хостом с настоящим сертификатом или CDN. GET на IP decoy без HMAC сессии стыкуется с origin.

Roster: `GET /static/v3/manifest.json` → `{"v":1,"p":"<base64url>"}`. Ключа участника в файле нет. После рукопожатия roster обновляется через Status. При отказе клиент перебирает фронты, неподписанные `replicas`, затем roster.

### Списки адресов

Контроллер публикует именованные множества адресов. Клиент сопоставляет имена с действиями:

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

Если хост попадает в несколько сопоставленных списков, приоритет такой: drop, затем direct, затем via. SOCKS сопоставляет по hostname, TUN — по IP (CIDR). Запрос: `GET /static/v3/lists.json`. Status несёт serial и хеш.

Имена списков — словарь оператора; для протокола это непрозрачные строки.

---

## 7. Decoy

Неавторизованный TCP (любой SNI, битый hello, открытый текст) стыкуется с `decoy:`. UDP на том же порту стыкуется с тем же origin, кроме QUIC loft, чей Initial несёт метку loft: фронты quic и h3 работают и за decoy (у клиентов начиная с этого выпуска). ClientHello loft (с действительным `session_id`) обрабатывает сама нода. При `mask: true` на этом пути показывается лист ECDSA в форме dest; зонды по-прежнему видят origin. `cdn_peers` перечисляет CIDR, которым разрешено тянуть origin по собственному SNI без sid. Когда origin — nginx на этом же сервере, `decoy_proxy enable` передаёт ему настоящий адрес клиента (для журналов, ограничений и защиты входа в панель).

`session_id` занимает 32 байта. sid v3 — HKDF под ECDH с ключом X25519 ноды (`mask_pub` / `pbk` в share-URI). sid v2 (запасной) — HKDF под ключом участника. Украденным ключом участника можно пометить только hello v2.

При включённом mask hello с SNI decoy принимается только как sid v3 (`mask_pub` / `pbk`); sid v2 стыкуется с origin. Если ClientHello предлагает X25519MLKEM768 (группа `0x11EC`), mask выбирает эту группу ([RFC 10024](https://www.rfc-editor.org/rfc/rfc10024), Post-Quantum Traditional (PQ/T) Hybrid Key Agreement Mechanisms for TLS 1.3). Лист по-прежнему не образует цепочку публичного УЦ. Повторный `client_random` в течение 10 минут стыкуется как зонд. На ноде с mask QUIC Initial принимается только с меткой v3.

Имена origin в примерах YAML — просто примеры. Выбирайте origin, публичный сайт которого на пути не решатся заблокировать и TLS которого нода умеет стыковать. В протокол они не вшиты.

---

## 8. Вне протокола

Виртуальные машины, DNS, публичные сертификаты и обновление двоичных файлов находятся вне протокола. По каналу передаются enrol, beat, slice, drain, roster и списки.

Типичное развёртывание:

1. Выпустить сертификат на публичное имя (или терминировать TLS на CDN).
2. Написать `network.conf` и запустить контроллер с `-dir`.
3. Опубликовать `controller_pub` и URL `bootstrap` на хосте, IP которого не стыкуется с decoy.
4. Запустить relay с `id`, совпадающим с записью в `relays:`.
5. Выдать участникам ключ и `client.yaml` (или файл `.loft`).

---

## 9. Сбои

| Симптом | Что проверить |
|---------|----------------|
| `token is required` | `LOFT_TOKEN` или `token:` |
| `network disabled` | `enable` в `network.conf` |
| TLS висит | decoy SNI, mask, сертификат, `session_id` |
| Манифест — чужой HTML | bootstrap попал на IP decoy без HMAC |
| Relay `enrol_no id` | id нет в `relays:`, relay выведен (drain) или ключ выпущен для другого id |
| `auth_already_online` | ключ занят другим экземпляром; включите `multi` или остановите другой экземпляр |
| `auth_limit` | `multi_limit` |
| `policy_drop` | overlay Open при `peers: hub` или drop в ACL |
| SOCKS жив, TUN молчит | `tun: auto`, привилегии, исключённые хосты исходящих |
| Системный маршрут по умолчанию пропал после падения TUN | `kill_switch`; вернуть маршруты или переподключиться |
| Ping ~1 с на GET | темп GET в простое — `keepalive/2` (не меньше 1 с); переподключитесь, чтобы клиент догнал после Dgram |
| Исходящий канал зависает / TLS bad record | раньше пропущенный в GET `inflight` по умолчанию был 1; в этом дереве теперь 8 |

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

Установщик создаёт пользователя `loft`, `/etc/loft/loft.env` (токен), самоподписанный сертификат, unit systemd и `/root/loft-client.yaml`. Токен хранится только в файле окружения. `loft-node -print-pub` печатает `controller_pub`.

---

## 11. Тесты

```bash
make test
go test ./internal/wire ./internal/crypto ./internal/mux ./internal/netconf ./internal/transport
```

Тесты протокола покрывают кадры, Hello/Status, подмену roster, списки и флот. Тесты транспорта — decoy `session_id`, запрос roster и политику оверлея. Mask на TCP loopback **может** пропускаться на части хостов. `make test` также запускает `server/debian/install_os_test.sh`.

---

## 12. GitHub Actions

Push и pull request в `main` запускают `make test`.

Тег `vX.Y.Z` должен совпадать с корневым `VERSION`. По этому тегу сборка выпускает:

- tarball и `.deb` ноды (linux amd64, arm64)
- архивы CLI (`loft`, `loft-front`, `loft-probe`, `loft-bench` для linux/darwin, amd64/arm64)
- macOS DMG с ad-hoc подписью: `loft-*-macos-arm.dmg` и `loft-*-macos-intel.dmg` (`BUMP=0`, CI не переписывает `clients/macOS/VERSION`). Раннер — Apple Silicon; для Intel Swift/Go собираются кросс-компиляцией (`ARCH=x86_64`).

и публикует GitHub Release. Dist также отправляет тег в [proxy.golang.org](https://proxy.golang.org) и просит [pkg.go.dev](https://pkg.go.dev/github.com/a3app/loft) собрать страницу модуля для этого релиза. Версия входит в имя файла (`loft-1.0.38-macos-arm.dmg`, `loft-node_1.0.38_amd64.deb`). Повторный прогон на том же теге заменяет файлы. Без тега Actions → Dist → Run workflow только выкладывает артефакты.

Локальные пакеты: `make dist`. Локальный DMG без повышения маркетинговой версии macOS: `make macos-dmg BUMP=0`. Developer ID и нотаризация остаются на Маке со связкой ключей; в Actions их нет.

# loft · клиент macOS

[English](DESIGN.md) · **Русский**

Агент в строке меню. Окно вторично. Марка, палитра и Avenir Next: [`brand/`](../../../brand/). Интерактивный макет: [`index.html`](index.html). Протокол: [docs/protocol_ru.md](../../../../docs/protocol_ru.md).

Программа Swift 5 + AppKit живёт в [`../Sources/`](../Sources/). Живая сессия — процесс `loft-front` на Go, скопированный в `loft.app/Contents/MacOS/`. In-process `GETFront.swift` — не тот путь, которым ходит приложение. Hello `loft-front` — Go-клиент (GET, WS, H2, QUIC, H3, протокол 1.0). SSTP — отдельный VPN PPP-over-HTTPS в том же GUI. VLESS — третий тип (TCP+TLS, REALITY, Vision, XHTTP). Редактор VLESS: **Ссылка** (`vless://`) или **Вручную** (поля); обе проекции пишут строку DuckSoft. Trojan/Shadowsocks — то же семейство, позже.

---

## 1. Продукт

Клиент говорит протокол 1.0. Локально отдаёт SOCKS5 и TUN (уже есть в Go CLI). Учётные данные — ключ члена `lt_…`. Пароля нет.

В v1 вошло: ключ, исходящие `get` / `ws` / `h2` / `quic` / `h3` через `loft-front`, несколько **подключений** (профили), импорт/экспорт `.loft`, правка профиля, connect/disconnect, TUN и hold. `select: auto` внутри профиля остаётся в Go CLI.

Подключение — один именованный `client.yaml`. Несколько подключений — несколько сессий. Это отдельно от cap `multi` (несколько `instance_id` на одном ключе).

Общий файл (все приложения loft): `.loft` держит **одно или несколько подключений**, типы `loft` / `sstp` / `vless` / `trojan` смешиваются. Подпись в списке — **тип · транспорт · хост**. Адрес loft — `loft://`. Спецификация: [docs/share_ru.md](../../../docs/share_ru.md).

---

## 2. Вход в протокол (1.0)

ПО `1.0.8`. Протокол `1.0` (`0x0100` в Hello). SOCKS и TUN остаются локальными. Спецификация: roster, decoy, mask, **именованные списки адресов** и пересылка оверлея обязательны для клиента на фильтрованном пути.

| В приложении | В канале |
|--------------|------------|
| Подключение (профиль, `.loft`) | Один `client.yaml` **или** элемент `connections:`: `kind`, `token` / учётка, `outbounds`, `select`, опционально `bootstrap` / `controller_pub` / `fronts` / `lists_url` / `use` / `default` / `p2p` |
| Ключ `lt_` + 48 hex | Auth blob `0x02` + HMAC; нода хранит SHA-256 |
| URL + type + path | Один outbound: `type` `get`\|`ws`\|`h2`\|`quic`\|`h3`, `url`, `path` |
| Фронты в панели статуса | Живой outbound после пересечения Hello/Status `caps` |
| IP оверлея | Status `ip` (и `mtu`, `gateway` оверлея) |
| `select: auto` | Оставшиеся фронты → следующий outbound → Status `replicas` → roster |
| Несколько outbound | Несколько профилей = несколько сессий. Отдельно от cap `multi` |
| Cap `multi` | Один ключ, несколько `instance_id`. Нода: `auth_already_online` / `auth_limit` |
| — | Персистить `instance_id` на профиль; пусто → нода чеканит, сохранить |
| — | Билет `0x03` на реконнект внутри `lease_hold` |
| About: фронты / mask | Caps, которые **этот клиент** кладёт в Hello |
| SNI decoy | Клиент не выбирает. Status / roster / проба. CDN-фронт держит hostname CDN |
| Roster | GET `{roster_path}` до auth (`bootstrap`); после — Status `roster`. Ключа в файле нет |
| Split / via / past | Протокольные **списки**: именованные множества с контроллера. Клиент мапит имена на `via` / `direct` / `drop` (`use:`). Дефолт несовпавших: `via` |
| Keenetic `.bat` / локальное добавление | Лишние локальные множества, не в канале. Те же три действия. Имена контроллера не затирать (`source: controller` vs `me` / `repo`) |
| TUN UDP | Cap `dgram`, mux `CmdDgram` |
| Оверлей к другому члену | Status `peers` hub\|mesh\|vlan. Cap `p2p` опционален; `p2p: false` = только hub-сплайс |

Hello живого `loft-front` (член, не relay): фронты, которые включает профиль (`get` / `ws` / `h2` / `quic` / `h3`), плюс `status timers hello_caps reset dgram clockless ticket decoy mask roster resume lists`. Cap `p2p` только если профиль разрешает слушать. Cap `relay` выкл. `hub:*` и `ctl:1` никогда не открываются.

После первого Status — только пересечение фронтов, в порядке этого клиента. Не-OK Status запрещает mux. GET 404 на auth — `auth_bad_key`.

Списки: Status `lists_serial` / `lists_hash` → `GET {lists_path}` (дефолт `/static/v3/lists.json`), тот же ключ Ed25519, что у roster. SOCKS матчит hostname; TUN матчит CIDR. Несколько попаданий: **drop, затем direct, затем via**.

Hold, failover, Packet Tunnel vs привилегированный помощник, «reset routes» — политика машины поверх этого маппинга. Это не caps.

**Макет ещё отстаёт от протокола:** ручной экран только GET\|WS; нет `bootstrap` / `controller_pub`; нет fetch именованных списков; split UI имеет via/bypass и нет `drop`; нет смены живого фронта после failover. Полный `.loft` уже несёт поля YAML.

---

## 3. Профили

Подключение — именованный профиль: тип (`loft` / `sstp` / `vless` / `trojan`) плюс учётка плюс outbounds. Подпись строки — тип · транспорт · хост (`loft · get · cdn.example.com`). Контекстное меню строки: Править / Экспорт `.loft` (выбранное подключение) / Удалить. Экспорт всех — `Family.loft`.

Два режима (Настройки → «Несколько outbound», тот же тумблер на списке):

| Режим | Поведение |
|-------|-----------|
| Один активный (дефолт) | Смена профиля рвёт сессию и рукопожатие заново. Список помечает «active». Popover: одна кнопка Open/Close. |
| Несколько outbound | У каждого профиля свои Open/Close и статус. Открытие одного остальные не трогает. Лампа в меню горит, если жива любая сессия. SOCKS: общий `:1080` или порт на профиль. |
| Простой вид | Главные экраны: открыть и закрыть. IP, фронт, SOCKS, TUN, spark: только когда простой вид выкл. Настройки остаются полными. |

`select: auto` выбирает outbound **внутри** YAML-профиля.

| Где | Файл |
|-----|------|
| Профили | `~/Library/Application Support/loft/profiles/<id>.yaml` |
| Указатель | `~/Library/Application Support/loft/active` (только режим одного активного) |
| Сессии | `~/Library/Application Support/loft/sessions/<id>` (какие подняты в multi) |
| Prefs | `~/Library/Application Support/loft/prefs.yaml` (`multi: true`) |
| Ключ | Keychain. UI после сохранения не показывает ключ постоянным полем plaintext. |

Импорт: `.loft` или `client.yaml`. `.loft` **может** быть сегодняшним одним документом **или** пакетом с `connections:` (одно или несколько, смешанные протоколы). Каждый элемент становится профилем. Экспорт выбранного — `Office.loft`; нескольких — `Family.loft`. В файле ключи; UI предупреждает не раздавать.

Адрес loft — `loft://<token>@host…`. Один фронт → QR `loft://`. Несколько фронтов одного подключения → `.loft` этого подключения.

Правка: имя, тип, затем блок типа. Вкладки **Соединение / Расширенные**. loft и VLESS — спокойная капсула **Ссылка / Вручную** с цепочкой и карандашом. loft: вставка `loft://` или ключ / хост / порт / GET·WS·H2·QUIC·H3 / путь. Обе проекции — один профиль. VLESS: вставка `vless://` или адрес / порт / UUID / TLS·REALITY / TCP·XHTTP и поля режима. Обе проекции пишут `vless://`. SOCKS5, туннель, DNS и mux — на «Расширенные». В multi на этом экране ещё статус и Open/Close этого профиля.

---

## 4. Маршрут / TUN

У машины один маршрут по умолчанию. SOCKS **может** быть на профиль. TUN — нет.

| Что | Где | Зачем |
|-----|-----|-------|
| Весь трафик | на **подключении** | Эта сессия — кандидат в default route. SOCKS-only маршрут не берётся. Живой TUN, владеющий маршрутом, не больше одного. |
| Отдать живому | **клиент** | Владелец маршрута умер: маршрут переезжает на другой открытый TUN. Три сессии, две мёртвые → маршрут на последней живой. |
| Держать прямой интернет закрытым | **клиент** | Последний TUN умер: нативный маршрут остаётся вниз. У машины нет пути, и она не звонит со своего IP. |
| В меню при обрыве | **клиент** | Показать «Reset routes» в меню, когда все подключения вниз. Выкл: только Настройки. |
| Split routing | **клиент** | Тумблер. Вкл: списки в таблице. Выкл: loft эти маршруты не ставит. |
| Общий SOCKS | **клиент** | Одна запись `:1080` на каждый живой outbound. Если split вкл, те же правила via/bypass. Выкл: порт на профиль. |
| Простой вид | **клиент** | Отображение. Popover, статус, список, правка: имя и Open/Close. Настройки, split, импорт остаются видны. |
| Reset routes | действие | Пользователь возвращает прямой интернет. В popover, если «В меню при обрыве» вкл. |

Hold и failover — политика машины. Класть их на подключения — политики дерутся.

Поведение:

1. Живые TUN-сессии: маршрут, SOCKS и popover сидят на последнем подключении (самое свежее). Список подключений сортируется по этой свежести.
2. Эта сессия умерла, failover вкл, другое живое осталось → popover, SOCKS и маршрут прыгают на предыдущее живое по свежести.
3. Живого TUN нет, hold вкл → сеть закрыта, лампа красная, «route closed». Если «В меню при обрыве» вкл, popover показывает «Reset routes»; иначе только Настройки.
4. Hold выкл → на последнем обрыве нативный маршрут возвращается (реальный IP достижим).

Формулировки говорят *route*, *direct Internet*, *reset*. Не говорят «kill switch» и «shield».

### 4.1 Бэкенды

Один клиент, два движка. Выбор по версии ОС, не настройка.

| macOS | Движок | Почему |
|-------|--------|--------|
| 10.13–10.14 | privileged helper (`SMJobBless`) | Packet Tunnel с Developer ID недоступен: appex только App Store. |
| 10.15+ | Packet Tunnel, **system extension** | С Catalina Apple пускает Network Extension вне стора только как sysex. |

Отдельный процесс SOCKS не ставится. Движок стартует на первом «весь трафик».

На 10.15+ привилегированный помощник не bless'ится, даже если sysex отказали: TUN остаётся выкл, SOCKS жив. Два способа править таблицу на одной машине запрещены.

Политика маршрута — один объект (`RouteEngine`). Меняется только путь в систему.

**Помощник (10.13–10.14).** Приложение открывает `utun` и ведёт стек. Помощник по XPC: `ifconfig`, `route add/delete`, снимок шлюза, hold, reset. Трафик не читает. Один админ-диалог при первой установке.

**Packet Tunnel (10.15+).** System extension, entitlement `packet-tunnel-provider-systemextension`. Первый TUN: активировать sysex и сохранить конфигурацию. В Системных настройках это сетевой профиль; в UI loft это «route», имя профиля `loft`.

Как политика ложится на `NEPacketTunnelNetworkSettings`:

| Состояние | includedRoutes | excludedRoutes | provider |
|-----------|----------------|----------------|----------|
| loft жив, весь трафик | default | bypass (+ хост фронта) | запущен |
| loft жив, split | via | bypass | запущен |
| Hold, loft мёртв | default (пакеты в utun **отбрасываются**) | bypass | **оставить работать** |
| Reset / hold выкл на обрыве | — | — | стоп |

Hold и «мимо loft, пока loft закрыт» на Network Extension работают, только пока provider запущен. Остановка возвращает нативный default.

App Store, если появится: тот же Packet Tunnel (appex или sysex по правилам стора). Привилегированный помощник из MAS выкинут.

---

## 5. Split routing

Маппинг на весь клиент, не список на профиль. Иначе три TUN-сессии спорят за префиксы.

Протокол публикует **именованные** множества (банки, видео, реклама). Приложение мапит каждое имя на `via` (через loft), `direct` (мимо loft, запомненный нативный GW) или `drop`. Несовпавшие назначения идут в `default` (SHOULD быть `via`). Тумблер split выкл: лишние маршруты не ставить; несовпавшие = via loft как раньше.

Локальный импорт Keenetic и git-репозиторий — дополнительные множества рядом с файлом контроллера. Строки контроллера обновляются с serial; `source: me` не затирают.

**Тумблер.** Списки — конфигурация. В таблицу они входят, только пока split вкл. Выкл: лишние маршруты снимаются; машина ведёт себя как без split.

**Direct next-hop.** Тот же снимок, который TUN уже берёт до захвата `0.0.0.0/0` (hold/restore нужен адрес, куда вернуть интернет). Split второй снимок не берёт. «Мимо loft» ставит более узкие маршруты на этот next-hop.

Мимо loft работает **пока loft закрыт**: hold закрыл default; банки и порталы всё равно идут на запомненный шлюз. Via loft без живого TUN снимается (отправлять некуда).

| Список | Split вкл, loft жив | Split вкл, loft закрыт | Split выкл |
|--------|---------------------|------------------------|------------|
| **Via loft** | в utun | снят | снят |
| **Past loft** | запомненный нативный | запомненный нативный | снят |
| Default | TUN / hold | hold или нативный | как раньше |

Снимок: когда split включается, если default ещё нативный, запомнить сейчас. Если loft уже держит default, взять снимок с последнего открытия. Нет снимка: «запомнить, когда loft возьмёт default».

**Общий SOCKS.** Одна запись `:1080`. Если split вкл, те же списки (via / past / какой loft). Если выкл, весь SOCKS в живой loft (владелец маршрута или первый живой). Общий выкл: порт на профиль; приложение выбирает outbound.

### 5.1 Формат файла

Keenetic грузит Windows `.bat` `route ADD`. С KeeneticOS 5.0 комментарий в конце строки: `:: rem` или `& rem`. loft читает оба, пишет `:: rem`.

**Keenetic (стандарт)** — loft импортирует как есть:

```
route ADD 142.250.0.0 MASK 255.255.0.0 0.0.0.0
route ADD 1.1.1.0 MASK 255.255.255.0 0.0.0.0 :: rem Cloudflare
```

Шлюз игнорируется (как Keenetic при бинде на интерфейс). `route add` без учёта регистра. Без комментария имя сервиса пустое. Весь файл идёт в один список: импорт спрашивает «via loft» или «past loft».

**loft extended** — тот же `.bat`. Keenetic его принимает (REM и `| via` в комментарии для него текст):

```
@echo off
REM loft-routes 1.0
REM
route ADD 142.250.0.0 MASK 255.255.0.0 0.0.0.0 :: rem YouTube | via
route ADD 77.88.8.8 MASK 255.255.255.255 0.0.0.0 :: rem Yandex DNS | bypass
```

Комментарий: `service name | via` или `| bypass` (`loft` / `direct` — синонимы). Всё до `|` — подпись в UI. Без `|`: как Keenetic, направление из заголовка `REM list: via` или из диалога.

Экспорт:

| Куда | Файл | Содержимое |
|------|------|------------|
| Keenetic | `YouTube.bat` | один список, `:: rem YouTube`, без `\| via` |
| loft | `loft-routes.bat` | оба списка, `\| via` / `\| bypass` |

Импорт: `.bat`, `.txt`, `.loft-routes`. Смешанный файл loft режется сам. Голый Keenetic открывает диалог «куда».

Эти файлы — локальные таблицы маршрутов. Они отдельно от списков адресов контроллера (`use:` в `client.yaml`), которые публикует протокол. Поздняя ревизия **может** импортировать список контроллера в ту же таблицу.

### 5.2 Репозиторий

URL сырого `.bat` (GitHub и подобное). Автообновление раз в сутки. Импорт в выбранный список (обычно «via loft»). Строки из репо помечаются `source: repo`. Обновление вручную добавленные CIDR не трогает.

Опубликованный список: [`a3app/loft-routes`](https://github.com/a3app/loft-routes) обходит деревья вроде [RockBlack-VPN/ip-address](https://github.com/RockBlack-VPN/ip-address) и собирает один loft-routes файл с `:: rem <сервис> | via` на каждой строке.

```
https://raw.githubusercontent.com/a3app/loft-routes/main/via.bat
```

### 5.3 Windows

Настройки: тумблер (строка открывает списки). Окно списков: тот же тумблер, подпись прямого маршрута. Внизу: Import / Export / Repository. Плюс: добавить сеть.

---

## 6. Информационная архитектура

```
NSStatusItem (L + окно лампы)
  └ popover 300 × auto
       exclusive:
         ├ Connect / Disconnect  |  Retry при ошибке
         ├ Reset routes  (все вниз, hold, «в меню при обрыве»)
         ├ Connection ›  (переключатель)
         ├ IP оверлея, фронт, SOCKS5
       multi:
         ├ header: loft · «open · N» | «error» | «route closed»
         ├ Reset routes  (все вниз, hold, «в меню при обрыве»)
         ├ по строке на профиль:
         │    lamp-dot  имя
         │    IP оверлея · порт   |  «failed»
         │    Open / Close / Retry
         └ лампа хедера = жёлтая, если кто-то жив, красная при ошибке или hold
       ├ Open Loft…
       └ Quit

Main / Connections   — список; внизу Import / Export / Manual; тумблер multi
Edit                 — имя, loft:// (или поля SSTP/VLESS), SOCKS, туннель;
                       вкладка «Расширенные» — mux, DNS
Onboarding           — ключ → адрес, или импорт файла
Settings             — логин, авто-connect, проверка обновлений при запуске, простой вид, multi, общий SOCKS,
                       маршрут (failover / hold / в меню при обрыве / split / reset)
Split                — via loft / past loft, импорт Keenetic+loft, репозиторий
About                — версия клиента, протокол 1.0, фронты и опции, которые
                       этот клиент реализует, колофон A³. При открытии
                       спрашивает latest у GitHub `a3app/loft`; кнопка
                       ставит macOS DMG этой машины и перезапускает. Проверка
                       при запуске (Настройки) открывает окно: Обновить / Позже /
                       не спрашивать снова (выключает преф).
```

Приложение — `LSUIElement`.

---

## 7. Меню-бар

Иконка — L как template (система красит тело). Окно — отдельный слой, не template: это лампа.

| Состояние | Тело | Окно |
|-----------|------|------|
| disconnected | template | тёмное, не горит |
| connecting | template | лампа, пульс |
| connected | template | лампа `#E8B84A`, лёгкое свечение |
| error | template | красный `#E24B4B`, свечение |

В multi: жёлтый, если жива любая сессия; красный, если любая в ошибке (красный побеждает). Закрыть все: окно гаснет.

Реализация: `NSStatusItem` с custom view или составным изображением (template-тело + цветной прямоугольник окна). Весь глиф медью не заливается.

### 7.1 Popover · multi

Это список живых сессий, не переключатель одного активного профиля.

- Хедер: марка с той же лампой, что в меню; строка «open · 2» / «open · 1 · error» / «loft closed».
- Строка: точка (лампа / серая / красная), имя, ниже IP оверлея и порт SOCKS. При ошибке вместо IP — «failed».
- Справа: одно действие: Close | Open | Retry.
- IP и порт — то, что нужно увидеть: сессия жива и куда ткнуть приложение.

Popover следует системному appearance. Акцент марки — лампа (жёлтый / красный). Сам popover цветом марки не заливается.

---

## 8. Первый запуск

Два шага. Без ключа и адреса Connect недоступен.

1. **Ключ доступа.** «Введите ключ доступа, который вам выдали.» Поле `lt_…`. Проверка: префикс `lt_` + 48 hex. Подсказка: начинается с `lt_`; показывают один раз.
2. **Адрес.** «Введите адрес, который вам выдали вместе с ключом.» URL, тип GET | WebSocket, опциональный path. «Open loft» → рукопожатие, затем popover.
3. **Или импорт файла** с экрана ключа.

Этот экран не упоминает peer, SHA-256 и revocation.

---

## 9. Главное окно

Тёмные чернила `#1A1714`, снежный текст, лампа только когда подключено. Системные настройки не копируются.

Exclusive:

- Хедер: марка + `loft` + имя активного профиля
- Большой Connect (заливка лампы, когда вкл)
- Строка: IP оверлея · имя фронта (`cdn-get`) · `127.0.0.1:1080`
- Sparkline up/down, без галереи стран
- Список outbound: имя, тип, хост URL, точка (live / standby / dead)
- `select: auto` — пробует сверху вниз; живой помечен лампой

Multi: вместо одного Connect список профилей, у каждого лампа, IP оверлея, порт SOCKS и Open/Close. Хедер показывает «N · open».

**Простой вид.** Тумблер в Настройках. Popover, статус, список, правка: имя и Open/Close (плюс смена подключения из меню). Нет IP оверлея, фронта, SOCKS, spark, бейджа TUN, GET/WS и path. Список без тумблера «Несколько». Настройки, split, импорт/экспорт как есть.

---

## 10. Тексты

Каноническая локаль — английская. Интерактивный макет по умолчанию английский (`?lang=ru`, `de`, `fr`, `pt`, `zh`). Скриншоты в `shots/` — русский и английский. В приложении русский, английский, немецкий, французский, португальский и упрощённый китайский.

| id | English |
|----|---------|
| connect | Open loft |
| disconnect | Disconnect |
| connecting | Opening… |
| key.title | Access key |
| key.body | Enter the access key you were given. |
| key.hint | Starts with lt_. It is shown once — keep it. |
| url.title | Address |
| url.body | Enter the address you were given with the key. |
| url.get / url.ws / url.h2 / url.quic / url.h3 | GET / WS / H2 / QUIC / H3 |
| front | Front |
| socks | SOCKS5 |
| launch | Open at login |
| auto | Connect automatically |
| quit | Quit loft |
| conns.sub | Each has its own key and address. One is active. |
| conns.sub.multi | Each opens on its own. |
| conns.multi | Several |
| conn.up / conn.down | open / closed |
| conn.err / conn.fail | error / failed |
| conn.retry | Retry |
| edit | Edit |
| edit.tun | All traffic |
| pref.multi | Several outbounds |
| pref.simple | Simple view |
| pref.socks.share | Shared SOCKS |
| pref.socks.port | SOCKS5 port (Settings; a connection may set its own) |
| pref.fail | Hand to a live one |
| pref.hold | Keep direct Internet closed |
| pref.popreset | In the menu on break |
| split | Split routing |
| split.d | Past loft uses the direct route from before loft. |
| split.via | Via loft |
| split.bypass | Past loft |
| route.reset | Reset routes (Settings footer) |
| route.held | route closed |
| open.one / close.one | Open / Disconnect |
| about.ver | 1.0.8 |
| about.proto | protocol · 1.0 |
| about.fronts | fronts · get · ws · quic · h2 · h3 |
| about.opts | options · mask |
| about.blurb | From the street, ordinary HTTPS. Inside, a private network. |
| about.sig | A3 |
| about.upd | Одна кнопка, по вертикали посередине между протоколами и колофоном. Окно само спрашивает GitHub при открытии. Проверка / последняя / Обновить до N / скачивание % / установка. Строки над ней нет. |
| pref.upd | Проверять обновления при запуске |
| upd.offer.title | Обновление loft |
| upd.later | Позже |
| upd.quiet | Не спрашивать снова (выключает проверку при запуске) |

Словарь на этих поверхностях: overlay, loft, front, key. About — две строки и подпись A3. Устройство марки не обсуждает. Не говорит «VPN», «protected» и «shield».

---

## 11. Хром

| Поверхность | Размер | Заметки |
|-------------|--------|---------|
| Popover | 300 × hug | `NSPopover.behavior = .transient` |
| Main | 400 × 560 | скрытый titlebar, traffic lights. Простой вид — hug. |
| Onboarding | 440 × 520 | по центру, без ресайза |
| Edit | 440 × hug | имя, тип, вкладки; поля с overlay-скроллом, если выше экрана. Save прижат снизу. VLESS TLS/REALITY и TCP/XHTTP в одной строке. |
| Settings | 480 × hug | sheet на main |
| About | 360 × 448 | версия, протоколы, blurb, обновление с GitHub, A³ |
| Offer | 360 × hug | при запуске найден новый DMG: Обновить / Позже / не спрашивать снова |

Окна и popover **hug** контент. У клиента нет постоянного скролл-гуттера. Если список перерос окно: системный overlay (`NSScrollView`, появляется на жесте).

Шрифт UI: Avenir Next Medium 13 / Demi 15 для заголовков / Regular 13 для вторичного. Моно для ключа и IP: SF Mono 12.

---

## 12. Вне v1

Карта сети, выбор страны, аккаунт/пароль, трей Windows в этом дереве. Windows — отдельный `clients/windows`. Split-tunnel как имя продукта вне; split routing как в §5 — внутри.

# Yandex Cloud CDN для loft

[English](../README.md) · **Русский** · [中文](yandex-cdn_zh.md)

Инструкция оператора: куда нажимать в консоли Yandex Cloud, чтобы GET-фронт loft шёл через Cloud CDN.

Профиль `cdn: yandex` в клиенте — не capability протокола. В канале остаётся фронт `get`. Имя Yandex в YAML только поджимает бюджет заголовков и SNI.

Через этот край проходит **только GET**. POST и WebSocket отвечают 405. Заголовки длиннее ~12 КиБ дают 413. Источник обязан ответить за 5 секунд. CDN ходит к origin только по IPv4.

Скриншоты — консоль Cloud CDN (каталог `default`). В примерах origin — `loft.example.com`, раздача — `cdn.example.com`. Подписи на экране совпадают с таблицами.

---

## Имена в примерах

Подставьте свои. В примерах так:

| Роль | Значение |
|------|----------|
| Origin (нода) | `loft.example.com:444` — `advertise https://loft.example.com`, сертификат ноды `CN=loft.example.com` |
| CDN-ресурс | `cdn.example.com` |
| CNAME раздачи | `cdn.example.com` → `<id>.topology.gslb.yccdn.ru` |
| ACME | `_acme-challenge.cdn.example.com` → `<цель из карточки>.cm.yandexcloud.net` |
| GET path | `/static/v3/build` (дефолт ноды, если `get_path` в `node.conf` пустой) |
| Группа источников | `loft-origin` |
| Панель | `#loft/node` — поле **Сертификат ноды** |

Токены и ключи членов в консоль CDN не кладутся. Они остаются в `{dir}/members.keys` и в YAML клиента.

---

## 1. Origin — нода loft, до консоли

CDN открывает **свой** TLS к ноде. HMAC из ClientHello клиента до ноды не доходит. Сессия живёт в HTTP: первый GET — `Cookie: session=` и `X-Auth: 1`, дальше тоже `Cookie: session=` (ребро часто режет `X-Request-Id`). Ответ ноды обязан донести до клиента заголовок `X-Session`.

Поэтому origin для CDN — слушатель, который **сам** отвечает на `GET` по `get_path`, а не сплайсирует чужой ClientHello в decoy.

На ноде:

```yaml
listen: ":444"
advertise: https://loft.example.com
get_path: /static/v3/build   # тот же path, что у клиента
get_h2: 2                   # второй HTTP/2 к CDN; inflight общий. 1 = как раньше
fronts: [get]               # ws/h2/quic на этом слушателе CDN не пронесёт
# decoy/mask можно оставить: ребро ходит с SNI advertise (loft.example.com),
# без session_id — нода отвечает своим сертификатом, не сплайсирует.
# Чужой SNI по-прежнему уходит в decoy. Host и SNI у CDN — loft.example.com.
```

Сертификат ноды должен покрывать Host/SNI, с которыми CDN стучится к origin. В примерах это `loft.example.com`. В панели: `#loft/node` → **Сертификат ноды** → пара из пула на это имя → Сохранить. Пути в `node.conf` остаются `/etc/loft/tls/fullchain.pem` и `privkey.pem`.

Если на ноде висит сертификат `other.example.net`, а Host у CDN — `loft.example.com`, TLS-клиенты и проверка origin у CDN не сойдутся.

Проверка origin напрямую, минуя CDN. SNI должен быть `loft.example.com` — иначе при включённом decoy рукопожатие уйдёт в сплайс:

```bash
curl -sI --max-time 5 --resolve loft.example.com:444:ORIGIN_IPV4 \
  https://loft.example.com:444/static/v3/build
```

Ожидается ответ самой ноды (cover или 404 на голый path без session), не страница decoy и не обрыв по таймауту.

Файрвол origin принимает IPv4 с адресов Yandex CDN. IPv6-only origin ребро не возьмёт.

Клиент к краю и egress ноды в интернет — тоже IPv4 (`tcp4`). Пока overlay IPv6 не выдан, DNS-заглушка отвечает на AAAA пустым NOERROR, иначе Happy Eyeballs выходит в нативный IPv6 ноды.

Нода уже ставит на GET `Cache-Control: private, no-store`, `CDN-Cache-Control: no-store`, `Vary: Cookie, X-Request-Id`. Этого мало: кэш на **ресурсе CDN** всё равно выключается (шаг 2 визарда).

Nginx перед loft GET на `:444` не нужен: нода сама origin. Если на той же машине nginx слушает 80/443 (другие сайты), loft-node — `:444`. Ребро origin pull идёт на `:444`. Nginx как в гайдах Xray (шаг 7) на этом пути нет; ставить его перед GET — буфер, `proxy_read_timeout` и срез `Cookie`/`X-Session`.

---

## 2. Платёжный аккаунт

Cloud CDN списывается с **платёжного аккаунта**. Он идентифицирует плательщика и привязан к лицевому счёту. Ресурсами (каталог, CDN) он не управляет.

Консоль: [Yandex Cloud Billing](https://console.yandex.cloud/billing) → вкладка **Аккаунт** → **Создать аккаунт**.

Официально: [создать новый платежный аккаунт](https://yandex.cloud/ru/docs/billing/operations/create-new-account).

1. Войдите Яндекс ID или аккаунтом Яндекс 360.
2. Если организации ещё нет — [Identity Hub](https://console.yandex.cloud) → создать организацию. В ней появится каталог `default`.
3. Откройте сервис **Yandex Cloud Billing**.
4. На вкладке **Аккаунт** нажмите **Создать аккаунт**. Второй и следующие аккаунты — плюс рядом с именем текущего.
5. В окне создания:
   - **Имя аккаунта** — как будет видно в списке.
   - **Организация** — та, в которой будете заводить CDN.
   - **Тип аккаунта** — **Физическое лицо** или бизнес-аккаунт (ИП / юрлицо). Тип после создания не меняется.
   - **Страна** — резидентство плательщика. В рамках одного Яндекс ID все плательщики платят в одной валюте.
   - **Способ оплаты** — банковская карта или пополнение вручную.
6. На странице аккаунта в блоке **Привязанные облака и сервисы** облако привязано к этому платёжному аккаунту. Без привязки облако неактивно, Cloud CDN в нём не создать.

Для создания аккаунта нужна роль владельца организации. Статус на странице Billing — активен или пробный период; Cloud CDN заводится уже в [консоли управления](https://console.yandex.cloud), в каталоге этого облака.

---

## 3. Группа источников

Крошки: `Cloud CDN / Группы источников / Создать`. Заголовок **Создание группы источников**. Сертификата на этой форме нет — только origin.

![Форма «Создание группы источников»: тип Сервер, одно поле «Доменное имя источника»](img/yandex-cdn/01-origin-group.jpg)

| Поле на экране | Что писать |
|----------------|------------|
| **Название группы** | `loft-origin` |
| **Тип источника** | **Сервер** (не Бакет, не L7-балансировщик) |
| **Доменное имя источника** | `loft.example.com:444` |
| **Приоритет** | **Основной** |

**Добавить источник** не нажимать. Синяя кнопка **Создать**.

CLI:

```bash
yc cdn origin-group create --name loft-origin \
  --origin source=loft.example.com:444,enabled=true \
  --provider-type ourcdn
```

Запомните `id` группы.

Документация: [создать группу источников](https://yandex.cloud/ru/docs/cdn/operations/origin-groups/create-group).

---

## 4. CDN-ресурс

Крошки: `Cloud CDN / CDN-ресурсы / Создать`. Визард **Шаг 1 из 4 — Основные настройки**.

### Шаг 1. Основные настройки (блок Контент)

Сверху формы по умолчанию стоит **Из одного источника**. Нужна уже созданная группа:

![Шаг 1: блок Контент. Переключить «Запрос контента» на «Из группы источников»](img/yandex-cdn/02-resource-step1.jpg)

| Поле на экране | Что выбрать / писать |
|----------------|----------------------|
| **Доступ к контенту** | галка стоит |
| **Запрос контента** | **Из группы источников** (группа `loft-origin`) |
| **Тип источника** / **Доменное имя источника** | не нужны, если выбрана группа |
| **Протокол для источников** | **HTTPS** |
| **Задать SNI вручную** | **включить**, значение = `loft.example.com` (без порта) |
| **Заголовок Host** | **Свое значение** |
| **Значение заголовка** | `loft.example.com` |
| **Доменное имя** | `cdn.example.com` (ваше имя раздачи). После создания имя не меняется |
| **Метки** | пусто |

Нижняя половина того же шага — Host и имя раздачи:

![Host = loft.example.com, протокол HTTPS, группа loft-origin. Доменное имя — cdn.example.com](img/yandex-cdn/03-resource-host.png)

На скрине тумблер **Задать SNI вручную** выключен. Его надо включить и поставить то же имя, что Host: `loft.example.com`. Иначе ребро придёт с SNI `cdn.example.com`, а сертификат ноды на это имя не подписан.

### Шаг 2. Кеширование

Так выглядит **дефолт**. Для loft он вредный: тело GET — AEAD, кэш и нарезка на ребре ломают сессию.

![Дефолт кеша: «Кеширование в CDN» включено, cookie игнорируются, сегментация больших файлов включена — всё это выключить](img/yandex-cdn/04-cache-defaults.jpg)

| Поле на экране | Что поставить |
|----------------|---------------|
| **Кеширование в CDN** | **выключить** (сейчас синий тумблер) |
| **Кеширование в браузере** | выключено — так и оставить |
| **Кеширование query-параметров** | **Не кешировать** |
| **Игнорировать файлы cookie** | **снять** галку |
| **gzip-сжатие** | выключено |
| **Сегментация больших файлов** | **снять** галку |

После выключения кеша в CDN блок «Как у источника / День» пропадает — так и надо.

### Шаг 3. HTTP-заголовки и методы

`Cookie`, `X-Auth`, `X-Request-Id` идут от клиента сами. Статическими их не дублировать. `X-Session` из ответа ноды до клиента доходит, пока стоит **Не скрывать**.

![Заголовки: ничего не добавлять, скрытие заголовков источника — «Не скрывать», CORS — «Не добавлять»](img/yandex-cdn/05-headers.png)

![Методы: GET, HEAD, OPTIONS. «Настраиваемый HTTP-ответ» выключен](img/yandex-cdn/06-methods.png)

| Поле на экране | Что поставить |
|----------------|---------------|
| **Заголовки запроса к источнику** → **Добавить** | не нажимать |
| **Заголовки ответа клиенту** → **Добавить** | не нажимать |
| **Скрытие заголовков источника** | **Не скрывать** |
| **CORS** / Access-Control-Allow-Origin | **Не добавлять** |
| **Методы запросов от клиентов** | GET, HEAD, OPTIONS |
| **Настраиваемый HTTP-ответ** | выкл |

### Шаг 4. Дополнительно

![Шаг 4: выгрузка логов и экранирование источников выключены. Кнопка «Продолжить»](img/yandex-cdn/07-extra.png)

| Поле на экране | Что поставить |
|----------------|---------------|
| **Выгрузка логов** | выкл |
| **Настройки экранирования** | выкл |

**Продолжить**. Применение — до 15 минут. После смены кеша сбросить кеш ресурса.

CLI (каркас):

```bash
yc cdn resource create cdn.example.com \
  --origin-group-id ORIGIN_GROUP_ID \
  --origin-protocol HTTPS \
  --host-header loft.example.com \
  --disable-cache \
  --cert-manager-ssl-cert-id CERT_ID
```

Документация: [создать ресурс](https://yandex.cloud/ru/docs/cdn/operations/resources/create-resource), [заголовки](https://yandex.cloud/ru/docs/cdn/operations/resources/configure-headers), [кэш](https://yandex.cloud/ru/docs/cdn/concepts/caching).

---

## 5. DNS и сертификат CDN

Сертификат Let's Encrypt — **на имя раздачи** (`cdn.example.com`), не на origin (`loft.example.com`). Origin уже закрыт сертификатом ноды из панели.

На форме группы источников сертификата нет. Он на ресурсе: поле **Тип сертификата**.

Проще всего выпустить его оттуда: **Тип сертификата** → Certificate Manager → выпуск Let's Encrypt на `cdn.example.com`.

Если идти в Certificate Manager отдельно (тот же каталог `default`):

1. В крошках клик по **домику** (слева от `Cloud CDN`) — это дашборд каталога, не Cloud CDN.
2. Поиск сверху консоли: `Certificate Manager`.
3. Сервис **Certificate Manager** → **Добавить сертификат** → **Сертификат от Let's Encrypt**.
4. **Домены:** `cdn.example.com`. Проверка прав — DNS. В зоне имени раздачи:

```
_acme-challenge.cdn.example.com.  CNAME  <цель из карточки сертификата>.cm.yandexcloud.net.
```

Цель берётся из карточки сертификата. Certificate Manager видит запись не сразу — подождать.

Когда статус «выпущен», в CDN-ресурсе выбирается этот сертификат. Пока висит дефолтный `*.yccdn.cloud.yandex.net`, HTTPS на `cdn.example.com` рвётся (SAN mismatch / RST).

Обход, пока LE не приклеился: ресурс в режиме HTTP-only, клиент `url: http://cdn.example.com` (без TLS на крае). Origin при этом всё равно HTTPS.

После создания ресурса консоль показывает цель вида `….topology.gslb.yccdn.ru`. В DNS:

```
cdn.example.com.  CNAME  <id>.topology.gslb.yccdn.ru.
```

Клиент проверяет этот сертификат (`insecure_skip_verify: false`).

Официально: [добавить сертификат Let's Encrypt](https://yandex.cloud/ru/docs/certificate-manager/operations/managed/cert-create).

---

## 6. Клиент

`path` outbound = `get_path` ноды (`/static/v3/build`). `url` — схема `https` и имя CDN, без порта origin.

```yaml
listen:
  socks5: 127.0.0.1:18080
select: cdn-get
outbounds:
  - name: cdn-get
    type: get
    cdn: yandex
    url: https://cdn.example.com
    path: /static/v3/build
    fingerprint: chrome
    header_budget: 12288
    inflight: 16
    insecure_skip_verify: false
```

`cdn: yandex` в `internal/cdn/yandex.go` фиксирует бюджет 12 КиБ и inflight 16. Ребро держит ~10–12 HTTP/1.1 к origin: 32 GET в полёте поднимали TTFB Cookie до ~900 мс при той же выгрузке ~3 Мбит. Явный `header_budget: 12288` страхует, если профиль не подхватился.

SNI клиента — `cdn.example.com`. Это делает транспорт сам, когда `cdn` задан.

macOS `loft-front` сейчас шьёт 16 КиБ и `Insecure`. Для проверки края используйте CLI `loft` с YAML выше или `loft-bench -cdn yandex`.

---

## 7. Проверка

Сначала cover через CDN (без токена):

```bash
curl -sI --max-time 8 https://cdn.example.com/
curl -sI --max-time 8 https://cdn.example.com/static/v3/build
```

Ожидается ответ ноды, статус не 502/504. Голый `/static/v3/build` без сессии — обычный cover/404 ноды.

Затем сессия GET. У `loft-bench` дефолтный `-path` — `/static/js`, его надо перебить:

```bash
make bench
./dist/clients/go/loft-bench \
  -cdn yandex \
  -type get \
  -url https://cdn.example.com \
  -path /static/v3/build \
  -token "$LOFT_TOKEN"
```

`-cdn yandex` ставит бюджет 12 КиБ и проверку TLS. WS через этот край не гоняется.

Либо CLI:

```bash
./dist/clients/go/loft -c cdn-client.yaml
ALL_PROXY=socks5://127.0.0.1:18080 curl -sI https://example.com
```

CLI `loft` / `loft-front` без явного порта слушают `127.0.0.1:18080`, чтобы не занять `1080` у открытого GUI. YAML — из §6 (`cdn-get` + `listen.socks5: 127.0.0.1:18080`).

---

## Типичные ответы края

| Симптом | Что проверить |
|---------|----------------|
| 413 | `header_budget` больше ~12 КиБ; клиент без `cdn: yandex` |
| 405 | WS или POST; для Yandex нужен только GET |
| 502 | Host/SNI к origin не совпадают с сертификатом ноды (`other.example.net` не покрывает `loft.example.com`); decoy сплайсирует ребро |
| 504 / origin timeout 5 с | SNI/Host к origin не advertise-имя (нода сплайсирует); файрвол; у origin нет IPv4 |
| Рукопожатие есть, mux пустой | CDN спрятал `X-Session`, либо не пропустил `Cookie` / `X-Request-Id` |
| Сессии путаются | кэш на ресурсе ещё включён; «игнорировать Cookie» включено |
| TLS клиента не сходится | CNAME ещё не разошёлся; на крае дефолтный `*.yccdn.cloud.yandex.net`, а не LE на `cdn.example.com` |
| `loft-bench` 404 / пустой mux | забыли `-path /static/v3/build` |

Лимиты края: [источники](https://yandex.cloud/ru/docs/cdn/concepts/origins) (IPv4, 5 с), [обмен с origin](https://yandex.cloud/ru/docs/cdn/concepts/servers-to-origins).

---

## Что уже есть в дереве

| Файл | Роль |
|------|------|
| `internal/cdn/yandex.go` | профиль: GET, 12 КиБ, inflight 16 |
| `internal/transport/get.go` | `setCDNHeaders`, Cookie / `X-Request-Id` / `X-Session` |
| `clients/go/configs/client.yaml` | пример outbound `cdn-get`, path `/static/v3/build` |
| `clients/go/cmd/loft-bench` | `-cdn yandex`; path задавать явно |
| `docs/img/yandex-cdn/` | скриншоты консоли к этой инструкции |

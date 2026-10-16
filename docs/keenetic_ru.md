# loft на KeeneticOS

[English](keenetic.md) · **Русский**

Как loft сидит на роутере Keenetic. Каталог: [clients/Keenetic/](../clients/Keenetic/). План: [clients/Keenetic/PLAN_ru.md](../clients/Keenetic/PLAN_ru.md). Макет: [clients/Keenetic/design/index.html](../clients/Keenetic/design/index.html). Протокол: [protocol_ru.md](protocol_ru.md).

Пакета ещё нет. Этот файл — договорённость.

---

## 1. Две плоскости

KeeneticOS решает **куда** идут пакеты (приоритеты, политики, DNS-маршруты). loft решает **как** туннель выглядит в канале (фронты loft).

```
хост LAN
  → политика Keenetic
      → OpkgTun0  (интерфейс NDM, описание Loft)
          → TUN loft  (без маршрута по умолчанию)
              → loft  get|ws|h2|quic|h3
                  → TLS 1.3 → CDN или нода
```

VLESS, когда появится, — второй исходящий loft на том же OpkgTun. SSTP остаётся штатным клиентом Keenetic.

## 2. OpkgTun

KeeneticOS 5.0+ поднимает TUN/TAP из Entware в NDM (`OpkgTun` / `OpkgTap`) и пускает эти интерфейсы в политики подключений и статику.

Эскиз после живого Status:

```
interface OpkgTun0 type OpkgTun
interface OpkgTun0 description Loft
interface OpkgTun0 security-level public
interface OpkgTun0 ip address <ip-оверлея> 255.255.255.255
interface OpkgTun0 ip tcp adjust-mss pmtu
interface OpkgTun0 up
```

Только `ndmc`. Никогда `ip route` из шелла — NDM пересобирает таблицы.

Процесс loft **не должен** ставить `0/0` и kill-switch. Хватает `listen.tun: auto`.

## 3. Раздельная маршрутизация

В вебе Keenetic:

- **Приоритеты подключений** — Loft в основной политике или в именной.
- **Политики на устройства / сегменты** — телефоны через Loft, IoT через провайдера.
- **DNS-маршруты** — список доменов → шлюз = Loft.

Списки `use:` loft на роутере по умолчанию выкл. Контроллер может по-прежнему публиковать списки; этот клиент их не берёт, пока оператор это не включит.

## 4. Петля

Политика с `0/0` в Loft утащит в туннель и GET/WS на CDN/ноду. До `up` IP исходящих пинятся на интерфейс провайдера (как endpoint у WireGuard). На десктопе loft уже исключает хосты сессии из TUN; в режиме Keenetic то же через хостовые маршруты `ndmc`.

## 5. Вход в панель

HTTP `http://<lan>:8090/`. Процесс прокидывает `Cookie` браузера на `127.0.0.1/auth`. 200 и роль admin/configurator — UI. 401 — редирект на стоковую вебадминку.

Cookie привязана к хосту, `Path=/`, поэтому `:80` и `:8090` её делят. `localhost:79` (RCI без пароля) остаётся только демону.

KeeneticOS 5.2 **может** объявить `x-ndw4-authenticate`; тогда панель берёт одноразовый токен по редиректу.

## 6. Чем этот файл не является

Не компонент Keenetic. Не новая строка в «Добавить подключение». Не патч `/usr/share/htdocs_`. Для этого нужна сама Keenetic или инжект под прошивку; панель от них не зависит.

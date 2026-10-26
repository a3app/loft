# loft на KeeneticOS

[English](keenetic.md) · **Русский** · [中文](keenetic_zh.md)

Как loft работает на роутере Keenetic. Каталог: [clients/Keenetic/](../clients/Keenetic/). План: [clients/Keenetic/PLAN.md](../clients/Keenetic/PLAN.md). Макет: [clients/Keenetic/design/index.html](../clients/Keenetic/design/index.html). Протокол: [protocol_ru.md](protocol_ru.md).

Пакет пока не собирается. Этот файл — спецификация интеграции.

---

## 1. Две плоскости

KeeneticOS решает, **куда** идут пакеты (приоритеты, политики, DNS-маршруты). loft решает, **как** туннель выглядит в канале (фронты loft).

```
хост LAN
  → политика Keenetic
      → OpkgTun0  (интерфейс NDM, описание Loft)
          → TUN loft  (без маршрута по умолчанию)
              → loft  get|ws|h2|quic|h3
                  → TLS 1.3 → CDN или нода
```

VLESS, когда его добавят, станет ещё одним исходящим loft на том же OpkgTun. SSTP остаётся штатным клиентом Keenetic.

## 2. OpkgTun

KeeneticOS 5.0+ отображает TUN/TAP из Entware в NDM (`OpkgTun` / `OpkgTap`) и позволяет использовать эти интерфейсы в политиках подключений и статических маршрутах.

Набросок после получения Status:

```
interface OpkgTun0 type OpkgTun
interface OpkgTun0 description Loft
interface OpkgTun0 security-level public
interface OpkgTun0 ip address <ip-оверлея> 255.255.255.255
interface OpkgTun0 ip tcp adjust-mss pmtu
interface OpkgTun0 up
```

Только через `ndmc`. Никогда не используйте `ip route` из шелла: NDM пересобирает таблицы.

Процесс loft **не должен** устанавливать ни `0/0`, ни kill-switch. Достаточно `listen.tun: auto`.

## 3. Раздельная маршрутизация

В вебе Keenetic:

- **Приоритеты подключений** — Loft — в основной политике или в именованной.
- **Политики на устройства / сегменты** — телефоны через Loft, IoT через провайдера.
- **DNS-маршруты** — список доменов → шлюз = Loft.

Списки `use:` loft на роутере по умолчанию выключены. Контроллер по-прежнему может публиковать списки, но этот клиент их игнорирует, пока оператор не включит эту функцию.

## 4. Петля

Политика, отправляющая `0/0` в Loft, направит в туннель и GET/WS-соединения с CDN/нодой. До `up` IP исходящих жёстко привязываются к интерфейсу провайдера (так же, как адрес пира у WireGuard). На десктопах loft уже исключает хосты сессии из TUN; в режиме Keenetic то же делается через хостовые маршруты `ndmc`.

## 5. Вход в панель

HTTP `http://<lan>:8090/`. Процесс пересылает `Cookie` браузера на `127.0.0.1/auth`. Ответ 200 и роль admin/configurator — показывается UI. Ответ 401 — редирект на штатную веб-админку.

Cookie привязана к хосту, `Path=/`, поэтому она общая для `:80` и `:8090`. `localhost:79` (RCI без пароля) доступен только демону.

KeeneticOS 5.2 **может** объявить `x-ndw4-authenticate`; тогда панель использует одноразовый токен из редиректа.

## 6. Чем этот файл не является

Это не компонент Keenetic, не новая строка в «Добавить подключение» и не патч `/usr/share/htdocs_`. Для всего этого нужна сама Keenetic или инжект под конкретную прошивку; панель от них не зависит.
